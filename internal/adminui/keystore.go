package adminui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyStore is the admin UI's only write boundary into the database:
// issuing and revoking app_keys rows, the one management operation with
// no /admin API (admin-ui-plan.md §A3). It is a separate interface from
// the read-only Store so the type system documents exactly where writes
// can originate. Hashes are write-only — no method ever returns key_hash.
type KeyStore interface {
	ListKeys(ctx context.Context, appID string) ([]KeyRow, error)
	IssueKey(ctx context.Context, appID, prefix, hash, label string) error
	RevokeKey(ctx context.Context, appID, prefix string) error
}

// KeyRow is a display projection of an app_keys row. It deliberately has
// no hash field.
type KeyRow struct {
	Prefix    string
	Label     string
	CreatedAt time.Time
	RevokedAt *time.Time
}

var (
	// ErrAppInactive: keys must not be issued for deactivated apps.
	ErrAppInactive = errors.New("adminui: app inactive")
	// ErrPrefixTaken: the prefix collides with any existing key, revoked
	// ones included — reusing a revoked prefix would make audit entries
	// (which record only the prefix) ambiguous.
	ErrPrefixTaken = errors.New("adminui: key prefix already in use")
	// ErrKeyNotFound: no active key with that prefix (missing or already
	// revoked).
	ErrKeyNotFound = errors.New("adminui: key not found or already revoked")
)

type pgKeyStore struct {
	pool *pgxpool.Pool
}

// NewKeyStore builds a KeyStore backed by pool. Like NewStore, a nil pool
// (DATABASE_URL unset) yields a nil KeyStore so handlers have a single
// "DB unavailable" check.
func NewKeyStore(pool *pgxpool.Pool) KeyStore {
	if pool == nil {
		return nil
	}
	return &pgKeyStore{pool: pool}
}

func (s *pgKeyStore) ListKeys(ctx context.Context, appID string) ([]KeyRow, error) {
	const q = `
		SELECT key_prefix, label, created_at, revoked_at
		FROM app_keys
		WHERE app_id = $1
		ORDER BY created_at DESC, id DESC`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, appID)
	if err != nil {
		return nil, fmt.Errorf("adminui: list keys: %w", err)
	}
	defer rows.Close()

	var keys []KeyRow
	for rows.Next() {
		var k KeyRow
		if scanErr := rows.Scan(&k.Prefix, &k.Label, &k.CreatedAt, &k.RevokedAt); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan key: %w", scanErr)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return keys, nil
}

// IssueKey inserts a new app_keys row inside one transaction: the app
// must exist and be active, and the prefix must be unused. hash is the
// Argon2id encoding of the full plaintext token — the plaintext itself
// never reaches this layer.
func (s *pgKeyStore) IssueKey(ctx context.Context, appID, prefix, hash, label string) error {
	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("adminui: issue key: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM apps WHERE id = $1`, appID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAppNotFound
	}
	if err != nil {
		return fmt.Errorf("adminui: issue key: check app: %w", err)
	}
	if !active {
		return ErrAppInactive
	}

	// Friendly pre-check; the real guarantee is the app_keys_prefix_uniq
	// unique index (migration 0006), surfaced below as 23505 on INSERT.
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_keys WHERE key_prefix = $1)`, prefix).Scan(&taken); err != nil {
		return fmt.Errorf("adminui: issue key: check prefix: %w", err)
	}
	if taken {
		return ErrPrefixTaken
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO app_keys (app_id, key_hash, key_prefix, label) VALUES ($1, $2, $3, $4)`,
		appID, hash, prefix, label,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrPrefixTaken
		}
		return fmt.Errorf("adminui: issue key: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("adminui: issue key: commit: %w", err)
	}
	return nil
}

// RevokeKey is scoped to the app whose page the operator is on, so a
// crafted URL can never revoke another app's key through this route.
func (s *pgKeyStore) RevokeKey(ctx context.Context, appID, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	tag, err := s.pool.Exec(ctx,
		`UPDATE app_keys SET revoked_at = now() WHERE key_prefix = $1 AND app_id = $2 AND revoked_at IS NULL`,
		prefix, appID,
	)
	if err != nil {
		return fmt.Errorf("adminui: revoke key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}
