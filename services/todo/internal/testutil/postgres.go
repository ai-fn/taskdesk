// Package testutil provides shared test infrastructure for todo service tests.
// It is a regular package (not a _test package) so it can be imported by
// test files in multiple internal packages.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pkgpgx "github.com/fsociety/todo/pkg/pgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgres starts one ephemeral postgres:16-alpine container, applies
// all todo migrations (000001–000011), and returns the pool and a teardown
// function. Designed for use in TestMain so the container is shared across all
// tests in a package — far cheaper than one container per test.
//
// Usage in a test package:
//
//	var testPool *pgxpool.Pool
//
//	func TestMain(m *testing.M) {
//	    pool, teardown := testutil.StartPostgres()
//	    testPool = pool
//	    code := m.Run()
//	    teardown()
//	    os.Exit(code)
//	}
//
// When Docker is unavailable the function prints a skip notice and calls
// os.Exit(0) so the binary reports success rather than failure.
func StartPostgres() (pool *pgxpool.Pool, teardown func()) {
	// Disable the ryuk reaper — it requires a separate image that may not be
	// pre-pulled in restricted-network environments.
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testtodo"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		if isDockerUnavailable(err) {
			fmt.Println("SKIP: Docker unavailable, skipping DB integration tests:", err)
			os.Exit(0)
		}
		fmt.Println("FATAL: start postgres container:", err)
		os.Exit(1)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Println("FATAL: container DSN:", err)
		os.Exit(1)
	}

	p, err := pkgpgx.New(ctx, pkgpgx.Config{DSN: dsn, MaxConns: 5, MinConns: 1}, slog.Default())
	if err != nil {
		fmt.Println("FATAL: open pool:", err)
		os.Exit(1)
	}

	migrationsDir := "file://" + findMigrationsDir()
	if err := pkgpgx.Migrate(dsn, migrationsDir, slog.Default()); err != nil {
		fmt.Println("FATAL: migrate:", err)
		os.Exit(1)
	}

	return p, func() {
		p.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Println("WARN: terminate container:", err)
		}
	}
}

// Truncate removes all rows from todo tables between tests, preserving schema.
func Truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE todo.user_storage, todo.attachments, todo.outbox, todo.tasks, todo.lists, todo.list_groups CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// findMigrationsDir walks up from this source file until it finds go.work,
// then returns the path to migrations/todo relative to that root.
func findMigrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Println("FATAL: runtime.Caller failed")
		os.Exit(1)
	}

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			p := filepath.Join(dir, "migrations", "todo")
			if _, err := os.Stat(p); err != nil {
				fmt.Printf("FATAL: migrations dir not found at %s: %v\n", p, err)
				os.Exit(1)
			}
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fmt.Println("FATAL: could not find go.work walking up from", file)
			os.Exit(1)
		}
		dir = parent
	}
}

func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := fmt.Sprintf("%v", err)
	for _, s := range []string{
		"Cannot connect to the Docker daemon",
		"docker: not found",
		"no such file",
		"permission denied",
		"connection refused",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
