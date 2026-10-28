package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/api"
	"github.com/0xKimutai/double-entry-ledger/internal/config"
	"github.com/0xKimutai/double-entry-ledger/internal/health"
	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"github.com/0xKimutai/double-entry-ledger/internal/observability"
	"github.com/0xKimutai/double-entry-ledger/internal/storage/postgres"
	"github.com/0xKimutai/double-entry-ledger/migrations"
	"github.com/0xKimutai/double-entry-ledger/pkg/logger"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "unknown"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
	dependencyTimeout = 30 * time.Second
)

func main() {
	startedAt := time.Now()
	cfg, configErr := config.Load()

	environment := os.Getenv("APP_ENV")
	if configErr == nil {
		environment = cfg.Environment
	}
	log, err := logger.New(environment)
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize logger:", err)
		os.Exit(1)
	}

	if configErr != nil {
		log.Error("load application configuration", zap.Error(configErr))
		_ = log.Sync()
		os.Exit(1)
	}
	if err := run(cfg, log, startedAt); err != nil {
		log.Error("ledger API stopped with an error", zap.Error(err))
		_ = log.Sync()
		os.Exit(1)
	}
	if err := log.Sync(); err != nil {
		fmt.Fprintln(os.Stderr, "sync logger:", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *zap.Logger, startupStartedAt time.Time) error {
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), dependencyTimeout)
	log.Info("initializing tracing",
		zap.Bool("export_enabled", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""),
		zap.Float64("sample_ratio", cfg.TraceSampleRatio),
	)
	tracerProvider, tracingEnabled, err := observability.InitTracing(startupCtx, log, cfg.TraceSampleRatio)
	cancelStartup()
	if err != nil {
		return logStartupFailure(log, "initialize tracing", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Error("shut down tracing", zap.Error(err))
		}
	}()
	log.Info("tracing initialized", zap.Bool("export_enabled", tracingEnabled))

	dbCtx, cancelDB := context.WithTimeout(context.Background(), dependencyTimeout)
	log.Info("connecting to PostgreSQL")
	dbStartedAt := time.Now()
	db, err := postgres.Open(dbCtx, cfg.DatabaseURL)
	cancelDB()
	if err != nil {
		return logStartupFailure(log, "connect to PostgreSQL", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("close PostgreSQL connection pool", zap.Error(err))
		}
	}()
	log.Info("PostgreSQL connection ready", zap.Duration("duration", time.Since(dbStartedAt)))

	expectedMigrationVersion, err := migrations.LatestVersion()
	if err != nil {
		return logStartupFailure(log, "read embedded migration version", err)
	}
	log.Info("applying database migrations", zap.Uint("expected_version", expectedMigrationVersion))
	migrationStartedAt := time.Now()
	if err := postgres.Migrate(db); err != nil {
		return logStartupFailure(log, "apply database migrations", err)
	}
	migrationVersion, dirty, err := postgres.MigrationStatus(db)
	if err != nil {
		return logStartupFailure(log, "verify database migrations", err)
	}
	if dirty || migrationVersion != expectedMigrationVersion {
		err := fmt.Errorf("migration verification failed: version=%d expected=%d dirty=%t",
			migrationVersion, expectedMigrationVersion, dirty)
		return logStartupFailure(log, "verify database migrations", err)
	}
	log.Info("database migrations ready",
		zap.Uint("version", migrationVersion),
		zap.Duration("duration", time.Since(migrationStartedAt)),
	)

	readiness, err := postgres.NewReadinessChecker(db)
	if err != nil {
		return logStartupFailure(log, "initialize readiness checks", err)
	}
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 2*time.Second)
	components := readiness.Check(readyCtx)
	cancelReady()
	components["api"] = health.Component{Status: "healthy"}
	report := health.NewReport(components, startupStartedAt, time.Now())
	if report.Status != "healthy" {
		return logStartupFailure(log, "initial readiness check", fmt.Errorf("components are not ready: %s", componentSummary(components)))
	}
	log.Info("service dependencies ready", zap.Any("components", componentSummary(components)))

	routes, err := api.RegisteredRoutes()
	if err != nil {
		return logStartupFailure(log, "inspect registered routes", err)
	}
	if cfg.Environment == "development" || log.Core().Enabled(zap.DebugLevel) {
		log.Debug("registered HTTP routes", zap.Any("routes", routes))
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return logStartupFailure(log, "bind HTTP listener", fmt.Errorf("listen on %s: %w", cfg.HTTPAddress, err))
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           api.NewRouter(ledger.NewService(postgres.NewRepository(db)), log, readiness, observability.NewMetrics(db), startupStartedAt),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	log.Info("ledger API listening",
		zap.String("address", cfg.HTTPAddress),
		zap.String("environment", cfg.Environment),
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("go_version", runtime.Version()),
		zap.Int("route_count", len(routes)),
		zap.Duration("read_header_timeout", readHeaderTimeout),
		zap.Duration("idle_timeout", idleTimeout),
		zap.Bool("tracing_enabled", tracingEnabled),
		zap.Float64("trace_sample_ratio", cfg.TraceSampleRatio),
		zap.String("metrics_path", "/metrics"),
		zap.Duration("startup_duration", time.Since(startupStartedAt)),
	)

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("serve HTTP: %w", err)
	case <-shutdownCtx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		log.Info("ledger API stopped")
		return nil
	}
}

func logStartupFailure(log *zap.Logger, operation string, err error) error {
	log.Error("startup dependency initialization failed",
		zap.String("operation", operation),
		zap.Error(err),
	)
	return fmt.Errorf("%s: %w", operation, err)
}

func componentSummary(components map[string]health.Component) []string {
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)

	summary := make([]string, 0, len(names))
	for _, name := range names {
		component := components[name]
		summary = append(summary, fmt.Sprintf("%s=%s", name, component.Status))
	}
	return summary
}
