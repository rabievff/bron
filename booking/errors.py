"""Доменные исключения системы бронирования."""

from __future__ import annotations

from typing import TYPE_CHECKING, Sequence

if TYPE_CHECKING:  # pragma: no cover - только для проверки типов
    from booking.models import Booking, Room


class BookingError(Exception):
    """Базовая ошибка предметной области.

    Все исключения этого модуля перехватываются в CLI и печатаются
    пользователю без трассировки стека.
    """


class ValidationError(BookingError):
    """Некорректные входные данные (время, e-mail, номер кабинета)."""


class RoomNotFoundError(BookingError):
    """Запрошенный кабинет отсутствует в базе данных."""

    def __init__(self, room_id: int) -> None:
        """Сохранить номер ненайденного кабинета."""
        super().__init__(f"Кабинет №{room_id} не найден.")
        self.room_id = room_id


class BookingNotFoundError(BookingError):
    """Бронь с указанным идентификатором отсутствует."""

    def __init__(self, booking_id: int) -> None:
        """Сохранить идентификатор ненайденной брони."""
        super().__init__(f"Бронь №{booking_id} не найдена.")
        self.booking_id = booking_id


class RoomBusyError(BookingError):
    """Кабинет занят на запрошенный интервал.

    Хранит список пересекающихся броней, чтобы вызывающий код мог
    показать, кем и до скольки занят кабинет.
    """

    def __init__(self, room: Room, conflicts: Sequence[Booking]) -> None:
        """Сохранить кабинет и перечень конфликтующих броней."""
        super().__init__(f"{room.label} занят на выбранное время.")
        self.room = room
        self.conflicts = list(conflicts)

    @property
    def busy_until(self) -> str:
        """Вернуть время окончания последней конфликтующей брони."""
        return max(item.slot.end for item in self.conflicts).strftime("%H:%M")


class NotificationError(BookingError):
    """Не удалось отправить уведомление на e-mail."""
