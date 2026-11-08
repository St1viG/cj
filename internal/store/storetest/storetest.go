// Package storetest gives tests their own Postgres database.
//
// It reads KONDUKTER_DATABASE_URL (set by `make test` from the env file and
// by CI). Each test gets a fresh database cloned from a template that has
// every migration applied; the template is built once per migration set and
// reused across test processes. When the server is unreachable the test is
// skipped locally (start it with `make db-up`) and fails in CI.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/St1viG/cj/internal/store"
)

// EnvDatabaseURL names the variable holding the admin connection URL.
const EnvDatabaseURL = "KONDUKTER_DATABASE_URL"

// New returns a Store on a fresh, fully migrated database that is dropped
// when the test ends.
func New(t testing.TB) *store.Store {
	t.Helper()
	dbURL := newDatabase(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("storetest: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// NewEmptyURL returns the URL of a fresh database with no migrations
// applied, dropped when the test ends.
func NewEmptyURL(t testing.TB) string {
	t.Helper()
	return newDatabase(t, false)
}

func newDatabase(t testing.TB, migrated bool) string {
	t.Helper()
	base := os.Getenv(EnvDatabaseURL)
	if base == "" {
		unavailable(t, EnvDatabaseURL+" is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		unavailable(t, fmt.Sprintf("cannot connect to %s: %v", redact(base), err))
	}
	defer func() { _ = admin.Close(context.Background()) }()

	// One advisory lock serializes template builds and clones across every
	// test process using this server.
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		t.Fatalf("storetest: advisory lock: %v", err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey) }()

	name := "kdk_test_" + randomHex(6)
	create := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if migrated {
		tmpl, err := ensureTemplate(ctx, admin, base)
		if err != nil {
			t.Fatalf("storetest: template: %v", err)
		}
		create += " TEMPLATE " + pgx.Identifier{tmpl}.Sanitize()
	}
	if _, err := admin.Exec(ctx, create); err != nil {
		t.Fatalf("storetest: %s: %v", create, err)
	}
	t.Cleanup(func() { dropDatabase(t, base, name) })
	return withDatabase(base, name)
}

// lockKey is an arbitrary constant shared by every storetest user.
var lockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("kondukter/storetest")) // hash.Hash.Write never fails
	return int64(h.Sum64() >> 1)
}()

// ensureTemplate builds the migrated template database for the current
// migration set if it does not exist yet and drops templates of older sets.
func ensureTemplate(ctx context.Context, admin *pgx.Conn, base string) (string, error) {
	tmpl := "kdk_tmpl_" + store.MigrationsHash()
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", tmpl).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return tmpl, nil
	}

	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'kdk\\_tmpl\\_%'")
	if err != nil {
		return "", err
	}
	stale, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	for _, s := range stale {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{s}.Sanitize()+" WITH (FORCE)")
	}

	building := tmpl + "_building"
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{building}.Sanitize()+" WITH (FORCE)")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{building}.Sanitize()); err != nil {
		return "", err
	}
	if err := store.MigrateUp(ctx, withDatabase(base, building)); err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	// Rename only once fully migrated, so a crashed build never leaves a
	// half-migrated template behind under the final name.
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{building}.Sanitize()+" RENAME TO "+pgx.Identifier{tmpl}.Sanitize()); err != nil {
		return "", err
	}
	return tmpl, nil
}

func dropDatabase(t testing.TB, base, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Logf("storetest: drop %s: %v", name, err)
		return
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Logf("storetest: drop %s: %v", name, err)
	}
}

func unavailable(t testing.TB, why string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("storetest: Postgres required in CI: %s", why)
	}
	t.Skipf("storetest: Postgres unavailable (%s); start it with `make db-up`", why)
}

func withDatabase(base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(fmt.Sprintf("storetest: bad %s: %v", EnvDatabaseURL, err))
	}
	u.Path = "/" + name
	return u.String()
}

func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable url>"
	}
	return u.Redacted()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(hex.EncodeToString(b))
}
