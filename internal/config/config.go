package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the mini exchange.
type Config struct {
	Port               int
	LogLevel           string
	ExpirationInterval time.Duration
	WebhookTimeout     time.Duration
	VWAPWindow         time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
}

// Load reads configuration from environment variables, applies defaults,
// and validates values. It returns an error for any invalid value.
func Load() (*Config, error) {
	port, err := getInt("PORT", 8080)
	if err != nil {
		return nil, fmt.Errorf("invalid PORT: %w", err)
	}

	logLevel := getStr("LOG_LEVEL", "info")
	if !isValidLogLevel(logLevel) {
		return nil, fmt.Errorf("invalid LOG_LEVEL: %q, must be one of: debug, info, warn, error", logLevel)
	}

	expirationInterval, err := getDuration("EXPIRATION_INTERVAL", 1*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid EXPIRATION_INTERVAL: %w", err)
	}

	webhookTimeout, err := getDuration("WEBHOOK_TIMEOUT", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid WEBHOOK_TIMEOUT: %w", err)
	}

	vwapWindow, err := getDuration("VWAP_WINDOW", 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid VWAP_WINDOW: %w", err)
	}

	readTimeout, err := getDuration("READ_TIMEOUT", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid READ_TIMEOUT: %w", err)
	}

	writeTimeout, err := getDuration("WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid WRITE_TIMEOUT: %w", err)
	}

	idleTimeout, err := getDuration("IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid IDLE_TIMEOUT: %w", err)
	}

	shutdownTimeout, err := getDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid SHUTDOWN_TIMEOUT: %w", err)
	}

	return &Config{
		Port:               port,
		LogLevel:           logLevel,
		ExpirationInterval: expirationInterval,
		WebhookTimeout:     webhookTimeout,
		VWAPWindow:         vwapWindow,
		ReadTimeout:        readTimeout,
		WriteTimeout:       writeTimeout,
		IdleTimeout:        idleTimeout,
		ShutdownTimeout:    shutdownTimeout,
	}, nil
}

func getStr(key, defaultVal string) string {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	return v
}

func getInt(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	return strconv.Atoi(v)
}

func getDuration(key string, defaultVal time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	return time.ParseDuration(v)
}

func isValidLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}
