package postgres

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xKimutai/double-entry-ledger/migrations"
	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func Migrate(db *sql.DB) error {
	migrator, err := newMigrator(db)
	if err != nil {
		return err
	}

	latestVersion, err := migrations.LatestVersion()
	if err != nil {
		return err
	}
	version, dirty, err := migrator.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("read current database migration version: %w", err)
	}
	if dirty {
		return fmt.Errorf("database has a dirty migration at version %d; repair it before starting", version)
	}

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	version, dirty, err = migrator.Version()
	if err != nil {
		return fmt.Errorf("verify database migration version: %w", err)
	}
	if dirty {
		return fmt.Errorf("database has a dirty migration at version %d after migration; refusing to start", version)
	}
	if version != latestVersion {
		return fmt.Errorf("database migration version %d does not match embedded version %d; refusing to start", version, latestVersion)
	}
	return nil
}

func newMigrator(db *sql.DB) (*migrate.Migrate, error) {
	source, err := iofs.New(migrations.FS, "sql")
	if err != nil {
		return nil, fmt.Errorf("create embedded migration source: %w", err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL migration driver: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	return migrator, nil
}

func MigrationStatus(db *sql.DB) (version uint, dirty bool, err error) {
	migrator, err := newMigrator(db)
	if err != nil {
		return 0, false, err
	}
	version, dirty, err = migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read database migration version: %w", err)
	}
	return version, dirty, nil
}
