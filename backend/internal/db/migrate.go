package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"

	"kppbwi.app/filemanager/migrations"
)

// MigrationStatus is one migration with its applied state.
type MigrationStatus struct {
	Version   int64     `json:"version"`
	Name      string    `json:"name"`
	Applied   bool      `json:"applied"`
	AppliedAt time.Time `json:"applied_at,omitempty"`
}

func (d *DB) provider() (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectSQLite3, d.w, migrations.FS, goose.WithVerbose(false))
	if err != nil {
		return nil, fmt.Errorf("db: goose provider: %w", err)
	}
	return p, nil
}

// MigrateUp applies all pending migrations and returns the applied versions.
func (d *DB) MigrateUp(ctx context.Context) ([]int64, error) {
	p, err := d.provider()
	if err != nil {
		return nil, err
	}
	res, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: migrate up: %w", err)
	}
	out := make([]int64, 0, len(res))
	for _, r := range res {
		out = append(out, r.Source.Version)
	}
	return out, nil
}

// MigrateDown rolls back the most recent migration. It returns (0, nil) when
// nothing is applied.
func (d *DB) MigrateDown(ctx context.Context) (int64, error) {
	p, err := d.provider()
	if err != nil {
		return 0, err
	}
	res, err := p.Down(ctx)
	if err != nil {
		if errors.Is(err, goose.ErrNoNextVersion) || errors.Is(err, goose.ErrNoCurrentVersion) {
			return 0, nil
		}
		return 0, fmt.Errorf("db: migrate down: %w", err)
	}
	return res.Source.Version, nil
}

// MigrateStatus lists every embedded migration and whether it is applied.
func (d *DB) MigrateStatus(ctx context.Context) ([]MigrationStatus, error) {
	p, err := d.provider()
	if err != nil {
		return nil, err
	}
	st, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: migrate status: %w", err)
	}
	out := make([]MigrationStatus, 0, len(st))
	for _, s := range st {
		out = append(out, MigrationStatus{
			Version:   s.Source.Version,
			Name:      s.Source.Path,
			Applied:   s.State == goose.StateApplied,
			AppliedAt: s.AppliedAt,
		})
	}
	return out, nil
}

// HasPendingMigrations reports whether any embedded migration is not applied.
func (d *DB) HasPendingMigrations(ctx context.Context) (bool, error) {
	p, err := d.provider()
	if err != nil {
		return false, err
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return false, fmt.Errorf("db: has pending: %w", err)
	}
	return pending, nil
}

// Ready is the readiness probe: SELECT 1 on both pools and no pending migration.
func (d *DB) Ready(ctx context.Context) error {
	if err := d.Ping(ctx); err != nil {
		return err
	}
	pending, err := d.HasPendingMigrations(ctx)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("db: pending migrations")
	}
	return nil
}
