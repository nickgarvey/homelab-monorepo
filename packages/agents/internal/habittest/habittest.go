// Package habittest runs Habit Helper's PostgREST for integration tests: the
// habit-helper schema on a throwaway Postgres, as in the cluster. Roles
// habit_helper (owner, applies the migrations), habit_agent and habit_admin
// (what PostgREST switches to), and habit_helper_api (PostgREST's NOINHERIT
// login); no anonymous role; JWTs with aud habit-data.
//
// The habit-cli build runs these tests (packages/agents/habit-cli.nix): they
// need AGENTS_TEST_POSTGRES (postgresqlTestHook's Postgres), HABIT_TEST_SCHEMA
// (agents/habit-helper/schema) and postgrest on PATH. The build tests one
// package at a time, and each starts from a new database.
package habittest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// JWTKey is the key PostgREST checks tokens with.
const JWTKey = "integration-test-key-0123456789abcdef0123456789abcdef"

// Start makes the database, applies the migrations and starts PostgREST,
// returning its URL. It skips the test outside the habit-cli build.
func Start(t *testing.T) string {
	t.Helper()
	schema := os.Getenv("HABIT_TEST_SCHEMA")
	if os.Getenv("AGENTS_TEST_POSTGRES") == "" || schema == "" {
		t.Skip("needs AGENTS_TEST_POSTGRES and HABIT_TEST_SCHEMA; the habit-cli Nix build runs it")
	}
	bin, err := exec.LookPath("postgrest")
	if err != nil {
		t.Fatal("postgrest is not on PATH")
	}
	setupDatabase(t, context.Background(), schema)
	return startPostgREST(t, bin)
}

// Token is a PostgREST token for role, as agent-token issues.
func Token(t *testing.T, role string) string {
	return JWT(t, map[string]any{
		"role": role, "aud": "habit-data", "sub": "spiffe://home.garvey.sh/test/" + role,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
}

// JWT signs claims with JWTKey.
func JWT(t *testing.T, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := h + "." + enc.EncodeToString(c)
	mac := hmac.New(sha256.New, []byte(JWTKey))
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil))
}

// setupDatabase makes the roles and database as k8s/manifests/postgres does
// and applies the migrations as the owner, each file in a transaction, as
// tern does.
func setupDatabase(t *testing.T, ctx context.Context, schemaDir string) {
	admin, err := pgx.Connect(ctx, "user=postgres dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, q := range []string{
		"DROP DATABASE IF EXISTS habit_helper WITH (FORCE)",
		"CREATE ROLE habit_helper LOGIN",
		"CREATE ROLE habit_agent NOLOGIN",
		"CREATE ROLE habit_admin NOLOGIN",
		"CREATE ROLE habit_helper_api LOGIN NOINHERIT",
		"GRANT habit_agent, habit_admin TO habit_helper_api",
		"CREATE DATABASE habit_helper OWNER habit_helper",
	} {
		_, err := admin.Exec(ctx, q)
		// A package tested earlier made the roles.
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "42710" {
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	owner, err := pgx.Connect(ctx, "user=habit_helper dbname=habit_helper")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	files, err := filepath.Glob(filepath.Join(schemaDir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations in %s: %v", schemaDir, err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startPostgREST runs PostgREST configured like k8s/manifests/habit-helper's
// Deployment, and returns its URL once it is ready.
func startPostgREST(t *testing.T, bin string) string {
	port, admin := freePort(t), freePort(t)
	host := os.Getenv("PGHOST")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PGRST_DB_URI=postgresql:///habit_helper?host="+url.QueryEscape(host)+"&user=habit_helper_api",
		"PGRST_DB_SCHEMAS=habit",
		"PGRST_DB_EXTRA_SEARCH_PATH=",
		"PGRST_DB_CONFIG=false",
		"PGRST_DB_POOL=5",
		"PGRST_JWT_SECRET="+JWTKey,
		"PGRST_JWT_AUD=habit-data",
		"PGRST_SERVER_HOST=127.0.0.1",
		fmt.Sprintf("PGRST_SERVER_PORT=%d", port),
		fmt.Sprintf("PGRST_ADMIN_SERVER_PORT=%d", admin),
		"PGRST_LOG_LEVEL=warn",
		"LC_ALL=C.UTF-8",
	)
	logFile := filepath.Join(t.TempDir(), "postgrest.log")
	lf, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		lf.Close()
		if t.Failed() {
			log, _ := os.ReadFile(logFile)
			t.Logf("postgrest log:\n%s", log)
		}
	})
	ready := fmt.Sprintf("http://127.0.0.1:%d/ready", admin)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		resp, err := http.Get(ready)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return fmt.Sprintf("http://127.0.0.1:%d", port)
			}
		}
	}
	log, _ := os.ReadFile(logFile)
	t.Fatalf("PostgREST did not get ready:\n%s", log)
	return ""
}
