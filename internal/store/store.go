// Package store is the control plane's Postgres source of truth: goose
// migrations and sqlc-generated queries over a pgx v5 pool (ADR-0009,
// ADR-0018).
//
// Queries live in queries/*.sql and generate into the db subpackage
// (`make sqlc`); migrations live in migrations/ and are embedded.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/St1viG/cj/internal/store/db"
)

// Store is a pgx pool with the generated queries bound to it.
type Store struct {
	*db.Queries
	Pool *pgxpool.Pool
}

// Open connects to databaseURL and verifies the connection.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Queries: db.New(pool), Pool: pool}, nil
}

// Close releases every pooled connection.
func (s *Store) Close() { s.Pool.Close() }

// Tx is one open transaction: the generated queries plus the raw pgx.Tx for
// hand-written SQL such as the placement transaction.
type Tx struct {
	*db.Queries
	pgx.Tx
}

// WithTx runs fn in a read-committed transaction. It commits when fn returns
// nil and rolls back otherwise, returning fn's error unchanged.
func (s *Store) WithTx(ctx context.Context, fn func(tx Tx) error) error {
	return pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(Tx{Queries: s.Queries.WithTx(tx), Tx: tx})
	})
}

// WithTxOptions is WithTx with explicit options, e.g. a repeatable-read,
// read-only transaction for a consistent multi-query read.
func (s *Store) WithTxOptions(ctx context.Context, opts pgx.TxOptions, fn func(tx Tx) error) error {
	return pgx.BeginTxFunc(ctx, s.Pool, opts, func(tx pgx.Tx) error {
		return fn(Tx{Queries: s.Queries.WithTx(tx), Tx: tx})
	})
}

// ErrNotFound is returned by lookups that matched no row.
var ErrNotFound = errors.New("store: not found")

// NotFound maps pgx.ErrNoRows to ErrNotFound and leaves other errors alone.
func NotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Postgres error codes the control plane reacts to.
const (
	codeUniqueViolation      = "23505"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
)

// IsUniqueViolation reports whether err is a unique-constraint violation. If
// constraint is not empty, only a violation of that constraint (or index)
// matches.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != codeUniqueViolation {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// IsRetryable reports whether err is a serialization failure or deadlock,
// after which the whole transaction may be retried.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		(pgErr.Code == codeSerializationFailure || pgErr.Code == codeDeadlockDetected)
}
