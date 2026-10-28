package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/health"
	"github.com/0xKimutai/double-entry-ledger/migrations"
)

type ReadinessChecker struct {
	db              *sql.DB
	expectedVersion uint
}

func NewReadinessChecker(db *sql.DB) (*ReadinessChecker, error) {
	version, err := migrations.LatestVersion()
	if err != nil {
		return nil, err
	}
	return &ReadinessChecker{db: db, expectedVersion: version}, nil
}

func (c *ReadinessChecker) Check(ctx context.Context) map[string]health.Component {
	components := make(map[string]health.Component, 2)

	startedAt := time.Now()
	if err := c.db.PingContext(ctx); err != nil {
		components["database"] = health.Component{
			Status: "unhealthy",
			Error:  "database ping failed",
			Cause:  err,
		}
		components["migrations"] = health.Component{
			Status:          "not_checked",
			ExpectedVersion: uintPointer(c.expectedVersion),
			Error:           "database is unavailable",
			Cause:           err,
		}
		return components
	}
	components["database"] = health.Component{
		Status:    "healthy",
		LatencyMS: int64Pointer(time.Since(startedAt).Milliseconds()),
	}

	version, dirty, err := MigrationStatus(c.db)
	migrationStatus := "healthy"
	message := ""
	switch {
	case err != nil:
		migrationStatus = "unhealthy"
		message = "migration state could not be read"
	case dirty:
		migrationStatus = "unhealthy"
		message = "database has a dirty migration"
	case version != c.expectedVersion:
		migrationStatus = "unhealthy"
		message = "database schema is not up to date"
	}
	components["migrations"] = health.Component{
		Status:          migrationStatus,
		Version:         uintPointer(version),
		ExpectedVersion: uintPointer(c.expectedVersion),
		Dirty:           boolPointer(dirty),
		Error:           message,
		Cause:           err,
	}
	return components
}

func int64Pointer(value int64) *int64 {
	return &value
}

func uintPointer(value uint) *uint {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
