package booking

import (
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
)

// Message — письмо, готовое к отправке.
type Message struct {
	To      string
	Subject string
	Body    string
	from    string
}

// bodyTemplate — текст уведомления о брони.
const bodyTemplate = `Здравствуйте, %s!

Кабинет забронирован за вами.

    Кабинет:      №%d — %s
    Дата:         %s
    Время:        %s (%d мин.)
    Забронировал: %s <%s>%s

Номер брони: %d
Чтобы отменить бронь, выполните: booking cancel %d

Это письмо сформировано автоматически, отвечать на него не нужно.
`

// BuildMessage формирует письмо с датой, временем и номером кабинета.
func BuildMessage(b Booking, room Room, config SMTPConfig) Message {
	purposeLine := ""
	if b.Purpose != "" {
		purposeLine = "\n    Тема:         " + b.Purpose
	}
	date := b.Slot.Start.Format("02.01.2006")
	return Message{
		To: b.PersonEmail,
		Subject: fmt.Sprintf("Бронь кабинета №%d на %s %s",
			room.ID, date, b.Slot.HumanTime()),
		Body: fmt.Sprintf(bodyTemplate, b.PersonName, room.ID, room.Name,
			date, b.Slot.HumanTime(), b.Slot.Minutes(),
			b.PersonName, b.PersonEmail, purposeLine, b.ID, b.ID),
		from: fmt.Sprintf("%s <%s>",
			mime.QEncoding.Encode("utf-8", config.SenderName), config.Sender),
	}
}

// Bytes возвращает письмо в виде RFC 5322 с заголовками в UTF-8.
func (m Message) Bytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", m.from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n",
		mime.QEncoding.Encode("utf-8", m.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return []byte(b.String())
}

// Notifier отправляет уведомления.
type Notifier interface {
	Send(Message) (string, error)
}

// NewNotifier выбирает отправителя в зависимости от конфигурации.
//
// Пока SMTP не настроен, письма печатаются в out: утилита остаётся
// полностью работоспособной без почтового сервера. Поток передаётся
// параметром, а не берётся из os.Stdout, иначе вывод нельзя было бы
// перенаправить — ни в тестах, ни при запуске с другими потоками.
func NewNotifier(config SMTPConfig, dryRun bool, out io.Writer) Notifier {
	if out == nil {
		out = os.Stdout
	}
	if dryRun || !config.IsConfigured() {
		return ConsoleNotifier{Out: out}
	}
	return SMTPNotifier{config: config}
}

// ConsoleNotifier печатает письмо вместо отправки.
type ConsoleNotifier struct {
	Out io.Writer
}

// Send выводит письмо в заданный поток.
func (n ConsoleNotifier) Send(m Message) (string, error) {
	line := strings.Repeat("-", 62)
	fmt.Fprintf(n.Out, "%s\nКому:  %s\nТема:  %s\n%s\n%s%s\n",
		line, m.To, m.Subject, line, m.Body, line)
	return fmt.Sprintf(
		"письмо выведено в консоль (SMTP не настроен), адресат: %s", m.To), nil
}

// SMTPNotifier отправляет письмо через реальный почтовый сервер.
type SMTPNotifier struct {
	config SMTPConfig
}

// Send отправляет письмо, оборачивая ошибки транспорта в ErrNotification.
func (n SMTPNotifier) Send(m Message) (string, error) {
	if err := n.send(m); err != nil {
		return "", fmt.Errorf("%w: письмо на %s: %v", ErrNotification, m.To, err)
	}
	return fmt.Sprintf("письмо отправлено на %s", m.To), nil
}

// send выполняет сеанс SMTP: порт 465 — сразу TLS, иначе STARTTLS.
func (n SMTPNotifier) send(m Message) error {
	addr := net.JoinHostPort(n.config.Host, fmt.Sprint(n.config.Port))

	var client *smtp.Client
	var err error
	if n.config.Port == 465 {
		conn, dialErr := tls.Dial("tcp", addr,
			&tls.Config{ServerName: n.config.Host})
		if dialErr != nil {
			return dialErr
		}
		if client, err = smtp.NewClient(conn, n.config.Host); err != nil {
			return err
		}
	} else {
		if client, err = smtp.Dial(addr); err != nil {
			return err
		}
		if n.config.UseTLS {
			if err = client.StartTLS(
				&tls.Config{ServerName: n.config.Host}); err != nil {
				return err
			}
		}
	}
	defer client.Quit()

	if n.config.User != "" {
		auth := smtp.PlainAuth("", n.config.User, n.config.Password, n.config.Host)
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(n.config.Sender); err != nil {
		return err
	}
	if err = client.Rcpt(m.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(m.Bytes()); err != nil {
		return err
	}
	return writer.Close()
}
