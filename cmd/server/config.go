package main

import (
	"log/slog"
	"strings"
)

type config struct {
	addr      string
	adminAddr string
	logLevel  slog.Level
}

// loadConfig reads settings from the environment through getenv.
//
//	ADDR        public listen address  (default ":8080")
//	ADMIN_ADDR  pprof listen address   (default "localhost:6060")
//	LOG_LEVEL   debug, info, warn, error (default "info")
func loadConfig(getenv func(string) string) config {
	cfg := config{
		addr:      getenv("ADDR"),
		adminAddr: getenv("ADMIN_ADDR"),
	}
	if cfg.addr == "" {
		cfg.addr = ":8080"
	}
	if cfg.adminAddr == "" {
		cfg.adminAddr = "localhost:6060"
	}
	if err := cfg.logLevel.UnmarshalText([]byte(strings.TrimSpace(getenv("LOG_LEVEL")))); err != nil {
		cfg.logLevel = slog.LevelInfo
	}
	return cfg
}
