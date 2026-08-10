package booking

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// newTestService поднимает сервис на временной базе в каталоге теста.
func newTestService(t *testing.T) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(path, DefaultRoomCount)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	service := NewService(store, Config{
		DBPath:    path,
		RoomCount: DefaultRoomCount,
		DayStart:  DefaultDayStart,
		DayEnd:    DefaultDayEnd,
	})
	// Письма не должны попадать в отчёт о прогоне тестов.
	service.SetOutput(io.Discard)
	return service
}

// bookDefault создаёт типовую бронь 10:00-12:00.
func bookDefault(t *testing.T, s *Service, roomID int) Booking {
	t.Helper()
	booking, err := s.Book(roomID, "Иван Петров", "ivan@example.com",
		mustSlot(t, "10:00", "12:00"), "Планёрка")
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	return booking
}

func TestRoomsSeeded(t *testing.T) {
	s := newTestService(t)
	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 5 {
		t.Errorf("кабинетов = %d, ожидалось 5", len(rooms))
	}
}

func TestSeedRoomsIdempotent(t *testing.T) {
	s := newTestService(t)
	if err := s.store.SeedRooms(5); err != nil {
		t.Fatalf("повторный SeedRooms: %v", err)
	}
	rooms, _ := s.Rooms()
	if len(rooms) != 5 {
		t.Errorf("после повторного заполнения кабинетов = %d", len(rooms))
	}
}

func TestUnknownRoom(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Room(99); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("ожидалась ErrRoomNotFound, получено %v", err)
	}
}

func TestAllRoomsFreeInitially(t *testing.T) {
	s := newTestService(t)
	results, err := s.Check(mustSlot(t, "10:00", "11:00"), 0)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("проверено кабинетов = %d, ожидалось 5", len(results))
	}
	for _, r := range results {
		if !r.IsFree() {
			t.Errorf("%s должен быть свободен", r.Room.Label())
		}
	}
}

func TestBusyRoomReportsConflict(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	results, err := s.Check(mustSlot(t, "11:00", "13:00"), 3)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	result := results[0]
	if result.IsFree() {
		t.Fatal("кабинет должен быть занят")
	}
	if result.Conflicts[0].PersonName != "Иван Петров" {
		t.Errorf("владелец = %q", result.Conflicts[0].PersonName)
	}
	if got := result.BusyUntil(); got != "12:00" {
		t.Errorf("BusyUntil = %q, ожидалось 12:00", got)
	}
}

func TestOtherRoomsStayFree(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	results, _ := s.Check(mustSlot(t, "10:00", "12:00"), 0)
	var busy []int
	for _, r := range results {
		if !r.IsFree() {
			busy = append(busy, r.Room.ID)
		}
	}
	if len(busy) != 1 || busy[0] != 3 {
		t.Errorf("занятые кабинеты = %v, ожидался только 3", busy)
	}
}

func TestAdjacentIntervalIsFree(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	for _, pair := range [][2]string{{"12:00", "13:00"}, {"09:00", "10:00"}} {
		free, err := s.IsFree(3, mustSlot(t, pair[0], pair[1]))
		if err != nil {
			t.Fatalf("IsFree: %v", err)
		}
		if !free {
			t.Errorf("смежный интервал %v должен быть свободен", pair)
		}
	}
}

func TestBookPersistsFields(t *testing.T) {
	s := newTestService(t)
	created := bookDefault(t, s, 3)

	stored, err := s.Booking(created.ID)
	if err != nil {
		t.Fatalf("Booking: %v", err)
	}
	if stored.RoomID != 3 || stored.PersonEmail != "ivan@example.com" {
		t.Errorf("сохранено неверно: %+v", stored)
	}
	if stored.Purpose != "Планёрка" {
		t.Errorf("тема = %q", stored.Purpose)
	}
	if stored.IsNotified() {
		t.Error("новая бронь не должна быть помечена как уведомлённая")
	}
	if got := stored.Slot.HumanTime(); got != "10:00-12:00" {
		t.Errorf("интервал = %q", got)
	}
}

func TestDoubleBookingRejected(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	_, err := s.Book(3, "Мария", "maria@example.com",
		mustSlot(t, "11:00", "13:00"), "")
	var busy *RoomBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("ожидалась RoomBusyError, получено %v", err)
	}
	if busy.Conflicts[0].PersonName != "Иван Петров" {
		t.Errorf("в конфликте не тот сотрудник: %+v", busy.Conflicts[0])
	}
	if got := busy.BusyUntil().Format("15:04"); got != "12:00" {
		t.Errorf("BusyUntil = %s", got)
	}
}

func TestFailedBookingNotStored(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	_, _ = s.Book(3, "Мария", "maria@example.com",
		mustSlot(t, "11:00", "13:00"), "")

	bookings, err := s.Agenda(3, "2026-08-11", true)
	if err != nil {
		t.Fatalf("Agenda: %v", err)
	}
	if len(bookings) != 1 {
		t.Errorf("броней = %d, ожидалась 1 (откат транзакции)", len(bookings))
	}
}

func TestSameTimeOtherRoomAllowed(t *testing.T) {
	s := newTestService(t)
	bookDefault(t, s, 3)

	booking, err := s.Book(4, "Мария", "maria@example.com",
		mustSlot(t, "10:00", "12:00"), "")
	if err != nil {
		t.Fatalf("бронь в другом кабинете должна проходить: %v", err)
	}
	if booking.RoomID != 4 {
		t.Errorf("кабинет = %d", booking.RoomID)
	}
}

func TestBookValidation(t *testing.T) {
	s := newTestService(t)
	cases := []struct {
		name          string
		person, email string
		from, to      string
	}{
		{"пустое имя", "   ", "ivan@example.com", "10:00", "11:00"},
		{"плохой e-mail", "Иван", "ivan(at)mail", "10:00", "11:00"},
		{"вне рабочих часов", "Иван", "ivan@example.com", "06:00", "07:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Book(1, c.person, c.email, mustSlot(t, c.from, c.to), "")
			if !errors.Is(err, ErrValidation) {
				t.Errorf("ожидалась ErrValidation, получено %v", err)
			}
		})
	}
}

func TestCancelFreesRoom(t *testing.T) {
	s := newTestService(t)
	created := bookDefault(t, s, 3)

	if _, err := s.Cancel(created.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := s.Booking(created.ID); !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("бронь должна исчезнуть, получено %v", err)
	}
	free, _ := s.IsFree(3, mustSlot(t, "10:00", "12:00"))
	if !free {
		t.Error("после отмены кабинет должен быть свободен")
	}
}

func TestCancelUnknownBooking(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Cancel(404); !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("ожидалась ErrBookingNotFound, получено %v", err)
	}
}

func TestAgendaSortedByStart(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Book(1, "Поздний", "late@example.com",
		mustSlot(t, "15:00", "16:00"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Book(1, "Ранний", "early@example.com",
		mustSlot(t, "09:00", "10:00"), ""); err != nil {
		t.Fatal(err)
	}

	bookings, _ := s.Agenda(0, "2026-08-11", true)
	if len(bookings) != 2 {
		t.Fatalf("броней = %d", len(bookings))
	}
	if bookings[0].PersonName != "Ранний" || bookings[1].PersonName != "Поздний" {
		t.Errorf("порядок нарушен: %s, %s",
			bookings[0].PersonName, bookings[1].PersonName)
	}
}

func TestNotifyMarksBooking(t *testing.T) {
	s := newTestService(t)
	created := bookDefault(t, s, 3)

	result, err := s.Notify(created.ID, false)
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !strings.Contains(result, "ivan@example.com") {
		t.Errorf("в результате нет адресата: %q", result)
	}
	stored, _ := s.Booking(created.ID)
	if !stored.IsNotified() {
		t.Error("после отправки должна стоять отметка notified_at")
	}
}

func TestNotifyDryRunDoesNotMark(t *testing.T) {
	s := newTestService(t)
	created := bookDefault(t, s, 3)

	if _, err := s.Notify(created.ID, true); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	stored, _ := s.Booking(created.ID)
	if stored.IsNotified() {
		t.Error("режим dry-run не должен проставлять отметку")
	}
}

func TestNotifyUnknownBooking(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Notify(404, true); !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("ожидалась ErrBookingNotFound, получено %v", err)
	}
}

func TestMessageContainsRoomDateTime(t *testing.T) {
	s := newTestService(t)
	created := bookDefault(t, s, 3)
	room, _ := s.Room(3)

	message := BuildMessage(created, room, SMTPConfig{
		Sender: "noreply@office.local", SenderName: "Бронирование"})

	if message.To != "ivan@example.com" {
		t.Errorf("получатель = %q", message.To)
	}
	for _, want := range []string{"№3", "11.08.2026", "10:00-12:00"} {
		if !strings.Contains(message.Subject+message.Body, want) {
			t.Errorf("в письме нет %q", want)
		}
	}
	if !strings.Contains(message.Body, "Гамма") {
		t.Error("в письме нет названия кабинета")
	}
}
