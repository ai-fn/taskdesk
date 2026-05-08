package pgx

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate runs all pending UP migrations from migrationsDir against dsn.
// migrationsDir is a path like "file:///abs/path/to/migrations/auth".
// It is safe to call on every startup — already-applied migrations are no-ops.
func Migrate(dsn, migrationsDir string, log *slog.Logger) error {
	m, err := migrate.New(migrationsDir, dsn)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	ver, dirty, _ := m.Version()
	log.Info("migrations applied", "version", ver, "dirty", dirty)
	return nil
}
