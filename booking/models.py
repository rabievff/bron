"""Модели предметной области: кабинет и бронь."""

from __future__ import annotations

import sqlite3
from dataclasses import dataclass
from typing import Optional

from booking.slots import TimeSlot, slot_from_db


@dataclass(frozen=True)
class Room:
    """Кабинет офиса."""

    id: int
    name: str
    capacity: int

    @classmethod
    def from_row(cls, row: sqlite3.Row) -> "Room":
        """Создать кабинет из строки выборки."""
        return cls(
            id=row["id"], name=row["name"], capacity=row["capacity"]
        )

    @property
    def label(self) -> str:
        """Читаемое название вида ``Кабинет №3 «Переговорная»``."""
        return f"Кабинет №{self.id} «{self.name}»"


@dataclass(frozen=True)
class Booking:
    """Бронь кабинета на конкретный интервал времени."""

    id: int
    room_id: int
    person_name: str
    person_email: str
    slot: TimeSlot
    purpose: str = ""
    created_at: str = ""
    notified_at: Optional[str] = None

    @classmethod
    def from_row(cls, row: sqlite3.Row) -> "Booking":
        """Создать бронь из строки выборки."""
        return cls(
            id=row["id"],
            room_id=row["room_id"],
            person_name=row["person_name"],
            person_email=row["person_email"],
            slot=slot_from_db(row["starts_at"], row["ends_at"]),
            purpose=row["purpose"] or "",
            created_at=row["created_at"],
            notified_at=row["notified_at"],
        )

    @property
    def is_notified(self) -> bool:
        """Признак того, что уведомление уже отправлено."""
        return self.notified_at is not None

    @property
    def person(self) -> str:
        """Имя и адрес владельца брони одной строкой."""
        return f"{self.person_name} <{self.person_email}>"

    def describe(self) -> str:
        """Короткое описание брони для вывода в консоль."""
        parts = [f"{self.slot.human_time()} — {self.person}"]
        if self.purpose:
            parts.append(f"({self.purpose})")
        return " ".join(parts)
