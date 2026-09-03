package main

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxPool is a thin wrapper around a pgx connection pool (pgxpool).
type pgxPool struct {
	pool *pgxpool.Pool
}

func newPgxPool(ctx context.Context, connString string) (*pgxPool, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	return &pgxPool{pool: pool}, nil
}

func (p *pgxPool) Close() { p.pool.Close() }

func (p *pgxPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

func (p *pgxPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

func (p *pgxPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

// withUserTx runs fn inside a transaction with the RLS identity GUCs set for
// the current user. This enforces the row-level security policies from
// db/migrations/schema.sql (officer FIR-scoped access, SYSTEM_ADMIN no-content
// rule) on every query executed within fn.
func (p *pgxPool) withUserTx(ctx context.Context, userID, role string, fn func(tx pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// SET LOCAL cannot take bind parameters (PostgreSQL rejects $n in SET with
	// 42601). set_config(..., true) sets the GUC for the transaction, so RLS
	// policies can read it via current_setting, and it is scoped to the tx.
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_role', $1, true)`, role); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_ = tx
	return nil
}

func isNoRows(err error) bool {
	return err == pgx.ErrNoRows
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
