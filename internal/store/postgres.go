package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultCacheTable is the table name used by a Postgres cache store. It is a
// fixed identifier (never user input), so interpolating it into DDL/DML is safe.
const defaultCacheTable = "verification_cache"

// Postgres is a durable Store[V] backed by a single jsonb table with per-row
// expiry. Values are JSON-encoded, so V must be JSON round-trippable.
type Postgres[V any] struct {
	pool  *pgxpool.Pool
	table string
	now   func() time.Time
}

// NewPool opens a pgx connection pool from a DATABASE_URL.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}

// NewPostgres builds a Postgres-backed store and ensures its schema exists
// (idempotent migration). The pool is owned by the caller.
func NewPostgres[V any](ctx context.Context, pool *pgxpool.Pool) (*Postgres[V], error) {
	p := &Postgres[V]{pool: pool, table: defaultCacheTable, now: time.Now}
	if err := p.migrate(ctx); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return p, nil
}

func (p *Postgres[V]) migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %[1]s (
	key        TEXT PRIMARY KEY,
	value      JSONB NOT NULL,
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS %[1]s_expires_at_idx ON %[1]s (expires_at);`, p.table))
	return err
}

// Get returns the live value for key. Expired rows are treated as absent.
func (p *Postgres[V]) Get(ctx context.Context, key string) (V, bool, error) {
	var zero V
	var raw []byte
	err := p.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT value FROM %s WHERE key = $1 AND expires_at > $2`, p.table),
		key, p.now(),
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, false, nil
		}
		return zero, false, err
	}
	var v V
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, false, err
	}
	return v, true, nil
}

// Set upserts value under key with the given ttl. A non-positive ttl stores
// nothing.
func (p *Postgres[V]) Set(ctx context.Context, key string, value V, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (key, value, expires_at) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at`, p.table),
		key, raw, p.now().Add(ttl),
	)
	return err
}

// PurgeExpired deletes expired rows. Optional housekeeping; expiry is already
// enforced on read.
func (p *Postgres[V]) PurgeExpired(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at <= $1`, p.table), p.now())
	return err
}
