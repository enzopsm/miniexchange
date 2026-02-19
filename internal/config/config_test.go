package config

import (
	"os"
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "LOG_LEVEL", "EXPIRATION_INTERVAL", "WEBHOOK_TIMEOUT",
		"VWAP_WINDOW", "READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT",
		"SHUTDOWN_TIMEOUT",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.ExpirationInterval != 1*time.Second {
		t.Errorf("ExpirationInterval = %v, want 1s", cfg.ExpirationInterval)
	}
	if cfg.WebhookTimeout != 5*time.Second {
		t.Errorf("WebhookTimeout = %v, want 5s", cfg.WebhookTimeout)
	}
	if cfg.VWAPWindow != 5*time.Minute {
		t.Errorf("VWAPWindow = %v, want 5m", cfg.VWAPWindow)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want 5s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 10*time.Second {
		t.Errorf("WriteTimeout = %v, want 10s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("EXPIRATION_INTERVAL", "500ms")
	t.Setenv("WEBHOOK_TIMEOUT", "3s")
	t.Setenv("VWAP_WINDOW", "10m")
	t.Setenv("READ_TIMEOUT", "2s")
	t.Setenv("WRITE_TIMEOUT", "5s")
	t.Setenv("IDLE_TIMEOUT", "30s")
	t.Setenv("SHUTDOWN_TIMEOUT", "15s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.ExpirationInterval != 500*time.Millisecond {
		t.Errorf("ExpirationInterval = %v, want 500ms", cfg.ExpirationInterval)
	}
	if cfg.WebhookTimeout != 3*time.Second {
		t.Errorf("WebhookTimeout = %v, want 3s", cfg.WebhookTimeout)
	}
	if cfg.VWAPWindow != 10*time.Minute {
		t.Errorf("VWAPWindow = %v, want 10m", cfg.VWAPWindow)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid PORT")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL")
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	clearEnv(t)

	keys := []string{
		"EXPIRATION_INTERVAL", "WEBHOOK_TIMEOUT", "VWAP_WINDOW",
		"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "SHUTDOWN_TIMEOUT",
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(key, "not-a-duration")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for invalid %s", key)
			}
		})
	}
}
