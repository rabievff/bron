// Команда booking — бронирование переговорных кабинетов офиса.
package main

import (
	"os"

	"github.com/rabievff/bron/internal/booking"
)

func main() {
	os.Exit(booking.Run(os.Args[1:]))
}
