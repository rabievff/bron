"""Тесты бизнес-логики бронирования на базе SQLite в памяти."""

from __future__ import annotations

import io
import unittest
from contextlib import redirect_stdout
from datetime import date, time

from booking import db
from booking.config import AppConfig, SmtpConfig
from booking.errors import (
    BookingNotFoundError,
    RoomBusyError,
    RoomNotFoundError,
    ValidationError,
)
from booking.service import BookingService
from booking.slots import TimeSlot

DAY = date(2026, 8, 11)


def make_slot(start_hour: int, end_hour: int) -> TimeSlot:
    """Создать интервал внутри тестового дня."""
    return TimeSlot.from_parts(DAY, time(start_hour), time(end_hour))


class ServiceTestCase(unittest.TestCase):
    """Общая подготовка: база в памяти с пятью кабинетами."""

    def setUp(self) -> None:
        """Создать временную базу и сервис."""
        self.conn = db.connect(":memory:")
        db.init_schema(self.conn)
        db.seed_rooms(self.conn, 5)
        self.config = AppConfig(db_path=":memory:", smtp=SmtpConfig())
        self.service = BookingService(self.conn, self.config)

    def tearDown(self) -> None:
        """Закрыть подключение."""
        self.conn.close()

    def book_default(self, room_id: int = 3) -> object:
        """Создать типовую бронь 10:00-12:00."""
        return self.service.book(
            room_id,
            "Иван Петров",
            "ivan@example.com",
            make_slot(10, 12),
            "Планёрка",
        )


class RoomsTests(ServiceTestCase):
    """Справочник кабинетов."""

    def test_five_rooms_created(self) -> None:
        """При инициализации создаётся ровно пять кабинетов."""
        self.assertEqual(len(self.service.rooms()), 5)

    def test_seed_is_idempotent(self) -> None:
        """Повторное заполнение справочника не плодит дубликаты."""
        db.seed_rooms(self.conn, 5)
        self.assertEqual(len(self.service.rooms()), 5)

    def test_unknown_room(self) -> None:
        """Обращение к несуществующему кабинету — ошибка."""
        with self.assertRaises(RoomNotFoundError):
            self.service.get_room(99)


class AvailabilityTests(ServiceTestCase):
    """Проверка занятости."""

    def test_all_rooms_free_initially(self) -> None:
        """Пока броней нет, свободны все кабинеты."""
        results = self.service.check(make_slot(10, 11))
        self.assertEqual(len(results), 5)
        self.assertTrue(all(item.is_free for item in results))

    def test_busy_room_reports_conflict(self) -> None:
        """Занятый кабинет возвращает конфликт и время освобождения."""
        self.book_default(room_id=3)
        result = self.service.check(make_slot(11, 13), room_id=3)[0]
        self.assertFalse(result.is_free)
        self.assertEqual(result.conflicts[0].person_name, "Иван Петров")
        self.assertEqual(f"{result.busy_until:%H:%M}", "12:00")

    def test_other_rooms_stay_free(self) -> None:
        """Бронь одного кабинета не влияет на остальные."""
        self.book_default(room_id=3)
        results = self.service.check(make_slot(10, 12))
        busy = [item.room.id for item in results if not item.is_free]
        self.assertEqual(busy, [3])

    def test_adjacent_interval_is_free(self) -> None:
        """Интервал, примыкающий к брони, считается свободным."""
        self.book_default(room_id=3)
        self.assertTrue(self.service.is_free(3, make_slot(12, 13)))
        self.assertTrue(self.service.is_free(3, make_slot(9, 10)))


class BookingTests(ServiceTestCase):
    """Создание и отмена броней."""

    def test_book_persists_all_fields(self) -> None:
        """Бронь сохраняется со всеми переданными полями."""
        booking = self.book_default()
        stored = self.service.get_booking(booking.id)
        self.assertEqual(stored.room_id, 3)
        self.assertEqual(stored.person_email, "ivan@example.com")
        self.assertEqual(stored.purpose, "Планёрка")
        self.assertEqual(stored.slot, make_slot(10, 12))
        self.assertFalse(stored.is_notified)

    def test_double_booking_rejected(self) -> None:
        """Пересекающаяся бронь того же кабинета отклоняется."""
        self.book_default(room_id=3)
        with self.assertRaises(RoomBusyError) as ctx:
            self.service.book(
                3, "Мария", "maria@example.com", make_slot(11, 13)
            )
        self.assertEqual(ctx.exception.busy_until, "12:00")
        self.assertEqual(ctx.exception.conflicts[0].person_name, "Иван Петров")

    def test_failed_booking_is_not_stored(self) -> None:
        """После отказа в базе остаётся только первая бронь."""
        self.book_default(room_id=3)
        with self.assertRaises(RoomBusyError):
            self.service.book(
                3, "Мария", "maria@example.com", make_slot(11, 13)
            )
        self.assertEqual(len(self.service.agenda(room_id=3, on_date=DAY)), 1)

    def test_same_time_other_room_allowed(self) -> None:
        """То же время в другом кабинете доступно."""
        self.book_default(room_id=3)
        booking = self.service.book(
            4, "Мария", "maria@example.com", make_slot(10, 12)
        )
        self.assertEqual(booking.room_id, 4)

    def test_empty_name_rejected(self) -> None:
        """Пустое имя сотрудника не принимается."""
        with self.assertRaises(ValidationError):
            self.service.book(1, "   ", "ivan@example.com", make_slot(10, 11))

    def test_invalid_email_rejected(self) -> None:
        """Некорректный e-mail не принимается."""
        with self.assertRaises(ValidationError):
            self.service.book(1, "Иван", "ivan(at)mail", make_slot(10, 11))

    def test_outside_working_hours_rejected(self) -> None:
        """Время вне рабочих часов офиса не принимается."""
        with self.assertRaises(ValidationError):
            self.service.book(
                1, "Иван", "ivan@example.com", make_slot(6, 7)
            )

    def test_cancel_removes_booking(self) -> None:
        """Отменённая бронь исчезает и освобождает кабинет."""
        booking = self.book_default(room_id=3)
        self.service.cancel(booking.id)
        with self.assertRaises(BookingNotFoundError):
            self.service.get_booking(booking.id)
        self.assertTrue(self.service.is_free(3, make_slot(10, 12)))

    def test_cancel_unknown_booking(self) -> None:
        """Отмена несуществующей брони — ошибка."""
        with self.assertRaises(BookingNotFoundError):
            self.service.cancel(404)


class AgendaTests(ServiceTestCase):
    """Расписание броней."""

    def test_filter_by_room_and_date(self) -> None:
        """Фильтры по кабинету и дате работают совместно."""
        self.book_default(room_id=3)
        self.service.book(
            4, "Мария", "maria@example.com", make_slot(10, 12)
        )
        self.assertEqual(len(self.service.agenda(on_date=DAY)), 2)
        self.assertEqual(
            len(self.service.agenda(room_id=4, on_date=DAY)), 1
        )

    def test_sorted_by_start(self) -> None:
        """Брони возвращаются в хронологическом порядке."""
        self.service.book(
            1, "Поздний", "late@example.com", make_slot(15, 16)
        )
        self.service.book(
            1, "Ранний", "early@example.com", make_slot(9, 10)
        )
        names = [item.person_name for item in self.service.agenda(on_date=DAY)]
        self.assertEqual(names, ["Ранний", "Поздний"])


class NotificationTests(ServiceTestCase):
    """Уведомления по электронной почте."""

    def notify_quietly(self, booking_id: int, dry_run: bool = False) -> str:
        """Вызвать уведомление, подавив печать письма в отчёт тестов."""
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            return self.service.notify(booking_id, dry_run)

    def test_dry_run_does_not_mark_notified(self) -> None:
        """Режим --dry-run не проставляет отметку об отправке."""
        booking = self.book_default()
        self.notify_quietly(booking.id, dry_run=True)
        self.assertFalse(self.service.get_booking(booking.id).is_notified)

    def test_console_notifier_marks_notified(self) -> None:
        """Без SMTP письмо печатается, отметка проставляется."""
        booking = self.book_default()
        result = self.notify_quietly(booking.id)
        self.assertIn("ivan@example.com", result)
        self.assertTrue(self.service.get_booking(booking.id).is_notified)

    def test_message_contains_room_date_and_time(self) -> None:
        """В письме есть номер кабинета, дата и время брони."""
        from booking.notifier import build_message

        booking = self.book_default(room_id=3)
        room = self.service.get_room(3)
        message = build_message(booking, room, self.config.smtp)
        body = message.get_content()
        self.assertEqual(message["To"], "ivan@example.com")
        self.assertIn("№3", message["Subject"])
        self.assertIn("11.08.2026", body)
        self.assertIn("10:00-12:00", body)
        self.assertIn("Гамма", body)

    def test_notify_unknown_booking(self) -> None:
        """Уведомление по несуществующей брони — ошибка."""
        with self.assertRaises(BookingNotFoundError):
            self.service.notify(404)


if __name__ == "__main__":
    unittest.main()
