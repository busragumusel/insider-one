package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level string) (*slog.Logger, error) {
	return NewWithWriter(level, os.Stdout)
}

func NewWithWriter(level string, writer io.Writer) (*slog.Logger, error) {
	handlerLevel, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: handlerLevel, AddSource: true})
	return slog.New(handler).With("service", "insider-one-notifications"), nil
}

func ParseLevel(level string) (slog.Leveler, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "off", "none", "disabled":
		return slog.Level(100), nil
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}
}
