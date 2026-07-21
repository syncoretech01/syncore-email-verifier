//go:build live

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestPostgres_Integration exercises the Postgres store against a real database.
// It is excluded from the default suite (build tag `live`) and skips unless
// SYNCORE_VERIFIER_TEST_DATABASE_URL points at a disposable Postgres.
//
//	docker run --rm -e POSTGRES_PASSWORD=pw -p 5433:5432 -d postgres:16
//	SYNCORE_VERIFIER_TEST_DATABASE_URL=postgres://postgres:pw@localhost:5433/postgres \
//	  go test -tags=live ./internal/store/ -run TestPostgres_Integration
func TestPostgres_Integration(t *testing.T) {
	url := os.Getenv("SYNCORE_VERIFIER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set SYNCORE_VERIFIER_TEST_DATABASE_URL to run the Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()

	pg, err := NewPostgres[string](ctx, pool)
	require.NoError(t, err)
	require.NoError(t, pg.PurgeExpired(ctx))

	// Round-trip a live value.
	require.NoError(t, pg.Set(ctx, "rt-key", "hello", time.Minute))
	got, ok, err := pg.Get(ctx, "rt-key")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", got)

	// Upsert overwrites.
	require.NoError(t, pg.Set(ctx, "rt-key", "world", time.Minute))
	got, ok, err = pg.Get(ctx, "rt-key")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "world", got)

	// Expiry: a very short ttl is gone shortly after.
	require.NoError(t, pg.Set(ctx, "exp-key", "x", 20*time.Millisecond))
	time.Sleep(60 * time.Millisecond)
	_, ok, err = pg.Get(ctx, "exp-key")
	require.NoError(t, err)
	require.False(t, ok, "expired row must read as absent")

	// Missing key.
	_, ok, err = pg.Get(ctx, "nope")
	require.NoError(t, err)
	require.False(t, ok)

	// Non-positive ttl stores nothing.
	require.NoError(t, pg.Set(ctx, "zero", "x", 0))
	_, ok, _ = pg.Get(ctx, "zero")
	require.False(t, ok)
}
