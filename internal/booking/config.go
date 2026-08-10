package booking

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Значения настроек по умолчанию.
const (
	DefaultDBPath    = "office.db"
	DefaultRoomCount = 5
	DefaultDayStart  = "08:00"
	DefaultDayEnd    = "22:00"
)

// SMTPConfig — параметры подключения к почтовому серверу.
type SMTPConfig struct {
	Host       string
	Port       int
	User       string
	Password   string
	Sender     string
	SenderName string
	UseTLS     bool
}

// IsConfigured сообщает, задан ли SMTP-сервер.
func (c SMTPConfig) IsConfigured() bool { return c.Host != "" }

// Config — полная конфигурация приложения.
type Config struct {
	DBPath    string
	RoomCount int
	DayStart  string
	DayEnd    string
	SMTP      SMTPConfig
}

// LoadDotEnv загружает переменные из файла .env в окружение процесса.
//
// Уже установленные переменные не перезаписываются, отсутствие файла
// ошибкой не считается. Отдельная зависимость для этого не нужна.
func LoadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

// env возвращает переменную окружения или значение по умолчанию.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// envInt возвращает целочисленную переменную окружения.
func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

// LoadConfig собирает конфигурацию из .env и переменных окружения.
//
// Явно переданный dbPath имеет приоритет над окружением.
func LoadConfig(dbPath string) Config {
	LoadDotEnv(".env")
	path := dbPath
	if path == "" {
		path = env("BOOKING_DB", DefaultDBPath)
	}
	useTLS := env("SMTP_USE_TLS", "1")
	return Config{
		DBPath:    path,
		RoomCount: envInt("BOOKING_ROOMS", DefaultRoomCount),
		DayStart:  env("BOOKING_DAY_START", DefaultDayStart),
		DayEnd:    env("BOOKING_DAY_END", DefaultDayEnd),
		SMTP: SMTPConfig{
			Host:       os.Getenv("SMTP_HOST"),
			Port:       envInt("SMTP_PORT", 587),
			User:       os.Getenv("SMTP_USER"),
			Password:   os.Getenv("SMTP_PASSWORD"),
			Sender:     env("SMTP_SENDER", "noreply@office.local"),
			SenderName: env("SMTP_SENDER_NAME", "Бронирование кабинетов"),
			UseTLS:     useTLS != "0" && useTLS != "false",
		},
	}
}
