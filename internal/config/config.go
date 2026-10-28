package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL      string
	HTTPAddress      string
	Environment      string
	TraceSampleRatio float64
}

func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, fmt.Errorf("load .env file: %w", err)
	}

	cfg := Config{
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		HTTPAddress: strings.TrimSpace(os.Getenv("HTTP_ADDR")),
		Environment: strings.TrimSpace(os.Getenv("APP_ENV")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL must be set")
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = ":8080"
	}
	if cfg.Environment == "" {
		cfg.Environment = "development"
	}
	if cfg.Environment != "development" && cfg.Environment != "production" {
		return Config{}, errors.New("APP_ENV must be development or production")
	}
	cfg.TraceSampleRatio = 1
	if cfg.Environment == "production" {
		cfg.TraceSampleRatio = 0.1
	}
	if value := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); value != "" {
		sampleRatio, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(sampleRatio) || sampleRatio < 0 || sampleRatio > 1 {
			return Config{}, errors.New("OTEL_TRACES_SAMPLER_ARG must be a number between 0 and 1")
		}
		cfg.TraceSampleRatio = sampleRatio
	}
	return cfg, nil
}

func loadDotEnv(path string) error {
	err := godotenv.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
