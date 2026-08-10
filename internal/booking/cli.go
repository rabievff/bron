package booking

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Коды возврата программы.
const (
	// ExitOK — успешное выполнение.
	ExitOK = 0
	// ExitError — ошибка ввода или доменная ошибка.
	ExitError = 1
	// ExitBusy — кабинет занят; удобно для проверок в скриптах.
	ExitBusy = 2
)

const sep = "--------------------------------------------------------------"

const usageText = `Бронирование переговорных кабинетов офиса.

Использование:
  booking [--db ПУТЬ] КОМАНДА [флаги]

Команды:
  init     создать базу данных и справочник из 5 кабинетов
  rooms    показать список кабинетов
  check    проверить занятость и предложить бронирование
  book     забронировать кабинет
  notify   отправить уведомление о брони на e-mail
  list     показать расписание броней
  cancel   отменить бронь

Примеры:
  booking init
  booking check -d завтра -f 10:00 -t 11:30
  booking book -r 3 -d завтра -f 10:00 -t 11:30 -n "Иван Петров" -e ivan@example.com
  booking notify 1

Справка по команде: booking КОМАНДА -h
`

// App связывает потоки ввода-вывода с логикой команд.
// Вынесение потоков в поля делает команды тестируемыми.
type App struct {
	Stdout      io.Writer
	Stderr      io.Writer
	Stdin       io.Reader
	Interactive bool
}

// Run разбирает аргументы и выполняет команду, используя потоки ОС.
func Run(args []string) int {
	app := &App{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Stdin:       os.Stdin,
		Interactive: isTerminal(os.Stdin),
	}
	return app.Run(args)
}

// isTerminal сообщает, подключён ли ввод к терминалу.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Run выполняет команду и возвращает код возврата.
func (a *App) Run(args []string) int {
	root := flag.NewFlagSet("booking", flag.ContinueOnError)
	root.SetOutput(a.Stderr)
	root.Usage = func() { fmt.Fprint(a.Stderr, usageText) }
	dbPath := root.String("db", "",
		"путь к файлу базы данных (по умолчанию office.db)")
	if err := root.Parse(args); err != nil {
		return ExitError
	}

	rest := root.Args()
	if len(rest) == 0 {
		fmt.Fprint(a.Stderr, usageText)
		return ExitError
	}

	config := LoadConfig(*dbPath)
	store, err := OpenStore(config.DBPath, config.RoomCount)
	if err != nil {
		fmt.Fprintf(a.Stderr, "Ошибка базы данных: %v\n", err)
		return ExitError
	}
	defer store.Close()

	service := NewService(store, config)
	service.SetOutput(a.Stdout)
	code, err := a.dispatch(rest[0], rest[1:], service, config)
	if err != nil {
		return a.reportError(err)
	}
	return code
}

// dispatch направляет вызов в обработчик команды.
func (a *App) dispatch(command string, args []string, s *Service,
	config Config) (int, error) {

	switch command {
	case "init":
		return a.cmdInit(s, config)
	case "rooms":
		return a.cmdRooms(s)
	case "check":
		return a.cmdCheck(args, s)
	case "book":
		return a.cmdBook(args, s)
	case "notify":
		return a.cmdNotify(args, s)
	case "list":
		return a.cmdList(args, s)
	case "cancel":
		return a.cmdCancel(args, s)
	default:
		fmt.Fprintf(a.Stderr, "Неизвестная команда %q.\n\n", command)
		fmt.Fprint(a.Stderr, usageText)
		return ExitError, nil
	}
}

// reportError печатает доменную ошибку без трассировки стека.
func (a *App) reportError(err error) int {
	var busy *RoomBusyError
	if errors.As(err, &busy) {
		fmt.Fprintf(a.Stderr, "\n%s\n", busy.Error())
		return ExitBusy
	}
	fmt.Fprintf(a.Stderr, "Ошибка: %v\n", err)
	return ExitError
}

// newFlagSet создаёт набор флагов подкоманды.
func (a *App) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	return fs
}

// slotFlags добавляет общие флаги даты и времени.
func slotFlags(fs *flag.FlagSet) (date, from, to *string) {
	date = fs.String("d", "", "дата: ГГГГ-ММ-ДД, ДД.ММ.ГГГГ, «сегодня», «завтра»")
	from = fs.String("f", "", "время начала, ЧЧ:ММ")
	to = fs.String("t", "", "время окончания, ЧЧ:ММ")
	fs.StringVar(date, "date", "", "то же, что -d")
	fs.StringVar(from, "from", "", "то же, что -f")
	fs.StringVar(to, "to", "", "то же, что -t")
	return date, from, to
}

// requireSlot собирает интервал, проверяя обязательные флаги.
func requireSlot(date, from, to string) (TimeSlot, error) {
	if date == "" || from == "" || to == "" {
		return TimeSlot{}, validationErrorf(
			"нужно указать -d дату, -f начало и -t окончание")
	}
	return ParseSlot(date, from, to)
}

func (a *App) cmdInit(s *Service, config Config) (int, error) {
	rooms, err := s.Rooms()
	if err != nil {
		return ExitError, err
	}
	fmt.Fprintf(a.Stdout, "База данных готова: %s\n", config.DBPath)
	fmt.Fprintf(a.Stdout, "Кабинетов в справочнике: %d\n", len(rooms))
	a.printRooms(rooms)
	return ExitOK, nil
}

func (a *App) cmdRooms(s *Service) (int, error) {
	rooms, err := s.Rooms()
	if err != nil {
		return ExitError, err
	}
	a.printRooms(rooms)
	return ExitOK, nil
}

func (a *App) printRooms(rooms []Room) {
	for _, room := range rooms {
		fmt.Fprintf(a.Stdout, "  №%d — %s (мест: %d)\n",
			room.ID, room.Name, room.Capacity)
	}
}

func (a *App) cmdCheck(args []string, s *Service) (int, error) {
	fs := a.newFlagSet("check")
	room := fs.Int("r", 0, "номер кабинета; без него проверяются все")
	fs.IntVar(room, "room", 0, "то же, что -r")
	date, from, to := slotFlags(fs)
	noInput := fs.Bool("no-input", false, "не предлагать бронирование")
	if err := fs.Parse(args); err != nil {
		return ExitError, nil
	}

	slot, err := requireSlot(*date, *from, *to)
	if err != nil {
		return ExitError, err
	}
	results, err := s.Check(slot, *room)
	if err != nil {
		return ExitError, err
	}

	fmt.Fprintf(a.Stdout, "Проверка на %s\n%s\n", slot.Human(), sep)
	var free []Room
	for _, result := range results {
		if result.IsFree() {
			fmt.Fprintf(a.Stdout, "  [СВОБОДЕН]  %s\n", result.Room.Label())
			free = append(free, result.Room)
			continue
		}
		fmt.Fprintf(a.Stdout, "  [ЗАНЯТ]     %s — освободится в %s\n",
			result.Room.Label(), result.BusyUntil())
		for _, c := range result.Conflicts {
			purpose := ""
			if c.Purpose != "" {
				purpose = ", тема: " + c.Purpose
			}
			fmt.Fprintf(a.Stdout, "      занято до %s — %s%s [%s, бронь №%d]\n",
				c.Slot.End.Format("15:04"), c.Person(), purpose,
				c.Slot.HumanTime(), c.ID)
		}
	}
	fmt.Fprintln(a.Stdout, sep)

	if len(free) == 0 {
		fmt.Fprintln(a.Stdout, "Свободных кабинетов на это время нет.")
		return ExitBusy, nil
	}
	numbers := make([]string, 0, len(free))
	for _, room := range free {
		numbers = append(numbers, fmt.Sprintf("№%d", room.ID))
	}
	fmt.Fprintf(a.Stdout, "Свободны: %s\n", strings.Join(numbers, ", "))

	if *noInput || !a.Interactive {
		fmt.Fprintln(a.Stdout, "Забронировать: booking book --room N ...")
		return ExitOK, nil
	}
	return a.interactiveBook(s, slot, free)
}

// interactiveBook проводит диалог бронирования свободного кабинета.
func (a *App) interactiveBook(s *Service, slot TimeSlot,
	free []Room) (int, error) {

	reader := bufio.NewReader(a.Stdin)
	ask := func(prompt string) string {
		fmt.Fprint(a.Stdout, prompt)
		line, _ := reader.ReadString('\n')
		return strings.TrimSpace(line)
	}

	fmt.Fprintln(a.Stdout)
	answer := ask(fmt.Sprintf(
		"Забронировать кабинет? Номер [%d] или Enter для выхода: ", free[0].ID))
	if answer == "" {
		return ExitOK, nil
	}
	number, err := strconv.Atoi(answer)
	if err != nil || !containsRoom(free, number) {
		fmt.Fprintln(a.Stdout,
			"Такого свободного кабинета нет, бронирование отменено.")
		return ExitError, nil
	}

	name := ask("Ваше имя: ")
	email := ask("Ваш e-mail: ")
	purpose := ask("Тема встречи (можно пропустить): ")

	booking, err := s.Book(number, name, email, slot, purpose)
	if err != nil {
		return ExitError, err
	}
	room, err := s.Room(booking.RoomID)
	if err != nil {
		return ExitError, err
	}
	fmt.Fprintf(a.Stdout, "\nКабинет забронирован.\n%s\n",
		describeBooking(booking, room))
	fmt.Fprintf(a.Stdout, "\nОтправить уведомление: booking notify %d\n",
		booking.ID)
	return ExitOK, nil
}

// containsRoom сообщает, есть ли кабинет с таким номером в списке.
func containsRoom(rooms []Room, id int) bool {
	for _, room := range rooms {
		if room.ID == id {
			return true
		}
	}
	return false
}

func (a *App) cmdBook(args []string, s *Service) (int, error) {
	fs := a.newFlagSet("book")
	room := fs.Int("r", 0, "номер кабинета")
	fs.IntVar(room, "room", 0, "то же, что -r")
	date, from, to := slotFlags(fs)
	name := fs.String("n", "", "имя сотрудника")
	fs.StringVar(name, "name", "", "то же, что -n")
	email := fs.String("e", "", "e-mail сотрудника")
	fs.StringVar(email, "email", "", "то же, что -e")
	purpose := fs.String("p", "", "тема встречи")
	fs.StringVar(purpose, "purpose", "", "то же, что -p")
	notify := fs.Bool("notify", false, "сразу отправить уведомление")
	dryRun := fs.Bool("dry-run", false, "показать письмо, не отправляя")
	if err := fs.Parse(args); err != nil {
		return ExitError, nil
	}

	slot, err := requireSlot(*date, *from, *to)
	if err != nil {
		return ExitError, err
	}
	if *room == 0 {
		return ExitError, validationErrorf("нужно указать номер кабинета: -r N")
	}

	booking, err := s.Book(*room, *name, *email, slot, *purpose)
	if err != nil {
		return ExitError, err
	}
	roomInfo, err := s.Room(booking.RoomID)
	if err != nil {
		return ExitError, err
	}
	fmt.Fprintf(a.Stdout, "Кабинет забронирован.\n%s\n",
		describeBooking(booking, roomInfo))

	if *notify {
		result, err := s.Notify(booking.ID, *dryRun)
		if err != nil {
			return ExitError, err
		}
		fmt.Fprintf(a.Stdout, "\nУведомление: %s\n", result)
		return ExitOK, nil
	}
	fmt.Fprintf(a.Stdout, "\nОтправить уведомление: booking notify %d\n",
		booking.ID)
	return ExitOK, nil
}

func (a *App) cmdNotify(args []string, s *Service) (int, error) {
	fs := a.newFlagSet("notify")
	dryRun := fs.Bool("dry-run", false, "показать письмо, не отправляя")
	if err := fs.Parse(args); err != nil {
		return ExitError, nil
	}
	id, err := parseID(fs.Arg(0), "номер брони")
	if err != nil {
		return ExitError, err
	}
	result, err := s.Notify(id, *dryRun)
	if err != nil {
		return ExitError, err
	}
	fmt.Fprintf(a.Stdout, "Готово: %s\n", result)
	return ExitOK, nil
}

func (a *App) cmdList(args []string, s *Service) (int, error) {
	fs := a.newFlagSet("list")
	room := fs.Int("r", 0, "фильтр по кабинету")
	fs.IntVar(room, "room", 0, "то же, что -r")
	date := fs.String("d", "", "фильтр по дате")
	fs.StringVar(date, "date", "", "то же, что -d")
	all := fs.Bool("all", false, "включая прошедшие брони")
	if err := fs.Parse(args); err != nil {
		return ExitError, nil
	}

	onDate := ""
	if *date != "" {
		parsed, err := ParseDate(*date)
		if err != nil {
			return ExitError, err
		}
		onDate = parsed.Format(dateLayout)
	}

	bookings, err := s.Agenda(*room, onDate, *all)
	if err != nil {
		return ExitError, err
	}
	if len(bookings) == 0 {
		fmt.Fprintln(a.Stdout, "Броней не найдено.")
		return ExitOK, nil
	}

	rooms, err := s.Rooms()
	if err != nil {
		return ExitError, err
	}
	names := make(map[int]string, len(rooms))
	for _, room := range rooms {
		names[room.ID] = room.Name
	}

	currentDay := ""
	for _, b := range bookings {
		day := b.Slot.Start.Format("02.01.2006")
		if day != currentDay {
			currentDay = day
			fmt.Fprintf(a.Stdout, "\n%s\n%s\n", day, sep)
		}
		flagText := "не отправлено"
		if b.IsNotified() {
			flagText = "отправлено"
		}
		purpose := ""
		if b.Purpose != "" {
			purpose = " — " + b.Purpose
		}
		fmt.Fprintf(a.Stdout, "  №%-4d %s  каб. №%d %-8s %s%s  [увед.: %s]\n",
			b.ID, b.Slot.HumanTime(), b.RoomID, names[b.RoomID],
			b.Person(), purpose, flagText)
	}
	return ExitOK, nil
}

func (a *App) cmdCancel(args []string, s *Service) (int, error) {
	fs := a.newFlagSet("cancel")
	if err := fs.Parse(args); err != nil {
		return ExitError, nil
	}
	id, err := parseID(fs.Arg(0), "номер брони")
	if err != nil {
		return ExitError, err
	}
	booking, err := s.Cancel(id)
	if err != nil {
		return ExitError, err
	}
	room, err := s.Room(booking.RoomID)
	if err != nil {
		return ExitError, err
	}
	fmt.Fprintf(a.Stdout, "Бронь №%d отменена.\n%s\n",
		booking.ID, describeBooking(booking, room))
	return ExitOK, nil
}

// parseID разбирает обязательный числовой аргумент команды.
func parseID(raw, what string) (int, error) {
	if raw == "" {
		return 0, validationErrorf("нужно указать %s", what)
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, validationErrorf("%s должен быть числом, получено %q",
			what, raw)
	}
	return id, nil
}
