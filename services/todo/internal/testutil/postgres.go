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

// StartPostgres runs one pgvector Postgres container (migrations need vector and pg_trgm)
// with all todo migrations applied, for TestMain. Without Docker it returns a nil pool so
// DB tests can skip, unless REQUIRE_DOCKER=1 (set in CI), which makes that a failure.
func StartPostgres() (pool *pgxpool.Pool, teardown func()) {
	// Ryuk needs a separate image that may not be pre-pulled.
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
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
		if isDockerUnavailable(err) && os.Getenv("REQUIRE_DOCKER") != "1" {
			fmt.Println("SKIP: Docker unavailable, DB integration tests will be skipped:", err)
			return nil, func() {}
		}
		fmt.Println("FATAL: start postgres container:", err)
		os.Exit(1)
	}
	fatal := func(what string, err error) {
		fmt.Printf("FATAL: %s: %v\n", what, err)
		_ = testcontainers.TerminateContainer(container) // Ryuk is off, so don't orphan it
		os.Exit(1)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fatal("container DSN", err)
	}
	p, err := pkgpgx.New(ctx, pkgpgx.Config{DSN: dsn, MaxConns: 5, MinConns: 1}, slog.Default())
	if err != nil {
		fatal("open pool", err)
	}
	if err := pkgpgx.Migrate(dsn, "file://"+findMigrationsDir(), slog.Default()); err != nil {
		p.Close()
		fatal("migrate", err)
	}

	return p, func() {
		p.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Println("WARN: terminate container:", err)
		}
	}
}

// Truncate empties every todo table between tests, preserving schema.
func Truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT quote_ident(tablename) FROM pg_tables WHERE schemaname = 'todo'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, "todo."+name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) == 0 {
		return
	}
	if _, err := pool.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" CASCADE"); err != nil {
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
		"rootless Docker not found",
		"docker.sock: connect: no such file",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
