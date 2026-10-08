// Package storetest opens a store on the throwaway Postgres that the Nix
// build starts for the tests (postgresqlTestHook sets PGHOST and friends, and
// the build sets AGENTS_TEST_POSTGRES). Each test package gets its own
// database, since packages' tests run at the same time.
package storetest

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"homelab/agents/internal/store"
)

// Open returns a store on database db, with its tables empty. It skips the
// test when there is no test Postgres.
func Open(t *testing.T, db string) *store.Store {
	t.Helper()
	if os.Getenv("AGENTS_TEST_POSTGRES") == "" {
		t.Skip("AGENTS_TEST_POSTGRES is not set; the Nix build runs this against a throwaway Postgres")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var pgErr *pgconn.PgError
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize())
	if err != nil && !(errors.As(err, &pgErr) && pgErr.Code == "42P04") { // duplicate_database
		t.Fatal(err)
	}

	s, err := store.Open(ctx, "dbname="+db, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	conn, err := pgx.Connect(ctx, "dbname="+db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "TRUNCATE sessions, messages"); err != nil {
		t.Fatal(err)
	}
	return s
}
