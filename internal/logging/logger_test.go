package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		want    slog.Level
		wantErr bool
	}{
		{name: "default info", level: "", want: slog.LevelInfo},
		{name: "debug", level: "debug", want: slog.LevelDebug},
		{name: "off", level: "off", want: slog.Level(100)},
		{name: "invalid", level: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.level)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Level() != tt.want {
				t.Fatalf("unexpected level: got %v want %v", got.Level(), tt.want)
			}
		})
	}
}

func TestNewEmitsJSONLogs(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewWithWriter("info", &buf)
	if err != nil {
		t.Fatal(err)
	}

	logger.Info("hello", "request_id", "req-1", "correlation_id", "corr-1")

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("expected json log output: %v", err)
	}
	if payload["msg"] != "hello" {
		t.Fatalf("unexpected message: %v", payload["msg"])
	}
	if payload["request_id"] != "req-1" {
		t.Fatalf("missing structured field request_id: %v", payload["request_id"])
	}
	if payload["correlation_id"] != "corr-1" {
		t.Fatalf("missing structured field correlation_id: %v", payload["correlation_id"])
	}
}
