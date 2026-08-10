"""Настройки приложения, читаемые из окружения и файла ``.env``.

Файл ``.env`` разбирается вручную, чтобы не тянуть зависимость
``python-dotenv``.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import time
from pathlib import Path

from booking.errors import ValidationError
from booking.slots import parse_time

#: Корень проекта — на уровень выше пакета.
BASE_DIR = Path(__file__).resolve().parent.parent

#: Путь к файлу базы данных по умолчанию.
DEFAULT_DB_PATH = BASE_DIR / "office.db"

#: Количество кабинетов в офисе.
DEFAULT_ROOM_COUNT = 5

#: Границы рабочего дня по умолчанию.
DEFAULT_WORKDAY_START = time(8, 0)
DEFAULT_WORKDAY_END = time(22, 0)


def load_dotenv(path: Path | None = None) -> None:
    """Загрузить переменные из файла ``.env`` в ``os.environ``.

    Уже установленные переменные окружения имеют приоритет и не
    перезаписываются. Отсутствие файла не считается ошибкой.
    """
    env_path = path or BASE_DIR / ".env"
    if not env_path.is_file():
        return
    for raw_line in env_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ.setdefault(key.strip(), value.strip().strip("\"'"))


def _env_time(name: str, default: time) -> time:
    """Прочитать время из переменной окружения."""
    raw = os.environ.get(name)
    return parse_time(raw) if raw else default


@dataclass(frozen=True)
class SmtpConfig:
    """Параметры подключения к SMTP-серверу."""

    host: str = ""
    port: int = 587
    user: str = ""
    password: str = ""
    sender: str = "noreply@office.local"
    sender_name: str = "Бронирование кабинетов"
    use_tls: bool = True

    @property
    def is_configured(self) -> bool:
        """Признак того, что SMTP-сервер задан в окружении."""
        return bool(self.host)

    @classmethod
    def from_env(cls) -> "SmtpConfig":
        """Собрать конфигурацию из переменных окружения."""
        raw_port = os.environ.get("SMTP_PORT", "587")
        try:
            port = int(raw_port)
        except ValueError as exc:
            raise ValidationError(
                f"SMTP_PORT должен быть числом, получено {raw_port!r}."
            ) from exc
        return cls(
            host=os.environ.get("SMTP_HOST", ""),
            port=port,
            user=os.environ.get("SMTP_USER", ""),
            password=os.environ.get("SMTP_PASSWORD", ""),
            sender=os.environ.get("SMTP_SENDER", "noreply@office.local"),
            sender_name=os.environ.get(
                "SMTP_SENDER_NAME", "Бронирование кабинетов"
            ),
            use_tls=os.environ.get("SMTP_USE_TLS", "1") not in ("0", "false"),
        )


@dataclass(frozen=True)
class AppConfig:
    """Полная конфигурация приложения."""

    db_path: Path = DEFAULT_DB_PATH
    room_count: int = DEFAULT_ROOM_COUNT
    workday_start: time = DEFAULT_WORKDAY_START
    workday_end: time = DEFAULT_WORKDAY_END
    smtp: SmtpConfig = SmtpConfig()

    @classmethod
    def load(cls, db_path: str | None = None) -> "AppConfig":
        """Загрузить конфигурацию из ``.env`` и переменных окружения.

        Явно переданный ``db_path`` имеет приоритет над окружением.
        """
        load_dotenv()
        path = db_path or os.environ.get("BOOKING_DB")
        return cls(
            db_path=Path(path) if path else DEFAULT_DB_PATH,
            room_count=int(
                os.environ.get("BOOKING_ROOMS", DEFAULT_ROOM_COUNT)
            ),
            workday_start=_env_time(
                "BOOKING_DAY_START", DEFAULT_WORKDAY_START
            ),
            workday_end=_env_time("BOOKING_DAY_END", DEFAULT_WORKDAY_END),
            smtp=SmtpConfig.from_env(),
        )
