"""Отправка уведомлений о бронировании по электронной почте.

Используется только стандартная библиотека: :mod:`smtplib` и
:mod:`email.message`. Если SMTP-сервер не настроен, письмо печатается в
консоль — это позволяет проверить работу команды без реального сервера.
"""

from __future__ import annotations

import smtplib
import ssl
from email.headerregistry import Address
from email.message import EmailMessage
from typing import Protocol

from booking.config import SmtpConfig
from booking.errors import NotificationError
from booking.models import Booking, Room

_SUBJECT_TEMPLATE = "Бронь кабинета №{room_id} на {date} {time}"

_BODY_TEMPLATE = """Здравствуйте, {name}!

Кабинет забронирован за вами.

    Кабинет:      №{room_id} — {room_name}
    Дата:         {date}
    Время:        {time_range} ({duration} мин.)
    Забронировал: {name} <{email}>{purpose_line}

Номер брони: {booking_id}
Чтобы отменить бронь, выполните: python -m booking cancel {booking_id}

Это письмо сформировано автоматически, отвечать на него не нужно.
"""


def build_message(
    booking: Booking, room: Room, config: SmtpConfig
) -> EmailMessage:
    """Сформировать письмо с датой, временем и номером кабинета."""
    purpose_line = (
        f"\n    Тема:         {booking.purpose}" if booking.purpose else ""
    )
    message = EmailMessage()
    message["Subject"] = _SUBJECT_TEMPLATE.format(
        room_id=room.id,
        date=f"{booking.slot.start:%d.%m.%Y}",
        time=booking.slot.human_time(),
    )
    sender_user, _, sender_domain = config.sender.partition("@")
    message["From"] = Address(
        config.sender_name, sender_user, sender_domain
    )
    message["To"] = booking.person_email
    message.set_content(
        _BODY_TEMPLATE.format(
            name=booking.person_name,
            email=booking.person_email,
            room_id=room.id,
            room_name=room.name,
            date=f"{booking.slot.start:%d.%m.%Y}",
            time_range=booking.slot.human_time(),
            duration=booking.slot.duration_minutes,
            purpose_line=purpose_line,
            booking_id=booking.id,
        )
    )
    return message


class Notifier(Protocol):
    """Интерфейс отправителя уведомлений."""

    def send(self, message: EmailMessage) -> str:
        """Отправить письмо и вернуть описание результата."""
        ...


class ConsoleNotifier:
    """Заглушка: печатает письмо в консоль вместо отправки.

    Используется, когда SMTP не настроен, а также в режиме ``--dry-run``.
    """

    def send(self, message: EmailMessage) -> str:
        """Вывести письмо в стандартный поток вывода."""
        print("-" * 62)
        print(f"Кому:  {message['To']}")
        print(f"Тема:  {message['Subject']}")
        print("-" * 62)
        print(message.get_content().rstrip())
        print("-" * 62)
        return (
            f"письмо выведено в консоль (SMTP не настроен), "
            f"адресат: {message['To']}"
        )


class SmtpNotifier:
    """Отправка письма через реальный SMTP-сервер."""

    def __init__(self, config: SmtpConfig) -> None:
        """Сохранить параметры подключения."""
        self._config = config

    def send(self, message: EmailMessage) -> str:
        """Отправить письмо, преобразуя ошибки SMTP в доменные."""
        config = self._config
        try:
            if config.port == 465:
                server = smtplib.SMTP_SSL(
                    config.host,
                    config.port,
                    timeout=30,
                    context=ssl.create_default_context(),
                )
            else:
                server = smtplib.SMTP(config.host, config.port, timeout=30)
            with server:
                server.ehlo()
                if config.use_tls and config.port != 465:
                    server.starttls(context=ssl.create_default_context())
                    server.ehlo()
                if config.user:
                    server.login(config.user, config.password)
                server.send_message(message)
        except (smtplib.SMTPException, OSError) as exc:
            raise NotificationError(
                f"Не удалось отправить письмо на {message['To']}: {exc}"
            ) from exc
        return f"письмо отправлено на {message['To']}"


def get_notifier(config: SmtpConfig, dry_run: bool = False) -> Notifier:
    """Выбрать отправителя в зависимости от конфигурации."""
    if dry_run or not config.is_configured:
        return ConsoleNotifier()
    return SmtpNotifier(config)
