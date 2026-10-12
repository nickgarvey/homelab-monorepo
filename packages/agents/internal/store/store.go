// Package store keeps the worker's records in Postgres: the sessions table
// (the sandbox each conversation has, and its state) and the messages table
// (the chat log). schema.sql defines both.
package store

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type State string

const (
	Creating State = "creating"
	Running  State = "running"
	Stopped  State = "stopped"
	Starting State = "starting"
	Deleting State = "deleting"
	Deleted  State = "deleted"
)

type Session struct {
	Type         string
	Conversation string
	Sandbox      string
	Generation   int
	TypeVersion  string
	State        State
	LastActivity time.Time
	StoppedAt    *time.Time
}

type Message struct {
	Seq       int64
	Role      string
	Content   string
	Turn      string
	CreatedAt time.Time
}

// Roles in the messages table.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	// RoleWakeup is the prompt of a turn the worker started itself: a
	// wake-up.
	RoleWakeup = "wakeup"
)

type Store struct {
	pool *pgxpool.Pool
}

// TLSFiles is a client certificate to log in with, and the CA to verify the
// server against. They are read for every new connection, so a renewed
// certificate is used without a restart.
type TLSFiles struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

func (f *TLSFiles) config(host string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("postgres client certificate: %w", err)
	}
	ca, err := os.ReadFile(f.CAFile)
	if err != nil {
		return nil, fmt.Errorf("postgres CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("postgres CA %s: no certificates", f.CAFile)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   host,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// Open connects to Postgres and applies the schema. connString must not set
// any ssl* parameter when files is given: the TLS settings come from files.
func Open(ctx context.Context, connString string, files *TLSFiles) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	if files != nil {
		cfg.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			t, err := files.config(cc.Host)
			if err != nil {
				return err
			}
			cc.TLSConfig, cc.Fallbacks = t, nil
			return nil
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

// SandboxName is chat-<hash>-<generation>. The hash is 8 base32 characters
// (40 bits) of type/conversation, which leaves room for a five-digit
// generation within the gateway's 19-character limit on sandbox names.
func SandboxName(typ, conversation string, generation int) string {
	return fmt.Sprintf("chat-%s-%d", ConversationHash(typ, conversation), generation)
}

// ConversationHash is the conversation label on its sandboxes.
func ConversationHash(typ, conversation string) string {
	sum := sha256.Sum256([]byte(typ + "/" + conversation))
	return strings.ToLower(base32.StdEncoding.EncodeToString(sum[:5]))
}

const sessionColumns = `agent_type, conversation, sandbox, generation, type_version, state, last_activity, stopped_at`

func scanSession(row pgx.Row) (*Session, error) {
	var s Session
	err := row.Scan(&s.Type, &s.Conversation, &s.Sandbox, &s.Generation, &s.TypeVersion, &s.State, &s.LastActivity, &s.StoppedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Session returns the conversation's row, or nil if it has none.
func (s *Store) Session(ctx context.Context, typ, conversation string) (*Session, error) {
	return scanSession(s.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE agent_type = $1 AND conversation = $2`,
		typ, conversation))
}

// NewGeneration records the conversation's next sandbox, in state creating,
// before it is created: generation 1 for a new conversation, otherwise one
// more than the last.
func (s *Store) NewGeneration(ctx context.Context, typ, conversation, typeVersion string) (*Session, error) {
	var out *Session
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var gen int
		err := tx.QueryRow(ctx,
			`SELECT generation FROM sessions WHERE agent_type = $1 AND conversation = $2 FOR UPDATE`,
			typ, conversation).Scan(&gen)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		gen++
		out, err = scanSession(tx.QueryRow(ctx, `
			INSERT INTO sessions (agent_type, conversation, sandbox, generation, type_version, state)
			VALUES ($1, $2, $3, $4, $5, 'creating')
			ON CONFLICT (agent_type, conversation) DO UPDATE SET
				sandbox = EXCLUDED.sandbox, generation = EXCLUDED.generation,
				type_version = EXCLUDED.type_version, state = 'creating',
				last_activity = now(), stopped_at = NULL
			RETURNING `+sessionColumns,
			typ, conversation, SandboxName(typ, conversation, gen), gen, typeVersion))
		return err
	})
	return out, err
}

// Transition moves the conversation's row from one of the states in from to
// state to, if it still names sandbox. It reports whether it did: false
// means another actor (the reaper, or a newer generation) got there first.
// Moving to stopped records the time.
func (s *Store) Transition(ctx context.Context, typ, conversation, sandbox string, from []State, to State) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET state = $5,
			stopped_at = CASE WHEN $5 = 'stopped' THEN now() ELSE stopped_at END
		WHERE agent_type = $1 AND conversation = $2 AND sandbox = $3 AND state = ANY($4)`,
		typ, conversation, sandbox, from, to)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Touch records activity in the conversation now.
func (s *Store) Touch(ctx context.Context, typ, conversation string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET last_activity = now() WHERE agent_type = $1 AND conversation = $2`,
		typ, conversation)
	return err
}

// AddMessage appends a message to the chat log, unless the turn already has
// a message with that role.
func (s *Store) AddMessage(ctx context.Context, typ, conversation, turn, role, content string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO messages (agent_type, conversation, seq, role, content, turn)
		SELECT $1, $2, COALESCE(MAX(seq), 0) + 1, $3, $4, $5
		FROM messages WHERE agent_type = $1 AND conversation = $2
		ON CONFLICT (agent_type, conversation, turn, role) DO NOTHING`,
		typ, conversation, role, content, turn)
	return err
}

// TurnMessage returns the turn's message with the given role, if recorded.
func (s *Store) TurnMessage(ctx context.Context, typ, conversation, turn, role string) (string, bool, error) {
	var content string
	err := s.pool.QueryRow(ctx,
		`SELECT content FROM messages WHERE agent_type = $1 AND conversation = $2 AND turn = $3 AND role = $4`,
		typ, conversation, turn, role).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return content, err == nil, err
}

// Recent returns the conversation's last n messages outside turn, oldest
// first.
func (s *Store) Recent(ctx context.Context, typ, conversation, excludeTurn string, n int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT seq, role, content, turn, created_at FROM (
			SELECT * FROM messages
			WHERE agent_type = $1 AND conversation = $2 AND turn <> $3
			ORDER BY seq DESC LIMIT $4
		) last ORDER BY seq`,
		typ, conversation, excludeTurn, n)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Message, error) {
		var m Message
		err := row.Scan(&m.Seq, &m.Role, &m.Content, &m.Turn, &m.CreatedAt)
		return m, err
	})
}

// Sessions returns the rows in the given states, ordered by type and
// conversation.
func (s *Store) Sessions(ctx context.Context, states ...State) ([]Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE state = ANY($1) ORDER BY agent_type, conversation`,
		states)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Session, error) {
		s, err := scanSession(row)
		return *s, err
	})
}

// ClaimExpired moves the conversation's row from stopped to deleting if it
// still names sandbox and has been stopped for at least retention, by the
// database's clock. A row a message has claimed (starting), or that stopped
// again since, is left alone and ClaimExpired reports false.
func (s *Store) ClaimExpired(ctx context.Context, typ, conversation, sandbox string, retention time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET state = 'deleting'
		WHERE agent_type = $1 AND conversation = $2 AND sandbox = $3 AND state = 'stopped'
			AND stopped_at <= now() - make_interval(secs => $4)`,
		typ, conversation, sandbox, retention.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// HoldsSandbox reports whether a row names sandbox in a state other than
// deleted. A deleted row's name is never used again: the conversation's next
// sandbox gets the next generation.
func (s *Store) HoldsSandbox(ctx context.Context, sandbox string) (bool, error) {
	var held bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sessions WHERE sandbox = $1 AND state <> 'deleted')`,
		sandbox).Scan(&held)
	return held, err
}
