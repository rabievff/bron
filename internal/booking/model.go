package booking

import "fmt"

// Room — кабинет офиса.
type Room struct {
	ID       int
	Name     string
	Capacity int
}

// Label возвращает читаемое название вида «Кабинет №3 «Гамма»».
func (r Room) Label() string {
	return fmt.Sprintf("Кабинет №%d «%s»", r.ID, r.Name)
}

// Booking — бронь кабинета на конкретный интервал времени.
type Booking struct {
	ID          int
	RoomID      int
	PersonName  string
	PersonEmail string
	Slot        TimeSlot
	Purpose     string
	CreatedAt   string
	NotifiedAt  string
}

// IsNotified сообщает, отправлено ли уведомление по этой брони.
func (b Booking) IsNotified() bool { return b.NotifiedAt != "" }

// Person возвращает имя и адрес владельца брони одной строкой.
func (b Booking) Person() string {
	return fmt.Sprintf("%s <%s>", b.PersonName, b.PersonEmail)
}

// Availability — результат проверки одного кабинета на интервал.
type Availability struct {
	Room      Room
	Conflicts []Booking
}

// IsFree сообщает, свободен ли кабинет на запрошенное время.
func (a Availability) IsFree() bool { return len(a.Conflicts) == 0 }

// BusyUntil возвращает время освобождения кабинета в формате ЧЧ:ММ.
// Для свободного кабинета возвращается пустая строка.
func (a Availability) BusyUntil() string {
	if a.IsFree() {
		return ""
	}
	until := a.Conflicts[0].Slot.End
	for _, c := range a.Conflicts[1:] {
		if c.Slot.End.After(until) {
			until = c.Slot.End
		}
	}
	return until.Format("15:04")
}
