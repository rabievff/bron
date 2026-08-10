package booking

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tomorrow — дата, используемая в тестах команд.
var tomorrow = time.Now().AddDate(0, 0, 1).Format("2006-01-02")

// cliRunner запускает команды на временной базе.
type cliRunner struct {
	t      *testing.T
	dbPath string
	stdin  string
	tty    bool
}

func newCLI(t *testing.T) *cliRunner {
	t.Helper()
	return &cliRunner{t: t, dbPath: filepath.Join(t.TempDir(), "cli.db")}
}

// run выполняет команду и возвращает код возврата и весь вывод.
func (r *cliRunner) run(args ...string) (int, string) {
	r.t.Helper()
	var out bytes.Buffer
	app := &App{
		Stdout:      &out,
		Stderr:      &out,
		Stdin:       strings.NewReader(r.stdin),
		Interactive: r.tty,
	}
	code := app.Run(append([]string{"--db", r.dbPath}, args...))
	return code, out.String()
}

// book создаёт бронь кабинета №3 на завтра с 10:00 до 11:30.
func (r *cliRunner) book() {
	r.t.Helper()
	code, out := r.run("book", "-r", "3", "-d", tomorrow, "-f", "10:00",
		"-t", "11:30", "-n", "Иван Петров", "-e", "ivan@example.com",
		"-p", "Планёрка")
	if code != ExitOK {
		r.t.Fatalf("book вернул %d: %s", code, out)
	}
}

func TestCLIInitShowsFiveRooms(t *testing.T) {
	code, out := newCLI(t).run("init")
	if code != ExitOK {
		t.Fatalf("код = %d: %s", code, out)
	}
	if !strings.Contains(out, "Кабинетов в справочнике: 5") {
		t.Errorf("вывод не содержит числа кабинетов:\n%s", out)
	}
}

func TestCLICheckAllRoomsFree(t *testing.T) {
	code, out := newCLI(t).run("check", "-d", tomorrow,
		"-f", "10:00", "-t", "11:00", "-no-input")
	if code != ExitOK {
		t.Fatalf("код = %d: %s", code, out)
	}
	if got := strings.Count(out, "[СВОБОДЕН]"); got != 5 {
		t.Errorf("свободных кабинетов = %d, ожидалось 5:\n%s", got, out)
	}
}

func TestCLICheckShowsOwnerAndTime(t *testing.T) {
	cli := newCLI(t)
	cli.book()

	code, out := cli.run("check", "-r", "3", "-d", tomorrow,
		"-f", "11:00", "-t", "12:00", "-no-input")
	if code != ExitBusy {
		t.Errorf("код = %d, ожидался %d", code, ExitBusy)
	}
	for _, want := range []string{"[ЗАНЯТ]", "занято до 11:30",
		"Иван Петров", "ivan@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе нет %q:\n%s", want, out)
		}
	}
}

func TestCLIBookConflictReturnsBusy(t *testing.T) {
	cli := newCLI(t)
	cli.book()

	code, _ := cli.run("book", "-r", "3", "-d", tomorrow, "-f", "11:00",
		"-t", "12:00", "-n", "Мария", "-e", "maria@example.com")
	if code != ExitBusy {
		t.Errorf("код = %d, ожидался %d", code, ExitBusy)
	}
}

func TestCLIBookUnknownRoom(t *testing.T) {
	code, _ := newCLI(t).run("book", "-r", "99", "-d", tomorrow,
		"-f", "10:00", "-t", "11:00", "-n", "Иван", "-e", "ivan@example.com")
	if code != ExitError {
		t.Errorf("код = %d, ожидался %d", code, ExitError)
	}
}

func TestCLIBookNotifyPrintsLetter(t *testing.T) {
	code, out := newCLI(t).run("book", "-r", "1", "-d", tomorrow,
		"-f", "10:00", "-t", "11:00", "-n", "Иван", "-e", "ivan@example.com",
		"-notify", "-dry-run")
	if code != ExitOK {
		t.Fatalf("код = %d: %s", code, out)
	}
	for _, want := range []string{"Кабинет:      №1", "10:00-11:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("в письме нет %q:\n%s", want, out)
		}
	}
}

func TestCLIInteractiveBooking(t *testing.T) {
	cli := newCLI(t)
	cli.tty = true
	cli.stdin = "2\nМария Сидорова\nmaria@example.com\nРетро\n"

	code, out := cli.run("check", "-d", tomorrow, "-f", "14:00", "-t", "15:00")
	if code != ExitOK {
		t.Fatalf("код = %d: %s", code, out)
	}
	if !strings.Contains(out, "Кабинет забронирован") {
		t.Fatalf("бронь не создана:\n%s", out)
	}

	_, listing := cli.run("list", "-d", tomorrow)
	if !strings.Contains(listing, "Мария Сидорова") ||
		!strings.Contains(listing, "каб. №2") {
		t.Errorf("брони нет в расписании:\n%s", listing)
	}
}

func TestCLIInteractiveDeclined(t *testing.T) {
	cli := newCLI(t)
	cli.tty = true
	cli.stdin = "\n"

	code, _ := cli.run("check", "-d", tomorrow, "-f", "14:00", "-t", "15:00")
	if code != ExitOK {
		t.Errorf("код = %d, ожидался %d", code, ExitOK)
	}
	_, listing := cli.run("list", "-d", tomorrow)
	if !strings.Contains(listing, "Броней не найдено") {
		t.Errorf("бронь не должна была создаться:\n%s", listing)
	}
}

func TestCLIInteractiveBusyRoomRejected(t *testing.T) {
	cli := newCLI(t)
	cli.book()
	cli.tty = true
	cli.stdin = "3\n"

	code, out := cli.run("check", "-d", tomorrow, "-f", "10:00", "-t", "11:00")
	if code != ExitError {
		t.Errorf("код = %d, ожидался %d", code, ExitError)
	}
	if !strings.Contains(out, "бронирование отменено") {
		t.Errorf("нет сообщения об отмене:\n%s", out)
	}
}

func TestCLICancelFreesRoom(t *testing.T) {
	cli := newCLI(t)
	cli.book()

	if code, out := cli.run("cancel", "1"); code != ExitOK {
		t.Fatalf("cancel вернул %d: %s", code, out)
	}
	code, out := cli.run("check", "-r", "3", "-d", tomorrow,
		"-f", "10:00", "-t", "11:30", "-no-input")
	if code != ExitOK || !strings.Contains(out, "[СВОБОДЕН]") {
		t.Errorf("кабинет должен освободиться, код=%d:\n%s", code, out)
	}
}

func TestCLICancelUnknown(t *testing.T) {
	if code, _ := newCLI(t).run("cancel", "404"); code != ExitError {
		t.Errorf("код = %d, ожидался %d", code, ExitError)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	code, out := newCLI(t).run("привет")
	if code != ExitError {
		t.Errorf("код = %d, ожидался %d", code, ExitError)
	}
	if !strings.Contains(out, "Неизвестная команда") {
		t.Errorf("нет сообщения о неизвестной команде:\n%s", out)
	}
}
