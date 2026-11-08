package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"

	// Registers the "pgx" database/sql driver goose runs on.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the embedded goose migration files.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return sub
}

// MigrationsHash is a short digest of every migration file, used to key
// cached test template databases.
func MigrationsHash() string {
	entries, err := fs.ReadDir(Migrations(), ".")
	if err != nil {
		panic(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(Migrations(), n)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Migrator applies the embedded migrations to one database.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// NewMigrator opens a dedicated database/sql connection for goose. Close it
// when done.
func NewMigrator(databaseURL string) (*Migrator, error) {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: open for migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, Migrations())
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: goose: %w", err)
	}
	return &Migrator{db: sqlDB, provider: p}, nil
}

// Close closes the migration connection.
func (m *Migrator) Close() error { return m.db.Close() }

// Up applies every pending migration.
func (m *Migrator) Up(ctx context.Context) ([]*goose.MigrationResult, error) {
	return m.provider.Up(ctx)
}

// Down rolls back the most recent migration.
func (m *Migrator) Down(ctx context.Context) (*goose.MigrationResult, error) {
	return m.provider.Down(ctx)
}

// Reset rolls back every migration.
func (m *Migrator) Reset(ctx context.Context) ([]*goose.MigrationResult, error) {
	return m.provider.DownTo(ctx, 0)
}

// Status lists every migration and whether it is applied.
func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	return m.provider.Status(ctx)
}

// Version is the highest applied migration version.
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	return m.provider.GetDBVersion(ctx)
}

// MigrateUp applies every pending migration to databaseURL.
func MigrateUp(ctx context.Context, databaseURL string) error {
	m, err := NewMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	_, err = m.Up(ctx)
	return err
}
