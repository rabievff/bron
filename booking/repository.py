"""Слой доступа к данным: SQL-запросы к таблицам ``rooms`` и ``bookings``.

Весь SQL сосредоточен здесь, поэтому переход на другую СУБД потребует
изменений только в этом модуле и в :mod:`booking.db`.
"""

from __future__ import annotations

import sqlite3
from typing import List, Optional

from booking.models import Booking, Room
from booking.slots import TimeSlot

_BOOKING_COLUMNS = (
    "id, room_id, person_name, person_email, starts_at, ends_at, "
    "purpose, created_at, notified_at"
)


def list_rooms(conn: sqlite3.Connection) -> List[Room]:
    """Вернуть все кабинеты, отсортированные по номеру."""
    rows = conn.execute(
        "SELECT id, name, capacity FROM rooms ORDER BY id"
    ).fetchall()
    return [Room.from_row(row) for row in rows]


def get_room(conn: sqlite3.Connection, room_id: int) -> Optional[Room]:
    """Вернуть кабинет по номеру или ``None``, если он не найден."""
    row = conn.execute(
        "SELECT id, name, capacity FROM rooms WHERE id = ?", (room_id,)
    ).fetchone()
    return Room.from_row(row) if row else None


def find_conflicts(
    conn: sqlite3.Connection,
    room_id: int,
    slot: TimeSlot,
    exclude_booking_id: Optional[int] = None,
) -> List[Booking]:
    """Найти брони кабинета, пересекающиеся с интервалом ``slot``.

    Интервалы полуоткрытые, поэтому условие пересечения выглядит как
    ``starts_at < новый_конец AND ends_at > новое_начало``: смежные
    брони (10:00-11:00 и 11:00-12:00) конфликтом не считаются.
    """
    sql = (
        f"SELECT {_BOOKING_COLUMNS} FROM bookings "
        "WHERE room_id = ? AND starts_at < ? AND ends_at > ?"
    )
    params: List[object] = [room_id, slot.end_db, slot.start_db]
    if exclude_booking_id is not None:
        sql += " AND id <> ?"
        params.append(exclude_booking_id)
    sql += " ORDER BY starts_at"
    rows = conn.execute(sql, params).fetchall()
    return [Booking.from_row(row) for row in rows]


def insert_booking(
    conn: sqlite3.Connection,
    room_id: int,
    person_name: str,
    person_email: str,
    slot: TimeSlot,
    purpose: str = "",
) -> int:
    """Вставить бронь и вернуть её идентификатор."""
    cursor = conn.execute(
        "INSERT INTO bookings "
        "(room_id, person_name, person_email, starts_at, ends_at, purpose) "
        "VALUES (?, ?, ?, ?, ?, ?)",
        (
            room_id,
            person_name,
            person_email,
            slot.start_db,
            slot.end_db,
            purpose,
        ),
    )
    return int(cursor.lastrowid)


def get_booking(
    conn: sqlite3.Connection, booking_id: int
) -> Optional[Booking]:
    """Вернуть бронь по идентификатору или ``None``."""
    row = conn.execute(
        f"SELECT {_BOOKING_COLUMNS} FROM bookings WHERE id = ?",
        (booking_id,),
    ).fetchone()
    return Booking.from_row(row) if row else None


def list_bookings(
    conn: sqlite3.Connection,
    room_id: Optional[int] = None,
    on_date: Optional[str] = None,
    include_past: bool = False,
) -> List[Booking]:
    """Вернуть брони с необязательной фильтрацией.

    ``on_date`` задаётся строкой ``ГГГГ-ММ-ДД``. Если ``include_past``
    выключен и дата не указана, прошедшие брони отбрасываются.
    """
    sql = f"SELECT {_BOOKING_COLUMNS} FROM bookings WHERE 1 = 1"
    params: List[object] = []
    if room_id is not None:
        sql += " AND room_id = ?"
        params.append(room_id)
    if on_date is not None:
        sql += " AND date(starts_at) = ?"
        params.append(on_date)
    elif not include_past:
        sql += " AND ends_at >= datetime('now', 'localtime')"
    sql += " ORDER BY starts_at, room_id"
    rows = conn.execute(sql, params).fetchall()
    return [Booking.from_row(row) for row in rows]


def delete_booking(conn: sqlite3.Connection, booking_id: int) -> bool:
    """Удалить бронь. Возвращает ``True``, если запись существовала."""
    cursor = conn.execute(
        "DELETE FROM bookings WHERE id = ?", (booking_id,)
    )
    return bool(cursor.rowcount)


def mark_notified(conn: sqlite3.Connection, booking_id: int) -> None:
    """Проставить отметку об успешной отправке уведомления."""
    conn.execute(
        "UPDATE bookings SET notified_at = datetime('now', 'localtime') "
        "WHERE id = ?",
        (booking_id,),
    )
