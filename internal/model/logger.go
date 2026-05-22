package model

import (
	"context"
	"fmt"
	"log/slog"
)

type Logger struct {
	log 		*slog.Logger
}

func NewLogger(log *slog.Logger) *Logger {
	return &Logger{
		log: log,
	}
}

func (l *Logger) AddAttr(attr slog.Attr) *Logger {
	return &Logger{log: l.log.With(attr)}
}

type defaultLoggerKey uint8
const DefLogKey = defaultLoggerKey(0)

func Replacer(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.String("time", a.Value.Time().Format("15:04:05 02-01"))
	}
	return a
}

func (l *Logger) logf(level slog.Level, format string, args ...any) {
	if !l.log.Enabled(context.Background(), level) {
		return
	}
	l.log.Log(context.Background(), level, fmt.Sprintf(format, args...))
}

func (l *Logger) Infof(format string, args ...any) {
	l.logf(slog.LevelInfo, format, args...)
}

func (l *Logger) Debugf(format string, args ...any) {
	l.logf(slog.LevelDebug, format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.logf(slog.LevelError, format, args...)
}