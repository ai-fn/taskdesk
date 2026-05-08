// Package pgx wraps pgxpool with sane defaults and a migrations runner.
package pgx

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds pool tuning knobs. Embed in your service config struct.
type Config struct {
	// DSN is the Postgres connection string (DATABASE_URL).
	// May include golang-migrate params like x-migrations-table — they are
	// stripped before opening the pool connection.
	DSN             string `env:"DATABASE_URL,required"`
	MaxConns        int32  `env:"DB_MAX_CONNS"       default:"10"`
	MinConns        int32  `env:"DB_MIN_CONNS"       default:"2"`
	MaxConnIdleTime int    `env:"DB_MAX_IDLE_SECS"   default:"300"`
}

// New opens a pgxpool, pings, and returns it.
// Caller is responsible for calling pool.Close().
func New(ctx context.Context, cfg Config, log *slog.Logger) (*pgxpool.Pool, error) {
	dsn := poolDSN(cfg.DSN)

	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}

	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnIdleTime = time.Duration(cfg.MaxConnIdleTime) * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	log.Info("postgres connected", "host", pcfg.ConnConfig.Host, "db", pcfg.ConnConfig.Database)
	return pool, nil
}

// poolDSN strips golang-migrate-specific query params (x-*) from the DSN so
// pgxpool doesn't forward them to Postgres as unknown configuration params.
func poolDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	for key := range q {
		if strings.HasPrefix(key, "x-") {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
