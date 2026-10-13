package main

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want config
	}{
		{
			name: "defaults",
			env:  map[string]string{},
			want: config{addr: ":8080", adminAddr: "localhost:6060", logLevel: slog.LevelInfo},
		},
		{
			name: "overrides",
			env:  map[string]string{"ADDR": ":9000", "ADMIN_ADDR": ":7000", "LOG_LEVEL": "debug"},
			want: config{addr: ":9000", adminAddr: ":7000", logLevel: slog.LevelDebug},
		},
		{
			name: "invalid log level falls back to info",
			env:  map[string]string{"LOG_LEVEL": "loud"},
			want: config{addr: ":8080", adminAddr: "localhost:6060", logLevel: slog.LevelInfo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadConfig(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Errorf("loadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPprofMux(t *testing.T) {
	if pprofMux() == nil {
		t.Fatal("pprofMux returned nil")
	}
}

func TestRunShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config{addr: "127.0.0.1:0", adminAddr: "127.0.0.1:0"}, slog.New(slog.DiscardHandler))
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestRunFailsOnBadAddress(t *testing.T) {
	err := run(context.Background(), config{addr: "bad-address", adminAddr: "127.0.0.1:0"}, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("run returned nil, want listen error")
	}
}
