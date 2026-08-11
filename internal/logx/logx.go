package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

func (l Level) Enabled(min Level) bool {
	return l >= min
}

type Entry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Logger struct {
	mu       sync.RWMutex
	min      Level
	slog     *slog.Logger
	buf      []Entry
	bufSize  int
	subs     map[chan Entry]Level
	nextSub  uint64
}

func New(level Level) *Logger {
	l := &Logger{
		min:     level,
		bufSize: 1000,
		subs:    make(map[chan Entry]Level),
	}
	l.slog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel(level),
	}))
	return l
}

func slogLevel(l Level) slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.min = level
	l.slog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel(level),
	}))
}

func (l *Logger) Level() Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.min
}

func (l *Logger) Debug(msg string, args ...any) { l.log(LevelDebug, msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.log(LevelInfo, msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.log(LevelWarn, msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.log(LevelError, msg, args...) }

func (l *Logger) Debugf(format string, args ...any) { l.Debug(fmt.Sprintf(format, args...)) }
func (l *Logger) Infof(format string, args ...any)  { l.Info(fmt.Sprintf(format, args...)) }
func (l *Logger) Warnf(format string, args ...any)  { l.Warn(fmt.Sprintf(format, args...)) }
func (l *Logger) Errorf(format string, args ...any) { l.Error(fmt.Sprintf(format, args...)) }

func (l *Logger) log(level Level, msg string, args ...any) {
	l.mu.RLock()
	min := l.min
	s := l.slog
	wantSub := false
	for _, subMin := range l.subs {
		if level.Enabled(subMin) {
			wantSub = true
			break
		}
	}
	l.mu.RUnlock()

	toStderr := level.Enabled(min)
	if !toStderr && !wantSub {
		return
	}
	if toStderr {
		switch level {
		case LevelDebug:
			s.Debug(msg, args...)
		case LevelWarn:
			s.Warn(msg, args...)
		case LevelError:
			s.Error(msg, args...)
		default:
			s.Info(msg, args...)
		}
	}
	formatted := msg
	if len(args) > 0 {
		formatted = fmt.Sprintf("%s %v", msg, argsToMap(args))
	}
	entry := Entry{Time: time.Now().UTC(), Level: level.String(), Message: formatted}
	l.publish(entry, level)
}

func (l *Logger) publish(entry Entry, level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, entry)
	if len(l.buf) > l.bufSize {
		l.buf = l.buf[len(l.buf)-l.bufSize:]
	}
	for ch, min := range l.subs {
		if !level.Enabled(min) {
			continue
		}
		select {
		case ch <- entry:
		default:
		}
	}
}

func argsToMap(args []any) map[string]any {
	m := make(map[string]any)
	for i := 0; i+1 < len(args); i += 2 {
		key, _ := args[i].(string)
		if key == "" {
			key = fmt.Sprintf("arg%d", i)
		}
		m[key] = args[i+1]
	}
	return m
}

// Subscribe returns a channel of log entries at or above min. Caller must Unsubscribe.
func (l *Logger) Subscribe(min Level) chan Entry {
	ch := make(chan Entry, 64)
	l.mu.Lock()
	l.subs[ch] = min
	// replay recent buffer
	for _, e := range l.buf {
		if ParseLevel(e.Level).Enabled(min) {
			select {
			case ch <- e:
			default:
			}
		}
	}
	l.mu.Unlock()
	return ch
}

func (l *Logger) Unsubscribe(ch chan Entry) {
	l.mu.Lock()
	if _, ok := l.subs[ch]; ok {
		delete(l.subs, ch)
		close(ch)
	}
	l.mu.Unlock()
}

// StdlogAdapter adapts to the classic log.Logger Println/Printf style used by localproxy.
type StdlogAdapter struct {
	L     *Logger
	Level Level
}

func (a StdlogAdapter) Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch a.Level {
	case LevelDebug:
		a.L.Debug(msg)
	case LevelWarn:
		a.L.Warn(msg)
	case LevelError:
		a.L.Error(msg)
	default:
		a.L.Info(msg)
	}
}

func (a StdlogAdapter) Println(args ...any) {
	a.Printf("%s", fmt.Sprint(args...))
}

func (a StdlogAdapter) Writer() io.Writer { return Writer{L: a.L, Level: a.Level} }

// Writer implements io.Writer for use with log.New.
type Writer struct {
	L     *Logger
	Level Level
}

func (w Writer) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg == "" {
		return len(p), nil
	}
	switch w.Level {
	case LevelDebug:
		w.L.Debug(msg)
	case LevelWarn:
		w.L.Warn(msg)
	case LevelError:
		w.L.Error(msg)
	default:
		w.L.Info(msg)
	}
	return len(p), nil
}

// Context helpers for optional logger injection later.
type ctxKey struct{}

func WithContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func FromContext(ctx context.Context) *Logger {
	if v, ok := ctx.Value(ctxKey{}).(*Logger); ok && v != nil {
		return v
	}
	return New(LevelInfo)
}
