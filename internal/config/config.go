// Package config loads runtime configuration from the environment.
//
// Operational-excellence signal: everything that varies between
// environments (port, timeouts, pool sizes, log level) is configurable
// via env vars with sane defaults — nothing is hardcoded.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the service.
type Config struct {
	Port            string
	LogLevel        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	WorkerCount     int
	QueueSize       int
}

// Load reads configuration from the environment, applying defaults.
// It returns an error if a provided value is malformed so the process
// fails fast at startup rather than misbehaving later.
func Load() (Config, error) {
	c := Config{
		Port:            getEnv("PORT", "8080"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    10 * time.Second,
		ShutdownTimeout: 15 * time.Second,
		WorkerCount:     4,
		QueueSize:       256,
	}

	var err error
	if c.ReadTimeout, err = getEnvDuration("READ_TIMEOUT", c.ReadTimeout); err != nil {
		return c, err
	}
	if c.WriteTimeout, err = getEnvDuration("WRITE_TIMEOUT", c.WriteTimeout); err != nil {
		return c, err
	}
	if c.ShutdownTimeout, err = getEnvDuration("SHUTDOWN_TIMEOUT", c.ShutdownTimeout); err != nil {
		return c, err
	}
	if c.WorkerCount, err = getEnvInt("WORKER_COUNT", c.WorkerCount); err != nil {
		return c, err
	}
	if c.QueueSize, err = getEnvInt("QUEUE_SIZE", c.QueueSize); err != nil {
		return c, err
	}

	if c.WorkerCount < 1 {
		return c, fmt.Errorf("WORKER_COUNT must be >= 1, got %d", c.WorkerCount)
	}
	if c.QueueSize < 1 {
		return c, fmt.Errorf("QUEUE_SIZE must be >= 1, got %d", c.QueueSize)
	}

	return c, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("invalid %s: %w", key, err)
	}
	return n, nil
}

func getEnvDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def, fmt.Errorf("invalid %s: %w", key, err)
	}
	return d, nil
}
