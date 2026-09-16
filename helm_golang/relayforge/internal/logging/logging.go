// Package logging mirrors relayforge/logging.py: single-line JSON logs to
// stdout, with a deny-list that scrubs prohibited fields (payload, signature,
// secret, token, password) from extra context.
package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"
)

var forbidden = map[string]bool{
	"payload":   true,
	"signature": true,
	"secret":    true,
	"token":     true,
	"password":  true,
}

// Setup installs a JSON handler on slog outputting to stdout at Info level.
func Setup() {
	h := NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
}

// NewJSONHandler returns a slog.Handler emitting one JSON object per record:
// {"ts":..., "level":..., "logger":..., "msg":..., <extra attrs>} with the
// forbidden-field scrub.
func NewJSONHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	if opts.Level == nil {
		opts.Level = slog.LevelInfo
	}
	return &jsonHandler{o: out, opts: opts}
}

type jsonHandler struct {
	o    io.Writer
	opts *slog.HandlerOptions
}

func (h *jsonHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.opts.Level.Level() }
func (h *jsonHandler) WithAttrs(_ []slog.Attr) slog.Handler    { return h }
func (h *jsonHandler) WithGroup(_ string) slog.Handler         { return h }
func (h *jsonHandler) Handle(_ context.Context, r slog.Record) error {
	rec := map[string]interface{}{
		"ts":     r.Time.UTC().Format(time.RFC3339Nano),
		"level":  levelName(r.Level),
		"logger": "relayforge",
	}
	extra := map[string]interface{}{}
	r.Attrs(func(a slog.Attr) bool {
		extra[a.Key] = a.Value.Any()
		return true
	})
	rec["msg"] = r.Message
	if len(extra) > 0 {
		for k := range forbidden {
			delete(extra, k)
		}
		for k, v := range extra {
			rec[k] = v
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		slog.Default().Error("log marshal error", "err", err)
		return nil
	}
	_, err = h.o.Write(append(b, '\n'))
	return err
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}