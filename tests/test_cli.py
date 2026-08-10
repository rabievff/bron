"""Тесты интерфейса командной строки."""

from __future__ import annotations

import io
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import date, timedelta
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from booking.cli import EXIT_BUSY, EXIT_ERROR, EXIT_OK, main

TOMORROW = (date.today() + timedelta(days=1)).strftime("%Y-%m-%d")


class CliTestCase(unittest.TestCase):
    """Запуск команд на временной базе данных."""

    def setUp(self) -> None:
        """Создать временный каталог под базу."""
        self._tmp = TemporaryDirectory()
        self.db_path = str(Path(self._tmp.name) / "test.db")
        self.addCleanup(self._tmp.cleanup)

    def run_cli(self, *args: str) -> "tuple[int, str]":
        """Выполнить команду и вернуть код возврата и вывод.

        Перехватываются оба потока: сообщения об ошибках печатаются в
        stderr и иначе засоряли бы отчёт о прогоне тестов.
        """
        buffer = io.StringIO()
        with redirect_stdout(buffer), redirect_stderr(buffer):
            code = main(["--db", self.db_path, *args])
        return code, buffer.getvalue()

    def book_default(self) -> None:
        """Забронировать кабинет №3 на завтра с 10:00 до 11:30."""
        code, _ = self.run_cli(
            "book", "-r", "3", "-d", TOMORROW,
            "-f", "10:00", "-t", "11:30",
            "-n", "Иван Петров", "-e", "ivan@example.com",
            "-p", "Планёрка",
        )
        self.assertEqual(code, EXIT_OK)


class CheckCommandTests(CliTestCase):
    """Команда проверки занятости."""

    def test_all_rooms_free(self) -> None:
        """Свободны все пять кабинетов."""
        code, out = self.run_cli(
            "check", "-d", TOMORROW, "-f", "10:00", "-t", "11:00",
            "--no-input",
        )
        self.assertEqual(code, EXIT_OK)
        self.assertEqual(out.count("[СВОБОДЕН]"), 5)

    def test_busy_room_shows_owner_and_time(self) -> None:
        """Для занятого кабинета видно, кем и до скольки он занят."""
        self.book_default()
        code, out = self.run_cli(
            "check", "-r", "3", "-d", TOMORROW, "-f", "11:00", "-t", "12:00",
            "--no-input",
        )
        self.assertEqual(code, EXIT_BUSY)
        self.assertIn("[ЗАНЯТ]", out)
        self.assertIn("занято до 11:30", out)
        self.assertIn("Иван Петров", out)
        self.assertIn("ivan@example.com", out)

    def test_interactive_booking(self) -> None:
        """Диалог после проверки создаёт бронь."""
        answers = iter(
            ["2", "Мария Сидорова", "maria@example.com", "Ретро"]
        )
        with patch("builtins.input", lambda _: next(answers)), patch(
            "sys.stdin.isatty", return_value=True
        ):
            code, out = self.run_cli(
                "check", "-d", TOMORROW, "-f", "14:00", "-t", "15:00"
            )
        self.assertEqual(code, EXIT_OK)
        self.assertIn("Кабинет забронирован", out)

        _, listing = self.run_cli("list", "-d", TOMORROW)
        self.assertIn("Мария Сидорова", listing)
        self.assertIn("каб. №2", listing)

    def test_interactive_booking_declined(self) -> None:
        """Пустой ответ отменяет бронирование."""
        with patch("builtins.input", lambda _: ""), patch(
            "sys.stdin.isatty", return_value=True
        ):
            code, _ = self.run_cli(
                "check", "-d", TOMORROW, "-f", "14:00", "-t", "15:00"
            )
        self.assertEqual(code, EXIT_OK)
        _, listing = self.run_cli("list", "-d", TOMORROW)
        self.assertIn("Броней не найдено", listing)

    def test_interactive_booking_busy_room_rejected(self) -> None:
        """Занятый кабинет нельзя выбрать в диалоге."""
        self.book_default()
        with patch("builtins.input", lambda _: "3"), patch(
            "sys.stdin.isatty", return_value=True
        ):
            code, out = self.run_cli(
                "check", "-d", TOMORROW, "-f", "10:00", "-t", "11:00"
            )
        self.assertEqual(code, EXIT_ERROR)
        self.assertIn("бронирование отменено", out)


class BookCommandTests(CliTestCase):
    """Команда бронирования."""

    def test_conflict_returns_busy_code(self) -> None:
        """Повторная бронь на пересекающееся время возвращает код 2."""
        self.book_default()
        code, _ = self.run_cli(
            "book", "-r", "3", "-d", TOMORROW,
            "-f", "11:00", "-t", "12:00",
            "-n", "Мария", "-e", "maria@example.com",
        )
        self.assertEqual(code, EXIT_BUSY)

    def test_unknown_room_returns_error(self) -> None:
        """Несуществующий кабинет возвращает код 1."""
        code, _ = self.run_cli(
            "book", "-r", "99", "-d", TOMORROW,
            "-f", "10:00", "-t", "11:00",
            "-n", "Иван", "-e", "ivan@example.com",
        )
        self.assertEqual(code, EXIT_ERROR)

    def test_notify_flag_prints_letter(self) -> None:
        """Флаг --notify выводит письмо с датой, временем и кабинетом."""
        code, out = self.run_cli(
            "book", "-r", "1", "-d", TOMORROW,
            "-f", "10:00", "-t", "11:00",
            "-n", "Иван", "-e", "ivan@example.com",
            "--notify", "--dry-run",
        )
        self.assertEqual(code, EXIT_OK)
        self.assertIn("Кабинет:      №1", out)
        self.assertIn("10:00-11:00", out)


class CancelCommandTests(CliTestCase):
    """Команда отмены брони."""

    def test_cancel_frees_room(self) -> None:
        """После отмены кабинет снова свободен."""
        self.book_default()
        code, _ = self.run_cli("cancel", "1")
        self.assertEqual(code, EXIT_OK)
        code, out = self.run_cli(
            "check", "-r", "3", "-d", TOMORROW, "-f", "10:00", "-t", "11:30",
            "--no-input",
        )
        self.assertEqual(code, EXIT_OK)
        self.assertIn("[СВОБОДЕН]", out)

    def test_cancel_unknown_booking(self) -> None:
        """Отмена несуществующей брони возвращает код 1."""
        self.assertEqual(self.run_cli("cancel", "404")[0], EXIT_ERROR)


if __name__ == "__main__":
    unittest.main()
