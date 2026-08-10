// Package booking реализует бронирование переговорных кабинетов офиса.
package booking

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Сигнальные ошибки предметной области. Проверяются через errors.Is.
var (
	// ErrValidation — некорректные входные данные.
	ErrValidation = errors.New("некорректные данные")
	// ErrRoomNotFound — запрошенный кабинет отсутствует в базе.
	ErrRoomNotFound = errors.New("кабинет не найден")
	// ErrBookingNotFound — бронь с таким номером отсутствует.
	ErrBookingNotFound = errors.New("бронь не найдена")
	// ErrNotification — не удалось отправить письмо.
	ErrNotification = errors.New("ошибка отправки уведомления")
)

// validationError — ошибка валидации с собственным текстом.
//
// Метод Is позволяет проверять её через errors.Is(err, ErrValidation),
// не приклеивая при этом текст сигнальной ошибки к сообщению: иначе
// пользователь видел бы «Ошибка: некорректные данные: время вне...».
type validationError struct {
	msg string
}

// Error возвращает текст ошибки.
func (e validationError) Error() string { return e.msg }

// Is связывает тип с сигнальной ошибкой ErrValidation.
func (e validationError) Is(target error) bool { return target == ErrValidation }

// validationErrorf возвращает ошибку валидации с описанием причины.
func validationErrorf(format string, args ...any) error {
	return validationError{msg: fmt.Sprintf(format, args...)}
}

// RoomBusyError сообщает, что кабинет занят на запрошенный интервал.
//
// Хранит список пересекающихся броней, чтобы вызывающий код мог
// показать, кем и до скольки занят кабинет.
type RoomBusyError struct {
	Room      Room
	Conflicts []Booking
}

// Error реализует интерфейс error.
func (e *RoomBusyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s занят на это время:", e.Room.Label())
	for _, c := range e.Conflicts {
		fmt.Fprintf(&b, "\n  занято до %s — %s [%s]",
			c.Slot.End.Format("15:04"), c.Person(), c.Slot.HumanTime())
	}
	return b.String()
}

// BusyUntil возвращает время окончания последней конфликтующей брони.
func (e *RoomBusyError) BusyUntil() time.Time {
	var until time.Time
	for _, c := range e.Conflicts {
		if c.Slot.End.After(until) {
			until = c.Slot.End
		}
	}
	return until
}
