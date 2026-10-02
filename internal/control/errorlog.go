package control

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxEntries = 50

// Entry is one line in the UI's error pane. Repeats of the same message
// collapse into one entry with a count.
type Entry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
	Count   int       `json:"count"`
}

// ErrorLog keeps the recent warnings and errors shown in the UI.
type ErrorLog struct {
	mu      sync.Mutex
	entries []Entry
}

// Add records a message, folding it into the latest entry if it repeats.
func (l *ErrorLog) Add(t time.Time, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.entries); n > 0 && l.entries[n-1].Message == msg {
		l.entries[n-1].Time = t
		l.entries[n-1].Count++
		return
	}
	l.entries = append(l.entries, Entry{Time: t, Message: msg, Count: 1})
	if len(l.entries) > maxEntries {
		l.entries = slices.Delete(l.entries, 0, len(l.entries)-maxEntries)
	}
}

// Entries returns the recorded entries, newest first.
func (l *ErrorLog) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := slices.Clone(l.entries)
	slices.Reverse(out)
	return out
}

// Clear empties the log.
func (l *ErrorLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
}

// Handler wraps next so every warning or error logged anywhere in
// GameState also lands in the error pane.
func (l *ErrorLog) Handler(next slog.Handler) slog.Handler {
	return &captureHandler{next: next, log: l}
}

type captureHandler struct {
	next  slog.Handler
	log   *ErrorLog
	attrs []slog.Attr
}

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn || h.next.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.log.Add(r.Time, h.format(r))
	}
	if h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{next: h.next.WithAttrs(attrs), log: h.log, attrs: append(slices.Clip(h.attrs), attrs...)}
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{next: h.next.WithGroup(name), log: h.log, attrs: h.attrs}
}

// format renders a record for people: the message, then its error, then any
// other details in brackets. The game attribute is dropped because the pane
// sits under the game picker.
func (h *captureHandler) format(r slog.Record) string {
	var errText string
	var details []string
	add := func(a slog.Attr) bool {
		switch a.Key {
		case "err":
			errText = a.Value.String()
		case "game":
		default:
			details = append(details, fmt.Sprintf("%s=%s", a.Key, a.Value))
		}
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)

	msg := r.Message
	if errText != "" {
		msg += ": " + errText
	}
	if len(details) > 0 {
		msg += " (" + strings.Join(details, ", ") + ")"
	}
	return msg
}
