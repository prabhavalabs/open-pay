package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Config holds PostgreSQL connection configuration.
type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig(url string) Config {
	return Config{
		URL:             url,
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

// NewPool creates a new pgx connection pool.
func NewPool(ctx context.Context, cfg Config, logger zerolog.Logger) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	// pool_max_conns / pool_min_conns in the URL take precedence, so an
	// operator can size pools per deployment (a shared server caps the
	// connections each role may open).
	if !strings.Contains(cfg.URL, "pool_max_conns=") {
		poolConfig.MaxConns = cfg.MaxConns
	}
	if !strings.Contains(cfg.URL, "pool_min_conns=") {
		poolConfig.MinConns = cfg.MinConns
	}
	if poolConfig.MinConns > poolConfig.MaxConns {
		poolConfig.MinConns = poolConfig.MaxConns
	}
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	logger.Info().
		Str("host", poolConfig.ConnConfig.Host).
		Str("database", poolConfig.ConnConfig.Database).
		Int32("max_conns", cfg.MaxConns).
		Msg("database connection pool established")

	return pool, nil
}
