package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvLoadsMissingEnvironmentValues(t *testing.T) {
	const key = "LEDGER_CONFIG_TEST_VALUE"
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Unsetenv(key); err != nil {
			t.Errorf("unset test environment variable: %v", err)
		}
	})

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "from-file" {
		t.Fatalf("%s = %q, want %q", key, got, "from-file")
	}
}

func TestLoadDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	const key = "LEDGER_CONFIG_TEST_VALUE"
	t.Setenv(key, "from-environment")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "from-environment" {
		t.Fatalf("%s = %q, want environment value %q", key, got, "from-environment")
	}
}

func TestLoadDotEnvAllowsMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("load missing .env file: %v", err)
	}
}

func TestLoadTraceSampleRatioDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		override    string
		want        float64
		wantErr     bool
	}{
		{name: "development default", environment: "development", want: 1},
		{name: "production default", environment: "production", want: 0.1},
		{name: "explicit override", environment: "production", override: "0.25", want: 0.25},
		{name: "reject invalid override", environment: "production", override: "1.5", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/ledger")
			t.Setenv("APP_ENV", test.environment)
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", test.override)
			cfg, err := Load()
			if test.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want invalid sampling ratio error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TraceSampleRatio != test.want {
				t.Fatalf("TraceSampleRatio = %v, want %v", cfg.TraceSampleRatio, test.want)
			}
		})
	}
}
