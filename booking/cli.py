"""Интерфейс командной строки системы бронирования.

Реализован на :mod:`argparse` из стандартной библиотеки. Каждая
подкоманда — отдельная функция ``cmd_*``, возвращающая код возврата.
"""

from __future__ import annotations

import argparse
import sqlite3
import sys
from typing import List, Optional, Sequence

from booking import __version__, db
from booking.config import AppConfig
from booking.errors import BookingError, RoomBusyError, ValidationError
from booking.models import Booking
from booking.service import BookingService, RoomAvailability
from booking.slots import TimeSlot, parse_date, validate_email

#: Код возврата: успешное выполнение.
EXIT_OK = 0
#: Код возврата: ошибка ввода или доменная ошибка.
EXIT_ERROR = 1
#: Код возврата: кабинет занят (удобно для проверок в скриптах).
EXIT_BUSY = 2

_LINE = "-" * 62


def _email_arg(raw: str) -> str:
    """Проверить e-mail на этапе разбора аргументов.

    :mod:`argparse` перехватывает только :class:`ValueError` и
    :class:`TypeError`, поэтому доменное исключение преобразуется в
    :class:`argparse.ArgumentTypeError`.
    """
    try:
        return validate_email(raw)
    except ValidationError as error:
        raise argparse.ArgumentTypeError(str(error)) from error


def _configure_stdout() -> None:
    """Перевести вывод в UTF-8, чтобы кириллица не ломала консоль."""
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            try:
                reconfigure(encoding="utf-8", errors="replace")
            except (ValueError, OSError):  # pragma: no cover
                pass


# ----------------------------------------------------------------------
# Форматирование вывода
# ----------------------------------------------------------------------


def _print_conflicts(conflicts: Sequence[Booking]) -> None:
    """Показать, кем и до скольки занят кабинет."""
    for item in conflicts:
        print(
            f"      занято до {item.slot.end:%H:%M} — "
            f"{item.person_name} <{item.person_email}>"
            + (f", тема: {item.purpose}" if item.purpose else "")
            + f" [{item.slot.human_time()}, бронь №{item.id}]"
        )


def _print_availability(results: Sequence[RoomAvailability]) -> None:
    """Вывести таблицу занятости кабинетов."""
    for result in results:
        if result.is_free:
            print(f"  [СВОБОДЕН]  {result.room.label}")
        else:
            until = result.busy_until
            print(
                f"  [ЗАНЯТ]     {result.room.label} — "
                f"освободится в {until:%H:%M}"
            )
            _print_conflicts(result.conflicts)


def _print_booking(booking: Booking, service: BookingService) -> None:
    """Подробно вывести бронь."""
    room = service.get_room(booking.room_id)
    print(f"  Бронь №{booking.id}")
    print(f"  Кабинет:  №{room.id} — {room.name}")
    print(f"  Дата:     {booking.slot.start:%d.%m.%Y}")
    print(
        f"  Время:    {booking.slot.human_time()} "
        f"({booking.slot.duration_minutes} мин.)"
    )
    print(f"  Сотрудник: {booking.person}")
    if booking.purpose:
        print(f"  Тема:     {booking.purpose}")


# ----------------------------------------------------------------------
# Подкоманды
# ----------------------------------------------------------------------


def cmd_init(args: argparse.Namespace, service: BookingService) -> int:
    """Инициализировать базу данных и справочник кабинетов."""
    rooms = service.rooms()
    print(f"База данных готова: {args.config.db_path}")
    print(f"Кабинетов в справочнике: {len(rooms)}")
    for room in rooms:
        print(f"  №{room.id} — {room.name} (мест: {room.capacity})")
    return EXIT_OK


def cmd_rooms(args: argparse.Namespace, service: BookingService) -> int:
    """Показать список кабинетов."""
    for room in service.rooms():
        print(f"  №{room.id} — {room.name} (мест: {room.capacity})")
    return EXIT_OK


def cmd_check(args: argparse.Namespace, service: BookingService) -> int:
    """Проверить занятость и предложить забронировать кабинет."""
    slot = TimeSlot.parse(args.date, args.time_from, args.time_to)
    results = service.check(slot, args.room)

    print(f"Проверка на {slot.human()}")
    print(_LINE)
    _print_availability(results)
    print(_LINE)

    free = [result.room for result in results if result.is_free]
    if not free:
        print("Свободных кабинетов на это время нет.")
        return EXIT_BUSY

    print("Свободны: " + ", ".join(f"№{room.id}" for room in free))
    if args.no_input or not sys.stdin.isatty():
        print("Забронировать: python -m booking book --room N ...")
        return EXIT_OK
    return _interactive_book(service, slot, [room.id for room in free])


def _interactive_book(
    service: BookingService, slot: TimeSlot, free_ids: List[int]
) -> int:
    """Диалог бронирования свободного кабинета."""
    print()
    default = free_ids[0]
    answer = input(
        f"Забронировать кабинет? Номер [{default}] "
        "или Enter для выхода: "
    ).strip()
    if not answer:
        return EXIT_OK
    if not answer.isdigit() or int(answer) not in free_ids:
        print("Такого свободного кабинета нет, бронирование отменено.")
        return EXIT_ERROR

    name = input("Ваше имя: ").strip()
    email = input("Ваш e-mail: ").strip()
    purpose = input("Тема встречи (можно пропустить): ").strip()

    booking = service.book(int(answer), name, email, slot, purpose)
    print()
    print("Кабинет забронирован.")
    _print_booking(booking, service)
    print()
    print(
        "Отправить уведомление: "
        f"python -m booking notify {booking.id}"
    )
    return EXIT_OK


def cmd_book(args: argparse.Namespace, service: BookingService) -> int:
    """Забронировать кабинет на указанное время."""
    slot = TimeSlot.parse(args.date, args.time_from, args.time_to)
    booking = service.book(
        args.room, args.name, args.email, slot, args.purpose or ""
    )
    print("Кабинет забронирован.")
    _print_booking(booking, service)
    if args.notify:
        print()
        print("Уведомление: " + service.notify(booking.id, args.dry_run))
    else:
        print()
        print(
            "Отправить уведомление: "
            f"python -m booking notify {booking.id}"
        )
    return EXIT_OK


def cmd_notify(args: argparse.Namespace, service: BookingService) -> int:
    """Отправить владельцу брони письмо с деталями."""
    result = service.notify(args.booking_id, args.dry_run)
    print(f"Готово: {result}")
    return EXIT_OK


def cmd_list(args: argparse.Namespace, service: BookingService) -> int:
    """Показать расписание броней."""
    on_date = parse_date(args.date) if args.date else None
    bookings = service.agenda(args.room, on_date, args.all)
    if not bookings:
        print("Броней не найдено.")
        return EXIT_OK

    rooms = {room.id: room for room in service.rooms()}
    current_day = ""
    for booking in bookings:
        day = f"{booking.slot.start:%d.%m.%Y}"
        if day != current_day:
            current_day = day
            print(f"\n{day}")
            print(_LINE)
        flag = "отправлено" if booking.is_notified else "не отправлено"
        print(
            f"  №{booking.id:<4} {booking.slot.human_time()}  "
            f"каб. №{booking.room_id} {rooms[booking.room_id].name:<8} "
            f"{booking.person_name} <{booking.person_email}>"
            + (f" — {booking.purpose}" if booking.purpose else "")
            + f"  [увед.: {flag}]"
        )
    return EXIT_OK


def cmd_cancel(args: argparse.Namespace, service: BookingService) -> int:
    """Отменить бронь по идентификатору."""
    booking = service.cancel(args.booking_id)
    print(f"Бронь №{booking.id} отменена.")
    _print_booking(booking, service)
    return EXIT_OK


# ----------------------------------------------------------------------
# Разбор аргументов
# ----------------------------------------------------------------------


def _add_slot_arguments(parser: argparse.ArgumentParser) -> None:
    """Добавить общие аргументы даты и времени."""
    parser.add_argument(
        "--date",
        "-d",
        required=True,
        metavar="ДАТА",
        help="дата в формате ГГГГ-ММ-ДД, ДД.ММ.ГГГГ, «сегодня», «завтра»",
    )
    parser.add_argument(
        "--from",
        "-f",
        dest="time_from",
        required=True,
        metavar="ЧЧ:ММ",
        help="время начала",
    )
    parser.add_argument(
        "--to",
        "-t",
        dest="time_to",
        required=True,
        metavar="ЧЧ:ММ",
        help="время окончания",
    )


def build_parser() -> argparse.ArgumentParser:
    """Собрать разборщик аргументов командной строки."""
    parser = argparse.ArgumentParser(
        prog="booking",
        description="Бронирование переговорных кабинетов офиса.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Примеры:\n"
            "  python -m booking init\n"
            "  python -m booking check -d завтра -f 10:00 -t 11:30\n"
            "  python -m booking book -r 3 -d завтра -f 10:00 -t 11:30 \\\n"
            "      --name 'Иван Петров' --email ivan@example.com\n"
            "  python -m booking notify 1\n"
        ),
    )
    parser.add_argument(
        "--version", action="version", version=f"booking {__version__}"
    )
    parser.add_argument(
        "--db",
        metavar="ПУТЬ",
        help="путь к файлу базы данных (по умолчанию office.db)",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    init_parser = subparsers.add_parser(
        "init", help="создать базу данных и справочник из 5 кабинетов"
    )
    init_parser.set_defaults(handler=cmd_init)

    rooms_parser = subparsers.add_parser(
        "rooms", help="показать список кабинетов"
    )
    rooms_parser.set_defaults(handler=cmd_rooms)

    check_parser = subparsers.add_parser(
        "check",
        help="проверить, свободен ли кабинет, и предложить бронирование",
    )
    check_parser.add_argument(
        "--room",
        "-r",
        type=int,
        metavar="N",
        help="номер кабинета; без него проверяются все кабинеты",
    )
    _add_slot_arguments(check_parser)
    check_parser.add_argument(
        "--no-input",
        action="store_true",
        help="не предлагать интерактивное бронирование",
    )
    check_parser.set_defaults(handler=cmd_check)

    book_parser = subparsers.add_parser(
        "book", help="забронировать кабинет"
    )
    book_parser.add_argument(
        "--room", "-r", type=int, required=True, metavar="N",
        help="номер кабинета",
    )
    _add_slot_arguments(book_parser)
    book_parser.add_argument(
        "--name", "-n", required=True, help="имя сотрудника"
    )
    book_parser.add_argument(
        "--email", "-e", required=True, type=_email_arg,
        help="e-mail сотрудника",
    )
    book_parser.add_argument(
        "--purpose", "-p", default="", help="тема встречи"
    )
    book_parser.add_argument(
        "--notify",
        action="store_true",
        help="сразу отправить уведомление на e-mail",
    )
    book_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="вывести письмо в консоль вместо отправки",
    )
    book_parser.set_defaults(handler=cmd_book)

    notify_parser = subparsers.add_parser(
        "notify",
        help="отправить уведомление о брони на e-mail сотрудника",
    )
    notify_parser.add_argument(
        "booking_id", type=int, metavar="НОМЕР_БРОНИ"
    )
    notify_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="вывести письмо в консоль вместо отправки",
    )
    notify_parser.set_defaults(handler=cmd_notify)

    list_parser = subparsers.add_parser(
        "list", help="показать расписание броней"
    )
    list_parser.add_argument(
        "--room", "-r", type=int, metavar="N", help="фильтр по кабинету"
    )
    list_parser.add_argument(
        "--date", "-d", metavar="ДАТА", help="фильтр по дате"
    )
    list_parser.add_argument(
        "--all", action="store_true", help="включая прошедшие брони"
    )
    list_parser.set_defaults(handler=cmd_list)

    cancel_parser = subparsers.add_parser(
        "cancel", help="отменить бронь"
    )
    cancel_parser.add_argument(
        "booking_id", type=int, metavar="НОМЕР_БРОНИ"
    )
    cancel_parser.set_defaults(handler=cmd_cancel)

    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Точка входа: разобрать аргументы и выполнить подкоманду."""
    _configure_stdout()
    parser = build_parser()
    args = parser.parse_args(argv)

    config = AppConfig.load(args.db)
    args.config = config

    conn = db.setup(config.db_path, config.room_count)
    service = BookingService(conn, config)
    try:
        return int(args.handler(args, service))
    except RoomBusyError as error:
        print(f"\n{error.room.label} занят на это время:", file=sys.stderr)
        for item in error.conflicts:
            print(
                f"  занято до {item.slot.end:%H:%M} — {item.person} "
                f"[{item.slot.human_time()}]",
                file=sys.stderr,
            )
        return EXIT_BUSY
    except BookingError as error:
        print(f"Ошибка: {error}", file=sys.stderr)
        return EXIT_ERROR
    except sqlite3.Error as error:
        print(f"Ошибка базы данных: {error}", file=sys.stderr)
        return EXIT_ERROR
    except (KeyboardInterrupt, EOFError):
        print("\nПрервано пользователем.", file=sys.stderr)
        return EXIT_ERROR
    finally:
        conn.close()


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
