package booking

import (
	"fmt"
	"io"
	"strings"
)

// maxDurationMinutes — максимальная длительность одной брони.
const maxDurationMinutes = 12 * 60

// Service содержит бизнес-логику бронирования. Он не знает ни о выводе
// в консоль, ни о деталях SQL.
type Service struct {
	store  *Store
	config Config
	// notifier подменяет отправителя писем; если nil, он выбирается
	// по конфигурации.
	notifier Notifier
	// out — поток, куда печатается письмо, когда SMTP не настроен.
	out io.Writer
}

// NewService создаёт сервис поверх хранилища.
func NewService(store *Store, config Config) *Service {
	return &Service{store: store, config: config}
}

// SetNotifier подменяет отправителя уведомлений.
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// SetOutput задаёт поток для вывода письма при ненастроенном SMTP.
func (s *Service) SetOutput(w io.Writer) { s.out = w }

// Rooms возвращает список кабинетов офиса.
func (s *Service) Rooms() ([]Room, error) { return s.store.Rooms() }

// Room возвращает кабинет по номеру.
func (s *Service) Room(id int) (Room, error) { return s.store.Room(id) }

// Booking возвращает бронь по номеру.
func (s *Service) Booking(id int) (Booking, error) { return s.store.Booking(id) }

// Check проверяет занятость кабинетов на интервал.
//
// Если roomID равен нулю, проверяются все кабинеты офиса.
func (s *Service) Check(slot TimeSlot, roomID int) ([]Availability, error) {
	if err := s.validateSlot(slot); err != nil {
		return nil, err
	}
	var rooms []Room
	if roomID > 0 {
		room, err := s.store.Room(roomID)
		if err != nil {
			return nil, err
		}
		rooms = []Room{room}
	} else {
		var err error
		if rooms, err = s.store.Rooms(); err != nil {
			return nil, err
		}
	}

	results := make([]Availability, 0, len(rooms))
	for _, room := range rooms {
		conflicts, err := s.store.Conflicts(room.ID, slot)
		if err != nil {
			return nil, err
		}
		results = append(results, Availability{Room: room, Conflicts: conflicts})
	}
	return results, nil
}

// IsFree сообщает, свободен ли конкретный кабинет на интервал.
func (s *Service) IsFree(roomID int, slot TimeSlot) (bool, error) {
	results, err := s.Check(slot, roomID)
	if err != nil {
		return false, err
	}
	return results[0].IsFree(), nil
}

// Book бронирует кабинет на интервал.
//
// Если кабинет занят, возвращается *RoomBusyError со списком
// пересекающихся броней.
func (s *Service) Book(roomID int, name, email string, slot TimeSlot,
	purpose string) (Booking, error) {

	if err := s.validateSlot(slot); err != nil {
		return Booking{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Booking{}, validationErrorf("имя сотрудника не может быть пустым")
	}
	email, err := ValidateEmail(email)
	if err != nil {
		return Booking{}, err
	}
	room, err := s.store.Room(roomID)
	if err != nil {
		return Booking{}, err
	}

	id, err := s.store.InsertBooking(room, name, email, slot,
		strings.TrimSpace(purpose))
	if err != nil {
		return Booking{}, err
	}
	return s.store.Booking(id)
}

// Cancel отменяет бронь и возвращает удалённую запись.
func (s *Service) Cancel(id int) (Booking, error) {
	booking, err := s.store.Booking(id)
	if err != nil {
		return Booking{}, err
	}
	if err := s.store.DeleteBooking(id); err != nil {
		return Booking{}, err
	}
	return booking, nil
}

// Agenda возвращает расписание броней с фильтрами.
func (s *Service) Agenda(roomID int, onDate string,
	includePast bool) ([]Booking, error) {

	if roomID > 0 {
		if _, err := s.store.Room(roomID); err != nil {
			return nil, err
		}
	}
	return s.store.ListBookings(roomID, onDate, includePast)
}

// Notify отправляет владельцу брони письмо с датой, временем и номером
// кабинета. При успешной отправке проставляется отметка notified_at.
func (s *Service) Notify(bookingID int, dryRun bool) (string, error) {
	booking, err := s.store.Booking(bookingID)
	if err != nil {
		return "", err
	}
	room, err := s.store.Room(booking.RoomID)
	if err != nil {
		return "", err
	}

	notifier := s.notifier
	if notifier == nil {
		notifier = NewNotifier(s.config.SMTP, dryRun, s.out)
	}
	message := BuildMessage(booking, room, s.config.SMTP)
	result, err := notifier.Send(message)
	if err != nil {
		return "", err
	}
	if !dryRun {
		if err := s.store.MarkNotified(bookingID); err != nil {
			return "", err
		}
	}
	return result, nil
}

// validateSlot проверяет интервал на длительность и рабочие часы.
func (s *Service) validateSlot(slot TimeSlot) error {
	if slot.Minutes() > maxDurationMinutes {
		return validationErrorf("бронь не может длиться дольше %d часов",
			maxDurationMinutes/60)
	}
	startHour, startMin, err := ParseClock(s.config.DayStart)
	if err != nil {
		return err
	}
	endHour, endMin, err := ParseClock(s.config.DayEnd)
	if err != nil {
		return err
	}

	slotStart := slot.Start.Hour()*60 + slot.Start.Minute()
	slotEnd := slot.End.Hour()*60 + slot.End.Minute()
	if slotEnd == 0 {
		slotEnd = 24 * 60
	}
	dayStart := startHour*60 + startMin
	dayEnd := endHour*60 + endMin

	if slotStart < dayStart || slotEnd > dayEnd {
		return validationErrorf("время вне рабочих часов офиса (%s-%s)",
			s.config.DayStart, s.config.DayEnd)
	}
	return nil
}

// describeBooking формирует подробное описание брони для вывода.
func describeBooking(b Booking, room Room) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "  Бронь №%d\n", b.ID)
	fmt.Fprintf(&sb, "  Кабинет:   №%d — %s\n", room.ID, room.Name)
	fmt.Fprintf(&sb, "  Дата:      %s\n", b.Slot.Start.Format("02.01.2006"))
	fmt.Fprintf(&sb, "  Время:     %s (%d мин.)\n", b.Slot.HumanTime(),
		b.Slot.Minutes())
	fmt.Fprintf(&sb, "  Сотрудник: %s", b.Person())
	if b.Purpose != "" {
		fmt.Fprintf(&sb, "\n  Тема:      %s", b.Purpose)
	}
	return sb.String()
}
