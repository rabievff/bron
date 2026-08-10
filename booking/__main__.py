"""Позволяет запускать пакет как ``python -m booking``."""

import sys

from booking.cli import main

if __name__ == "__main__":
    sys.exit(main())
