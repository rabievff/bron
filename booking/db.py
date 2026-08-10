"""Подключение к СУБД SQLite и управление схемой данных."""

from __future__ import annotations

import sqlite3
from contextlib import contextmanager
from pathlib import Path
from typing import Iterator, Union

#: DDL схемы. Выполняется идемпотентно при каждом запуске команды.
SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS rooms (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL UNIQUE,
    capacity INTEGER NOT NULL DEFAULT 0 CHECK (capacity >= 0)
);

CREATE TABLE IF NOT EXISTS bookings (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id      INTEGER NOT NULL
                 REFERENCES rooms (id) ON DELETE CASCADE,
    person_name  TEXT    NOT NULL CHECK (length(trim(person_name)) > 0),
    person_email TEXT    NOT NULL CHECK (person_email LIKE '%_@_%._%'),
    starts_at    TEXT    NOT NULL,
    ends_at      TEXT    NOT NULL,
    purpose      TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (datetime('now', 'localtime')),
    notified_at  TEXT,
    CHECK (ends_at > starts_at)
);

CREATE INDEX IF NOT EXISTS idx_bookings_room_time
    ON bookings (room_id, starts_at, ends_at);

CREATE INDEX IF NOT EXISTS idx_bookings_email
    ON bookings (person_email);
"""

#: Названия кабинетов, создаваемых при инициализации базы.
ROOM_NAMES = ("Альфа", "Бета", "Гамма", "Дельта", "Омега")


def connect(db_path: Union[str, Path]) -> sqlite3.Connection:
    """Открыть подключение к базе данных.

    Включает контроль внешних ключей и построчный доступ по именам
    колонок. ``isolation_level=None`` отключает неявные транзакции —
    управление ими явное, через :func:`transaction`.
    """
    path = Path(db_path)
    if path.parent and str(path.parent) not in ("", "."):
        path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(path), isolation_level=None)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON")
    conn.execute("PRAGMA journal_mode = WAL")
    return conn


def init_schema(conn: sqlite3.Connection) -> None:
    """Создать таблицы и индексы, если их ещё нет."""
    conn.executescript(SCHEMA_SQL)


def seed_rooms(conn: sqlite3.Connection, count: int = 5) -> int:
    """Заполнить справочник кабинетов.

    Возвращает число добавленных записей. Существующие кабинеты не
    изменяются, поэтому команду можно вызывать повторно.
    """
    added = 0
    for number in range(1, count + 1):
        index = number - 1
        name = (
            ROOM_NAMES[index]
            if index < len(ROOM_NAMES)
            else f"Кабинет {number}"
        )
        cursor = conn.execute(
            "INSERT OR IGNORE INTO rooms (id, name, capacity) "
            "VALUES (?, ?, ?)",
            (number, name, 6),
        )
        added += cursor.rowcount or 0
    return added


@contextmanager
def transaction(conn: sqlite3.Connection) -> Iterator[sqlite3.Connection]:
    """Выполнить блок в транзакции ``BEGIN IMMEDIATE``.

    Немедленная блокировка на запись гарантирует, что проверка занятости
    и вставка брони не будут разорваны параллельным процессом.
    """
    conn.execute("BEGIN IMMEDIATE")
    try:
        yield conn
    except BaseException:
        conn.execute("ROLLBACK")
        raise
    conn.execute("COMMIT")


def setup(
    db_path: Union[str, Path], room_count: int = 5
) -> sqlite3.Connection:
    """Открыть базу, создать схему и справочник кабинетов."""
    conn = connect(db_path)
    init_schema(conn)
    seed_rooms(conn, room_count)
    return conn
