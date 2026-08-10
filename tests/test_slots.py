"""Тесты разбора даты, времени и пересечения интервалов."""

from __future__ import annotations

import unittest
from datetime import date, datetime, time, timedelta

from booking.errors import ValidationError
from booking.slots import (
    TimeSlot,
    parse_date,
    parse_time,
    slot_from_db,
    validate_email,
)


class ParseDateTests(unittest.TestCase):
    """Разбор дат в разных форматах."""

    def test_iso_format(self) -> None:
        """Формат ГГГГ-ММ-ДД поддерживается."""
        self.assertEqual(parse_date("2026-08-11"), date(2026, 8, 11))

    def test_russian_format(self) -> None:
        """Формат ДД.ММ.ГГГГ поддерживается."""
        self.assertEqual(parse_date("11.08.2026"), date(2026, 8, 11))

    def test_keywords(self) -> None:
        """Ключевые слова «сегодня» и «завтра» распознаются."""
        self.assertEqual(parse_date("сегодня"), date.today())
        self.assertEqual(
            parse_date("завтра"), date.today() + timedelta(days=1)
        )

    def test_invalid_date(self) -> None:
        """Неразбираемая строка приводит к ValidationError."""
        with self.assertRaises(ValidationError):
            parse_date("32 февраля")


class ParseTimeTests(unittest.TestCase):
    """Разбор времени суток."""

    def test_colon_format(self) -> None:
        """Формат ЧЧ:ММ поддерживается."""
        self.assertEqual(parse_time("10:30"), time(10, 30))

    def test_invalid_time(self) -> None:
        """Некорректное время приводит к ValidationError."""
        with self.assertRaises(ValidationError):
            parse_time("25:00")


class ValidateEmailTests(unittest.TestCase):
    """Проверка адресов электронной почты."""

    def test_valid(self) -> None:
        """Корректный адрес нормализуется."""
        self.assertEqual(
            validate_email("  ivan@example.com "), "ivan@example.com"
        )

    def test_invalid(self) -> None:
        """Адрес без домена отвергается."""
        for raw in ("ivan", "ivan@localhost", "@example.com", "a b@c.ru"):
            with self.subTest(raw=raw), self.assertRaises(ValidationError):
                validate_email(raw)


class TimeSlotTests(unittest.TestCase):
    """Поведение интервала времени."""

    def setUp(self) -> None:
        """Подготовить базовый интервал 10:00-11:00."""
        self.day = date(2026, 8, 11)
        self.slot = TimeSlot.from_parts(self.day, time(10), time(11))

    def test_empty_slot_rejected(self) -> None:
        """Конец интервала должен быть строго позже начала."""
        with self.assertRaises(ValidationError):
            TimeSlot.from_parts(self.day, time(11), time(11))
        with self.assertRaises(ValidationError):
            TimeSlot.from_parts(self.day, time(12), time(11))

    def test_midnight_end_moves_to_next_day(self) -> None:
        """Окончание в 00:00 трактуется как полночь следующих суток."""
        slot = TimeSlot.from_parts(self.day, time(23), time(0, 0))
        self.assertEqual(slot.end, datetime(2026, 8, 12, 0, 0))

    def test_overlapping_slots(self) -> None:
        """Пересекающиеся интервалы определяются корректно."""
        inside = TimeSlot.from_parts(self.day, time(10, 30), time(10, 45))
        crossing = TimeSlot.from_parts(self.day, time(9), time(10, 30))
        self.assertTrue(self.slot.overlaps(inside))
        self.assertTrue(self.slot.overlaps(crossing))

    def test_adjacent_slots_do_not_overlap(self) -> None:
        """Смежные интервалы конфликтом не считаются."""
        before = TimeSlot.from_parts(self.day, time(9), time(10))
        after = TimeSlot.from_parts(self.day, time(11), time(12))
        self.assertFalse(self.slot.overlaps(before))
        self.assertFalse(self.slot.overlaps(after))

    def test_duration(self) -> None:
        """Длительность считается в минутах."""
        self.assertEqual(self.slot.duration_minutes, 60)

    def test_db_round_trip(self) -> None:
        """Интервал сериализуется в БД и читается обратно без потерь."""
        restored = slot_from_db(self.slot.start_db, self.slot.end_db)
        self.assertEqual(restored, self.slot)


if __name__ == "__main__":
    unittest.main()
