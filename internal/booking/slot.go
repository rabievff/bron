package booking

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// dbLayout — формат хранения времени в SQLite. Такие строки
// сортируются лексикографически, поэтому сравнения работают штатными
// средствами СУБД без преобразований.
const dbLayout = "2006-01-02 15:04:05"

// dateLayout — формат даты для фильтров и вывода в базе.
const dateLayout = "2006-01-02"

// Поддерживаемые форматы ввода даты и времени.
var (
	dateLayouts = []string{"2006-01-02", "02.01.2006", "02.01.06"}
	timeLayouts = []string{"15:04", "15.04", "1504"}
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$`)

// ParseDate разбирает дату из строки.
//
// Поддерживаются форматы ГГГГ-ММ-ДД, ДД.ММ.ГГГГ и ДД.ММ.ГГ, а также
// ключевые слова «сегодня» и «завтра».
func ParseDate(raw string) (time.Time, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch value {
	case "сегодня", "today":
		return today, nil
	case "завтра", "tomorrow":
		return today.AddDate(0, 0, 1), nil
	}
	for _, layout := range dateLayouts {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, validationErrorf(
		"не удалось разобрать дату %q, ожидается формат ГГГГ-ММ-ДД", raw)
}

// ParseClock разбирает время суток и возвращает часы и минуты.
func ParseClock(raw string) (hour, minute int, err error) {
	value := strings.TrimSpace(raw)
	for _, layout := range timeLayouts {
		if parsed, e := time.Parse(layout, value); e == nil {
			return parsed.Hour(), parsed.Minute(), nil
		}
	}
	return 0, 0, validationErrorf(
		"не удалось разобрать время %q, ожидается формат ЧЧ:ММ", raw)
}

// ValidateEmail проверяет и нормализует адрес электронной почты.
func ValidateEmail(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if !emailRe.MatchString(value) {
		return "", validationErrorf("некорректный адрес e-mail %q", raw)
	}
	return value, nil
}

// TimeSlot — полуоткрытый интервал времени [Start, End).
//
// Благодаря полуоткрытости бронь 10:00-11:00 не конфликтует с бронью
// 11:00-12:00: кабинет освобождается ровно в момент окончания встречи.
type TimeSlot struct {
	Start time.Time
	End   time.Time
}

// NewSlot собирает интервал из даты и двух отметок времени.
//
// Значение 00:00 в качестве окончания трактуется как полночь
// следующих суток.
func NewSlot(day time.Time, from, to string) (TimeSlot, error) {
	startHour, startMin, err := ParseClock(from)
	if err != nil {
		return TimeSlot{}, err
	}
	endHour, endMin, err := ParseClock(to)
	if err != nil {
		return TimeSlot{}, err
	}
	start := time.Date(day.Year(), day.Month(), day.Day(),
		startHour, startMin, 0, 0, time.Local)
	end := time.Date(day.Year(), day.Month(), day.Day(),
		endHour, endMin, 0, 0, time.Local)
	if endHour == 0 && endMin == 0 {
		end = end.AddDate(0, 0, 1)
	}
	if !end.After(start) {
		return TimeSlot{}, validationErrorf(
			"время окончания должно быть строго позже времени начала")
	}
	return TimeSlot{Start: start, End: end}, nil
}

// ParseSlot собирает интервал из строковых аргументов командной строки.
func ParseSlot(day, from, to string) (TimeSlot, error) {
	parsedDay, err := ParseDate(day)
	if err != nil {
		return TimeSlot{}, err
	}
	return NewSlot(parsedDay, from, to)
}

// slotFromDB восстанавливает интервал из строк, прочитанных из базы.
func slotFromDB(start, end string) (TimeSlot, error) {
	parsedStart, err := time.ParseInLocation(dbLayout, start, time.Local)
	if err != nil {
		return TimeSlot{}, fmt.Errorf("разбор starts_at: %w", err)
	}
	parsedEnd, err := time.ParseInLocation(dbLayout, end, time.Local)
	if err != nil {
		return TimeSlot{}, fmt.Errorf("разбор ends_at: %w", err)
	}
	return TimeSlot{Start: parsedStart, End: parsedEnd}, nil
}

// StartDB возвращает начало интервала в формате хранения SQLite.
func (s TimeSlot) StartDB() string { return s.Start.Format(dbLayout) }

// EndDB возвращает конец интервала в формате хранения SQLite.
func (s TimeSlot) EndDB() string { return s.End.Format(dbLayout) }

// Minutes возвращает длительность интервала в минутах.
func (s TimeSlot) Minutes() int { return int(s.End.Sub(s.Start).Minutes()) }

// Overlaps сообщает, пересекается ли интервал с другим.
func (s TimeSlot) Overlaps(other TimeSlot) bool {
	return s.Start.Before(other.End) && s.End.After(other.Start)
}

// HumanTime возвращает интервал в виде «10:00-11:30».
func (s TimeSlot) HumanTime() string {
	return s.Start.Format("15:04") + "-" + s.End.Format("15:04")
}

// Human возвращает интервал вместе с датой: «11.08.2026 10:00-11:30».
func (s TimeSlot) Human() string {
	return s.Start.Format("02.01.2006") + " " + s.HumanTime()
}
