"""Бизнес-логика бронирования кабинетов.

Модуль не знает ни о CLI, ни о деталях SQL: он оперирует моделями и
возбуждает доменные исключения из :mod:`booking.errors`.
"""

from __future__ import annotations

import sqlite3
from dataclasses import dataclass
from datetime import datetime
from typing import List, Optional

from booking import repository
from booking.config import AppConfig
from booking.db import transaction
from booking.errors import (
    BookingNotFoundError,
    RoomBusyError,
    RoomNotFoundError,
    ValidationError,
)
from booking.models import Booking, Room
from booking.notifier import build_message, get_notifier
from booking.slots import DB_DATE_FORMAT, TimeSlot, validate_email

#: Максимальная длительность одной брони.
MAX_DURATION_MINUTES = 12 * 60


@dataclass(frozen=True)
class RoomAvailability:
    """Результат проверки одного кабинета на заданный интервал."""

    room: Room
    conflicts: List[Booking]

    @property
    def is_free(self) -> bool:
        """Признак того, что кабинет свободен."""
        return not self.conflicts

    @property
    def busy_until(self) -> Optional[datetime]:
        """Момент освобождения кабинета внутри запрошенного интервала."""
        if not self.conflicts:
            return None
        return max(item.slot.end for item in self.conflicts)


class BookingService:
    """Операции над кабинетами и бронями."""

    def __init__(
        self, conn: sqlite3.Connection, config: AppConfig
    ) -> None:
        """Сохранить подключение к БД и конфигурацию."""
        self._conn = conn
        self._config = config

    # ------------------------------------------------------------------
    # Справочники
    # ------------------------------------------------------------------

    def rooms(self) -> List[Room]:
        """Вернуть список кабинетов офиса."""
        return repository.list_rooms(self._conn)

    def get_room(self, room_id: int) -> Room:
        """Вернуть кабинет или возбудить :class:`RoomNotFoundError`."""
        room = repository.get_room(self._conn, room_id)
        if room is None:
            raise RoomNotFoundError(room_id)
        return room

    # ------------------------------------------------------------------
    # Проверка занятости
    # ------------------------------------------------------------------

    def check(
        self, slot: TimeSlot, room_id: Optional[int] = None
    ) -> List[RoomAvailability]:
        """Проверить занятость одного или всех кабинетов на интервал.

        Если ``room_id`` не задан, проверяются все кабинеты офиса.
        """
        self._validate_slot(slot)
        rooms = [self.get_room(room_id)] if room_id else self.rooms()
        return [
            RoomAvailability(
                room=room,
                conflicts=repository.find_conflicts(
                    self._conn, room.id, slot
                ),
            )
            for room in rooms
        ]

    def is_free(self, room_id: int, slot: TimeSlot) -> bool:
        """Короткая проверка: свободен ли конкретный кабинет."""
        return self.check(slot, room_id)[0].is_free

    # ------------------------------------------------------------------
    # Бронирование
    # ------------------------------------------------------------------

    def book(
        self,
        room_id: int,
        person_name: str,
        person_email: str,
        slot: TimeSlot,
        purpose: str = "",
    ) -> Booking:
        """Забронировать кабинет на интервал.

        Проверка занятости и вставка выполняются в одной транзакции
        ``BEGIN IMMEDIATE``, поэтому два параллельных процесса не смогут
        занять один и тот же интервал.

        :raises RoomBusyError: кабинет уже занят на это время.
        """
        self._validate_slot(slot)
        name = person_name.strip()
        if not name:
            raise ValidationError("Имя сотрудника не может быть пустым.")
        email = validate_email(person_email)
        room = self.get_room(room_id)

        with transaction(self._conn):
            conflicts = repository.find_conflicts(self._conn, room.id, slot)
            if conflicts:
                raise RoomBusyError(room, conflicts)
            booking_id = repository.insert_booking(
                self._conn, room.id, name, email, slot, purpose.strip()
            )
        booking = repository.get_booking(self._conn, booking_id)
        assert booking is not None  # запись только что вставлена
        return booking

    def cancel(self, booking_id: int) -> Booking:
        """Отменить бронь и вернуть удалённую запись."""
        booking = self.get_booking(booking_id)
        with transaction(self._conn):
            repository.delete_booking(self._conn, booking_id)
        return booking

    def get_booking(self, booking_id: int) -> Booking:
        """Вернуть бронь или возбудить :class:`BookingNotFoundError`."""
        booking = repository.get_booking(self._conn, booking_id)
        if booking is None:
            raise BookingNotFoundError(booking_id)
        return booking

    def agenda(
        self,
        room_id: Optional[int] = None,
        on_date: Optional[datetime] = None,
        include_past: bool = False,
    ) -> List[Booking]:
        """Вернуть расписание броней с фильтрами по кабинету и дате."""
        if room_id is not None:
            self.get_room(room_id)
        date_filter = (
            on_date.strftime(DB_DATE_FORMAT) if on_date else None
        )
        return repository.list_bookings(
            self._conn, room_id, date_filter, include_past
        )

    # ------------------------------------------------------------------
    # Уведомления
    # ------------------------------------------------------------------

    def notify(self, booking_id: int, dry_run: bool = False) -> str:
        """Отправить письмо владельцу брони.

        В письме указаны дата, время и номер кабинета. При успешной
        отправке проставляется отметка ``notified_at``.
        """
        booking = self.get_booking(booking_id)
        room = self.get_room(booking.room_id)
        message = build_message(booking, room, self._config.smtp)
        notifier = get_notifier(self._config.smtp, dry_run)
        result = notifier.send(message)
        if not dry_run:
            with transaction(self._conn):
                repository.mark_notified(self._conn, booking_id)
        return result

    # ------------------------------------------------------------------
    # Внутренние проверки
    # ------------------------------------------------------------------

    def _validate_slot(self, slot: TimeSlot) -> None:
        """Проверить интервал на длительность и рабочие часы."""
        if slot.duration_minutes > MAX_DURATION_MINUTES:
            raise ValidationError(
                "Бронь не может длиться дольше "
                f"{MAX_DURATION_MINUTES // 60} часов."
            )
        start_ok = slot.start.time() >= self._config.workday_start
        end_ok = (
            slot.end.time() <= self._config.workday_end
            or slot.end.time() == datetime.min.time()
        )
        if not (start_ok and end_ok):
            raise ValidationError(
                "Время вне рабочих часов офиса "
                f"({self._config.workday_start:%H:%M}-"
                f"{self._config.workday_end:%H:%M})."
            )
