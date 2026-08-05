package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" warn ":  slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"loud":    slog.LevelInfo,
	}

	for input, want := range cases {
		if got := ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNewWritesJSONAndHonoursLevel(t *testing.T) {
	var buffer bytes.Buffer
	log := New(Config{Level: "warn", Output: &buffer})

	log.Info("dropped")
	if buffer.Len() != 0 {
		t.Fatalf("info was written at warn level: %s", buffer.String())
	}

	log.Warn("kept", slog.String("app", "tronc"))

	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v (%s)", err, buffer.String())
	}
	if record["msg"] != "kept" || record["app"] != "tronc" {
		t.Errorf("unexpected record: %v", record)
	}
}

type countingHandler struct {
	slog.Handler
	calls *int
}

func (h countingHandler) Handle(ctx context.Context, record slog.Record) error {
	*h.calls++
	return h.Handler.Handle(ctx, record)
}

func TestWrapIsAppliedOnce(t *testing.T) {
	var buffer bytes.Buffer
	wrapped, handled := 0, 0

	log := New(Config{Output: &buffer, Wrap: func(inner slog.Handler) slog.Handler {
		wrapped++
		return countingHandler{Handler: inner, calls: &handled}
	}})

	log.Info("one")
	log.Info("two")

	if wrapped != 1 {
		t.Errorf("Wrap called %d times, want 1", wrapped)
	}
	if handled != 2 {
		t.Errorf("wrapped handler saw %d records, want 2", handled)
	}
	if buffer.Len() == 0 {
		t.Error("wrapping swallowed the underlying output")
	}
}
