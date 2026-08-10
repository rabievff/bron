package booking

import (
	"errors"
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	want := time.Date(2026, 8, 11, 0, 0, 0, 0, time.Local)
	cases := map[string]string{
		"формат ГГГГ-ММ-ДД": "2026-08-11",
		"формат ДД.ММ.ГГГГ": "11.08.2026",
		"формат ДД.ММ.ГГ":   "11.08.26",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseDate(raw)
			if err != nil {
				t.Fatalf("ParseDate(%q) вернул ошибку: %v", raw, err)
			}
			if !got.Equal(want) {
				t.Errorf("ParseDate(%q) = %v, ожидалось %v", raw, got, want)
			}
		})
	}
}

func TestParseDateKeywords(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	got, err := ParseDate("сегодня")
	if err != nil || !got.Equal(today) {
		t.Errorf("«сегодня» = %v (err=%v), ожидалось %v", got, err, today)
	}
	got, err = ParseDate("завтра")
	if err != nil || !got.Equal(today.AddDate(0, 0, 1)) {
		t.Errorf("«завтра» = %v (err=%v)", got, err)
	}
}

func TestParseDateInvalid(t *testing.T) {
	if _, err := ParseDate("32 февраля"); !errors.Is(err, ErrValidation) {
		t.Errorf("ожидалась ErrValidation, получено %v", err)
	}
}

func TestParseClockInvalid(t *testing.T) {
	if _, _, err := ParseClock("25:00"); !errors.Is(err, ErrValidation) {
		t.Errorf("ожидалась ErrValidation, получено %v", err)
	}
}

func TestValidateEmail(t *testing.T) {
	got, err := ValidateEmail("  ivan@example.com ")
	if err != nil || got != "ivan@example.com" {
		t.Errorf("ValidateEmail = %q (err=%v)", got, err)
	}
	for _, raw := range []string{"ivan", "ivan@localhost", "@example.com", "a b@c.ru"} {
		if _, err := ValidateEmail(raw); !errors.Is(err, ErrValidation) {
			t.Errorf("ValidateEmail(%q) должен отклонять адрес", raw)
		}
	}
}

// testDay — общая дата для тестов интервалов.
var testDay = time.Date(2026, 8, 11, 0, 0, 0, 0, time.Local)

func mustSlot(t *testing.T, from, to string) TimeSlot {
	t.Helper()
	slot, err := NewSlot(testDay, from, to)
	if err != nil {
		t.Fatalf("NewSlot(%s, %s): %v", from, to, err)
	}
	return slot
}

func TestNewSlotRejectsEmptyRange(t *testing.T) {
	for _, pair := range [][2]string{{"11:00", "11:00"}, {"12:00", "11:00"}} {
		if _, err := NewSlot(testDay, pair[0], pair[1]); !errors.Is(err, ErrValidation) {
			t.Errorf("интервал %v должен отклоняться", pair)
		}
	}
}

func TestNewSlotMidnightEnd(t *testing.T) {
	slot := mustSlot(t, "23:00", "00:00")
	want := time.Date(2026, 8, 12, 0, 0, 0, 0, time.Local)
	if !slot.End.Equal(want) {
		t.Errorf("окончание = %v, ожидалось %v", slot.End, want)
	}
}

func TestOverlaps(t *testing.T) {
	base := mustSlot(t, "10:00", "11:00")
	cases := []struct {
		name     string
		from, to string
		want     bool
	}{
		{"вложенный интервал", "10:30", "10:45", true},
		{"пересечение слева", "09:00", "10:30", true},
		{"пересечение справа", "10:30", "12:00", true},
		{"смежный слева", "09:00", "10:00", false},
		{"смежный справа", "11:00", "12:00", false},
		{"не пересекается", "14:00", "15:00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			other := mustSlot(t, c.from, c.to)
			if got := base.Overlaps(other); got != c.want {
				t.Errorf("Overlaps(%s) = %v, ожидалось %v",
					other.HumanTime(), got, c.want)
			}
		})
	}
}

func TestSlotMinutes(t *testing.T) {
	if got := mustSlot(t, "10:00", "11:30").Minutes(); got != 90 {
		t.Errorf("Minutes = %d, ожидалось 90", got)
	}
}

func TestSlotDBRoundTrip(t *testing.T) {
	slot := mustSlot(t, "10:00", "11:30")
	restored, err := slotFromDB(slot.StartDB(), slot.EndDB())
	if err != nil {
		t.Fatalf("slotFromDB: %v", err)
	}
	if !restored.Start.Equal(slot.Start) || !restored.End.Equal(slot.End) {
		t.Errorf("после round-trip = %v, ожидалось %v", restored, slot)
	}
}
