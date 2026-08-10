"""Разбор и представление интервалов времени.

Интервал считается полуоткрытым: ``[начало, конец)``. Благодаря этому
бронь с 10:00 до 11:00 не конфликтует с бронью с 11:00 до 12:00.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import date, datetime, time, timedelta

from booking.errors import ValidationError

#: Формат хранения даты и времени в SQLite (сортируется лексикографически).
DB_DATETIME_FORMAT = "%Y-%m-%d %H:%M:%S"

#: Формат даты для ввода и вывода в БД.
DB_DATE_FORMAT = "%Y-%m-%d"

_DATE_PATTERNS = ("%Y-%m-%d", "%d.%m.%Y", "%d.%m.%y")
_TIME_PATTERNS = ("%H:%M", "%H.%M", "%H%M")

_EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$")


def parse_date(raw: str) -> date:
    """Разобрать дату из строки.

    Поддерживаются форматы ``ГГГГ-ММ-ДД``, ``ДД.ММ.ГГГГ`` и ``ДД.ММ.ГГ``,
    а также ключевые слова ``сегодня``/``today`` и ``завтра``/``tomorrow``.
    """
    value = raw.strip().lower()
    if value in ("сегодня", "today"):
        return date.today()
    if value in ("завтра", "tomorrow"):
        return date.today() + timedelta(days=1)
    for pattern in _DATE_PATTERNS:
        try:
            return datetime.strptime(value, pattern).date()
        except ValueError:
            continue
    raise ValidationError(
        f"Не удалось разобрать дату {raw!r}. "
        "Ожидается формат ГГГГ-ММ-ДД, например 2026-08-11."
    )


def parse_time(raw: str) -> time:
    """Разобрать время суток из строки формата ``ЧЧ:ММ``."""
    value = raw.strip()
    for pattern in _TIME_PATTERNS:
        try:
            return datetime.strptime(value, pattern).time()
        except ValueError:
            continue
    raise ValidationError(
        f"Не удалось разобрать время {raw!r}. "
        "Ожидается формат ЧЧ:ММ, например 10:30."
    )


def validate_email(raw: str) -> str:
    """Проверить и нормализовать адрес электронной почты."""
    value = raw.strip()
    if not _EMAIL_RE.match(value):
        raise ValidationError(f"Некорректный адрес e-mail: {raw!r}.")
    return value


@dataclass(frozen=True)
class TimeSlot:
    """Полуоткрытый интервал времени ``[start, end)``."""

    start: datetime
    end: datetime

    def __post_init__(self) -> None:
        """Проверить, что интервал непустой."""
        if self.end <= self.start:
            raise ValidationError(
                "Время окончания должно быть строго позже времени начала."
            )

    @classmethod
    def from_parts(cls, day: date, start: time, end: time) -> "TimeSlot":
        """Собрать интервал из даты и двух отметок времени.

        Значение ``00:00`` в качестве времени окончания трактуется как
        полночь следующих суток.
        """
        starts_at = datetime.combine(day, start)
        ends_at = datetime.combine(day, end)
        if end == time(0, 0):
            ends_at += timedelta(days=1)
        return cls(starts_at, ends_at)

    @classmethod
    def parse(cls, day: str, start: str, end: str) -> "TimeSlot":
        """Собрать интервал из строковых аргументов командной строки."""
        return cls.from_parts(
            parse_date(day), parse_time(start), parse_time(end)
        )

    @property
    def start_db(self) -> str:
        """Начало интервала в формате хранения SQLite."""
        return self.start.strftime(DB_DATETIME_FORMAT)

    @property
    def end_db(self) -> str:
        """Конец интервала в формате хранения SQLite."""
        return self.end.strftime(DB_DATETIME_FORMAT)

    @property
    def duration_minutes(self) -> int:
        """Длительность интервала в минутах."""
        return int((self.end - self.start).total_seconds() // 60)

    def overlaps(self, other: "TimeSlot") -> bool:
        """Проверить пересечение с другим интервалом."""
        return self.start < other.end and self.end > other.start

    def human_time(self) -> str:
        """Вернуть интервал в виде ``10:00-11:30``."""
        return f"{self.start:%H:%M}-{self.end:%H:%M}"

    def human(self) -> str:
        """Вернуть интервал вместе с датой: ``11.08.2026 10:00-11:30``."""
        return f"{self.start:%d.%m.%Y} {self.human_time()}"

    def __str__(self) -> str:
        """Строковое представление совпадает с :meth:`human`."""
        return self.human()


def slot_from_db(starts_at: str, ends_at: str) -> TimeSlot:
    """Восстановить интервал из строк, прочитанных из базы данных."""
    return TimeSlot(
        datetime.strptime(starts_at, DB_DATETIME_FORMAT),
        datetime.strptime(ends_at, DB_DATETIME_FORMAT),
    )
