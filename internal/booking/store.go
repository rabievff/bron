package booking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	// Чистый Go-драйвер SQLite: не требует cgo и компилятора C.
	// Стандартная библиотека Go предоставляет только интерфейс
	// database/sql, драйверов СУБД в ней нет.
	_ "modernc.org/sqlite"
)

// schemaSQL — DDL схемы, выполняется идемпотентно при каждом запуске.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS rooms (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL UNIQUE,
    capacity INTEGER NOT NULL DEFAULT 0 CHECK (capacity >= 0)
);

CREATE TABLE IF NOT EXISTS bookings (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id      INTEGER NOT NULL
                 REFERENCES rooms (id) ON DELETE CASCADE,
    person_name  TEXT    NOT NULL CHECK (length(trim(person_name)) > 0),
    person_email TEXT    NOT NULL CHECK (person_email LIKE '%_@_%._%'),
    starts_at    TEXT    NOT NULL,
    ends_at      TEXT    NOT NULL,
    purpose      TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (datetime('now', 'localtime')),
    notified_at  TEXT,
    CHECK (ends_at > starts_at)
);

CREATE INDEX IF NOT EXISTS idx_bookings_room_time
    ON bookings (room_id, starts_at, ends_at);

CREATE INDEX IF NOT EXISTS idx_bookings_email
    ON bookings (person_email);
`

// roomNames — названия кабинетов, создаваемых при инициализации.
var roomNames = []string{"Альфа", "Бета", "Гамма", "Дельта", "Омега"}

// bookingColumns — общий список колонок для выборок броней.
const bookingColumns = `id, room_id, person_name, person_email,
	starts_at, ends_at, purpose, created_at, COALESCE(notified_at, '')`

// Store инкапсулирует доступ к СУБД. Весь SQL собран в этом файле,
// поэтому переход на другую СУБД затронет только его.
type Store struct {
	db *sql.DB
}

// OpenStore открывает базу, создаёт схему и справочник кабинетов.
//
// В строке подключения задаются busy_timeout и режим транзакций
// IMMEDIATE: он берёт блокировку на запись сразу, поэтому проверка
// занятости и вставка брони не могут быть разорваны параллельным
// процессом.
func OpenStore(path string, roomCount int) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("создание каталога базы: %w", err)
		}
	}
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("подключение к базе: %w", err)
	}
	// Одно соединение исключает гонки между горутинами и делает
	// поведение предсказуемым для файловой БД.
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(roomCount); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// migrate создаёт схему и заполняет справочник кабинетов.
func (s *Store) migrate(roomCount int) error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("создание схемы: %w", err)
	}
	return s.SeedRooms(roomCount)
}

// SeedRooms заполняет справочник кабинетов.
//
// Существующие кабинеты не изменяются, поэтому вызов идемпотентен.
func (s *Store) SeedRooms(count int) error {
	for number := 1; number <= count; number++ {
		name := fmt.Sprintf("Кабинет %d", number)
		if number <= len(roomNames) {
			name = roomNames[number-1]
		}
		_, err := s.db.Exec(
			`INSERT OR IGNORE INTO rooms (id, name, capacity) VALUES (?, ?, ?)`,
			number, name, 6)
		if err != nil {
			return fmt.Errorf("добавление кабинета %d: %w", number, err)
		}
	}
	return nil
}

// Close закрывает подключение к базе.
func (s *Store) Close() error { return s.db.Close() }

// Rooms возвращает все кабинеты, отсортированные по номеру.
func (s *Store) Rooms() ([]Room, error) {
	rows, err := s.db.Query(`SELECT id, name, capacity FROM rooms ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("выборка кабинетов: %w", err)
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var room Room
		if err := rows.Scan(&room.ID, &room.Name, &room.Capacity); err != nil {
			return nil, fmt.Errorf("чтение кабинета: %w", err)
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

// Room возвращает кабинет по номеру.
func (s *Store) Room(id int) (Room, error) {
	var room Room
	err := s.db.QueryRow(
		`SELECT id, name, capacity FROM rooms WHERE id = ?`, id).
		Scan(&room.ID, &room.Name, &room.Capacity)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("%w: №%d", ErrRoomNotFound, id)
	}
	if err != nil {
		return Room{}, fmt.Errorf("выборка кабинета: %w", err)
	}
	return room, nil
}

// scanBookings читает брони из курсора.
func scanBookings(rows *sql.Rows) ([]Booking, error) {
	var bookings []Booking
	for rows.Next() {
		var (
			b          Booking
			start, end string
		)
		err := rows.Scan(&b.ID, &b.RoomID, &b.PersonName, &b.PersonEmail,
			&start, &end, &b.Purpose, &b.CreatedAt, &b.NotifiedAt)
		if err != nil {
			return nil, fmt.Errorf("чтение брони: %w", err)
		}
		if b.Slot, err = slotFromDB(start, end); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

// conflictsTx ищет брони кабинета, пересекающиеся с интервалом.
//
// Интервалы полуоткрытые, поэтому условие пересечения записывается как
// starts_at < новый_конец AND ends_at > новое_начало: смежные брони
// 10:00-11:00 и 11:00-12:00 конфликтом не считаются.
func conflictsTx(q queryer, roomID int, slot TimeSlot) ([]Booking, error) {
	rows, err := q.Query(`SELECT `+bookingColumns+` FROM bookings
		WHERE room_id = ? AND starts_at < ? AND ends_at > ?
		ORDER BY starts_at`,
		roomID, slot.EndDB(), slot.StartDB())
	if err != nil {
		return nil, fmt.Errorf("поиск пересечений: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// queryer объединяет *sql.DB и *sql.Tx для переиспользования запросов.
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// Conflicts ищет пересекающиеся брони вне транзакции.
func (s *Store) Conflicts(roomID int, slot TimeSlot) ([]Booking, error) {
	return conflictsTx(s.db, roomID, slot)
}

// InsertBooking атомарно проверяет занятость и вставляет бронь.
//
// Проверка и вставка выполняются в одной транзакции, иначе два
// параллельных процесса могли бы оба пройти проверку и занять один
// и тот же интервал.
func (s *Store) InsertBooking(room Room, name, email string, slot TimeSlot,
	purpose string) (int, error) {

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("начало транзакции: %w", err)
	}
	defer tx.Rollback()

	conflicts, err := conflictsTx(tx, room.ID, slot)
	if err != nil {
		return 0, err
	}
	if len(conflicts) > 0 {
		return 0, &RoomBusyError{Room: room, Conflicts: conflicts}
	}

	result, err := tx.Exec(`INSERT INTO bookings
		(room_id, person_name, person_email, starts_at, ends_at, purpose)
		VALUES (?, ?, ?, ?, ?, ?)`,
		room.ID, name, email, slot.StartDB(), slot.EndDB(), purpose)
	if err != nil {
		return 0, fmt.Errorf("вставка брони: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("получение номера брони: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("фиксация транзакции: %w", err)
	}
	return int(id), nil
}

// Booking возвращает бронь по номеру.
func (s *Store) Booking(id int) (Booking, error) {
	rows, err := s.db.Query(
		`SELECT `+bookingColumns+` FROM bookings WHERE id = ?`, id)
	if err != nil {
		return Booking{}, fmt.Errorf("выборка брони: %w", err)
	}
	defer rows.Close()

	bookings, err := scanBookings(rows)
	if err != nil {
		return Booking{}, err
	}
	if len(bookings) == 0 {
		return Booking{}, fmt.Errorf("%w: №%d", ErrBookingNotFound, id)
	}
	return bookings[0], nil
}

// ListBookings возвращает брони с фильтрами по кабинету и дате.
//
// Пустая дата означает отсутствие фильтра; при includePast == false
// прошедшие брони отбрасываются.
func (s *Store) ListBookings(roomID int, onDate string,
	includePast bool) ([]Booking, error) {

	query := `SELECT ` + bookingColumns + ` FROM bookings WHERE 1 = 1`
	var args []any
	if roomID > 0 {
		query += ` AND room_id = ?`
		args = append(args, roomID)
	}
	switch {
	case onDate != "":
		query += ` AND date(starts_at) = ?`
		args = append(args, onDate)
	case !includePast:
		query += ` AND ends_at >= datetime('now', 'localtime')`
	}
	query += ` ORDER BY starts_at, room_id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("выборка расписания: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// DeleteBooking удаляет бронь по номеру.
func (s *Store) DeleteBooking(id int) error {
	result, err := s.db.Exec(`DELETE FROM bookings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("удаление брони: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("проверка удаления: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: №%d", ErrBookingNotFound, id)
	}
	return nil
}

// MarkNotified проставляет отметку об успешной отправке уведомления.
func (s *Store) MarkNotified(id int) error {
	_, err := s.db.Exec(
		`UPDATE bookings SET notified_at = datetime('now', 'localtime')
		 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("отметка об уведомлении: %w", err)
	}
	return nil
}
