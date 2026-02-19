package config

import (
	"fmt"
	"os"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 25: Configuration parsing
// Validates: Requirements 16.1, 16.2

// validLogLevels are the accepted log level values.
var validLogLevels = []string{"debug", "info", "warn", "error"}

// durationEnvKeys lists all Config fields that are parsed as time.Duration.
var durationEnvKeys = []string{
	"EXPIRATION_INTERVAL",
	"WEBHOOK_TIMEOUT",
	"VWAP_WINDOW",
	"READ_TIMEOUT",
	"WRITE_TIMEOUT",
	"IDLE_TIMEOUT",
	"SHUTDOWN_TIMEOUT",
}

// allEnvKeys is every config-related env var key.
var allEnvKeys = append([]string{"PORT", "LOG_LEVEL"}, durationEnvKeys...)

// unsetAllConfigEnv clears all config env vars.
func unsetAllConfigEnv() {
	for _, key := range allEnvKeys {
		os.Unsetenv(key)
	}
}

// genDurationString generates a valid Go duration string (e.g. "3s", "500ms", "2m").
func genDurationString() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		unit := rapid.SampledFrom([]string{"ms", "s", "m"}).Draw(t, "unit")
		val := rapid.IntRange(1, 600).Draw(t, "val")
		return fmt.Sprintf("%d%s", val, unit)
	})
}

// parseDurationOrDefault parses a duration string, returning the default if empty.
func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, _ := time.ParseDuration(s)
	return d
}

func TestProperty_ValidConfigParsing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		unsetAllConfigEnv()
		defer unsetAllConfigEnv()

		// Generate optional valid values for each field.
		// Empty string means "use default" (env var not set).
		portStr := rapid.OneOf(
			rapid.Just(""),
			rapid.Map(rapid.IntRange(1, 65535), func(v int) string { return fmt.Sprintf("%d", v) }),
		).Draw(t, "port")

		logLevel := rapid.OneOf(
			rapid.Just(""),
			rapid.SampledFrom(validLogLevels),
		).Draw(t, "logLevel")

		durStrs := make(map[string]string, len(durationEnvKeys))
		for _, key := range durationEnvKeys {
			durStrs[key] = rapid.OneOf(
				rapid.Just(""),
				genDurationString(),
			).Draw(t, key)
		}

		// Set env vars for non-empty values.
		if portStr != "" {
			os.Setenv("PORT", portStr)
		}
		if logLevel != "" {
			os.Setenv("LOG_LEVEL", logLevel)
		}
		for _, key := range durationEnvKeys {
			if durStrs[key] != "" {
				os.Setenv(key, durStrs[key])
			}
		}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error for valid inputs: %v", err)
		}

		// Verify Port
		expectedPort := 8080
		if portStr != "" {
			fmt.Sscanf(portStr, "%d", &expectedPort)
		}
		if cfg.Port != expectedPort {
			t.Fatalf("Port = %d, want %d", cfg.Port, expectedPort)
		}

		// Verify LogLevel
		expectedLogLevel := "info"
		if logLevel != "" {
			expectedLogLevel = logLevel
		}
		if cfg.LogLevel != expectedLogLevel {
			t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, expectedLogLevel)
		}

		// Verify duration fields
		type durField struct {
			envKey string
			got    time.Duration
			defVal time.Duration
		}
		durFields := []durField{
			{"EXPIRATION_INTERVAL", cfg.ExpirationInterval, 1 * time.Second},
			{"WEBHOOK_TIMEOUT", cfg.WebhookTimeout, 5 * time.Second},
			{"VWAP_WINDOW", cfg.VWAPWindow, 5 * time.Minute},
			{"READ_TIMEOUT", cfg.ReadTimeout, 5 * time.Second},
			{"WRITE_TIMEOUT", cfg.WriteTimeout, 10 * time.Second},
			{"IDLE_TIMEOUT", cfg.IdleTimeout, 60 * time.Second},
			{"SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout, 10 * time.Second},
		}
		for _, df := range durFields {
			expected := parseDurationOrDefault(durStrs[df.envKey], df.defVal)
			if df.got != expected {
				t.Fatalf("%s = %v, want %v (env=%q)", df.envKey, df.got, expected, durStrs[df.envKey])
			}
		}
	})
}

func TestProperty_InvalidPortReturnsError(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		unsetAllConfigEnv()
		defer unsetAllConfigEnv()

		// Generate strings that are not valid integers.
		invalidPort := rapid.OneOf(
			rapid.StringMatching(`[a-zA-Z]{1,10}`),
			rapid.Just("12.5"),
			rapid.Just("1.0e2"),
		).Filter(func(s string) bool {
			if s == "" {
				return false
			}
			_, err := fmt.Sscanf(s, "%d", new(int))
			return err != nil
		}).Draw(t, "invalidPort")

		os.Setenv("PORT", invalidPort)

		_, err := Load()
		if err == nil {
			t.Fatalf("Load() should return error for invalid PORT %q", invalidPort)
		}
	})
}

func TestProperty_InvalidLogLevelReturnsError(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		unsetAllConfigEnv()
		defer unsetAllConfigEnv()

		// Generate strings that are not valid log levels.
		invalidLevel := rapid.StringMatching(`[a-z]{1,20}`).Filter(func(s string) bool {
			for _, v := range validLogLevels {
				if s == v {
					return false
				}
			}
			return s != ""
		}).Draw(t, "invalidLevel")

		os.Setenv("LOG_LEVEL", invalidLevel)

		_, err := Load()
		if err == nil {
			t.Fatalf("Load() should return error for invalid LOG_LEVEL %q", invalidLevel)
		}
	})
}

func TestProperty_InvalidDurationReturnsError(t *testing.T) {
	for _, key := range durationEnvKeys {
		t.Run(key, func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				unsetAllConfigEnv()
				defer unsetAllConfigEnv()

				// Generate strings that are not valid Go durations.
				invalidDur := rapid.OneOf(
					rapid.StringMatching(`[a-zA-Z]{2,10}`),
					rapid.Just("notaduration"),
					rapid.Just("5x"),
					rapid.Just("abc123"),
				).Filter(func(s string) bool {
					if s == "" {
						return false
					}
					_, err := time.ParseDuration(s)
					return err != nil
				}).Draw(t, "invalidDuration")

				os.Setenv(key, invalidDur)

				_, err := Load()
				if err == nil {
					t.Fatalf("Load() should return error for invalid %s=%q", key, invalidDur)
				}
			})
		})
	}
}
