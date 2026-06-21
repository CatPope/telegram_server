package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PendingSupergroupToken struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
	CreatedAt time.Time
}

var (
	ErrTokenNotFound = errors.New("registry: pending supergroup token not found")
	ErrTokenExpired  = errors.New("registry: pending supergroup token expired")
)

type SupergroupStore struct {
	pool *pgxpool.Pool
}

func NewSupergroupStore(pool *pgxpool.Pool) *SupergroupStore {
	return &SupergroupStore{pool: pool}
}

// IssueToken inserts a startgroup deeplink token for a user with TTL.
// Idempotent on (token, user_id) — caller guarantees randomness.
func (s *SupergroupStore) IssueToken(ctx context.Context, token string, userID int64, ttl time.Duration) error {
	const q = `
		INSERT INTO pending_supergroup_tokens (token, user_id, expires_at)
		VALUES ($1, $2, now() + ($3 || ' seconds')::interval)
		ON CONFLICT (token) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, token, userID, fmt.Sprintf("%d", int(ttl.Seconds())))
	if err != nil {
		return fmt.Errorf("registry: issue token: %w", err)
	}
	return nil
}

// ConsumeToken atomically deletes a token and returns the associated user_id.
// Returns ErrTokenNotFound for unknown token, ErrTokenExpired if past TTL.
func (s *SupergroupStore) ConsumeToken(ctx context.Context, token string) (int64, error) {
	const q = `
		DELETE FROM pending_supergroup_tokens
		WHERE token = $1
		RETURNING user_id, expires_at`
	var userID int64
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, q, token).Scan(&userID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrTokenNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("registry: consume token: %w", err)
	}
	if time.Now().After(expiresAt) {
		return 0, ErrTokenExpired
	}
	return userID, nil
}

// LinkSupergroup binds the user to their newly-created personal supergroup
// chat_id. Sets linked_at=now() but leaves bot_is_admin_in_supergroup false
// until the user grants admin permissions.
func (s *SupergroupStore) LinkSupergroup(ctx context.Context, userID, chatID int64) error {
	const q = `
		UPDATE users
		SET personal_supergroup_chat_id = $1,
		    personal_supergroup_linked_at = now(),
		    bot_is_admin_in_supergroup = false,
		    updated_at = now()
		WHERE id = $2 AND status <> 'anonymized'`
	_, err := s.pool.Exec(ctx, q, chatID, userID)
	if err != nil {
		return fmt.Errorf("registry: link supergroup: %w", err)
	}
	return nil
}

// SetBotIsAdmin reflects a my_chat_member update where the bot was promoted
// (true) or demoted/kicked (false). When false the dispatch path will fail
// fast for that user with bot_not_admin.
func (s *SupergroupStore) SetBotIsAdmin(ctx context.Context, userID int64, isAdmin bool) error {
	const q = `
		UPDATE users
		SET bot_is_admin_in_supergroup = $1, updated_at = now()
		WHERE id = $2`
	_, err := s.pool.Exec(ctx, q, isAdmin, userID)
	if err != nil {
		return fmt.Errorf("registry: set bot is admin: %w", err)
	}
	return nil
}

// ResetLink wipes the personal_supergroup binding so the user can re-issue
// /start and create a fresh supergroup (recovery path for deleted /
// inaccessible groups).
func (s *SupergroupStore) ResetLink(ctx context.Context, userID int64) error {
	const q = `
		UPDATE users
		SET personal_supergroup_chat_id = NULL,
		    personal_supergroup_linked_at = NULL,
		    bot_is_admin_in_supergroup = false,
		    updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("registry: reset link: %w", err)
	}
	return nil
}

// FindUserByChatID looks up which user owns a given personal supergroup
// chat_id. Used by my_chat_member / chat_member event handlers to attribute
// updates to the right user without payload tokens.
func (s *SupergroupStore) FindUserByChatID(ctx context.Context, chatID int64) (int64, error) {
	const q = `SELECT id FROM users WHERE personal_supergroup_chat_id = $1`
	var userID int64
	err := s.pool.QueryRow(ctx, q, chatID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("registry: find by chat: %w", err)
	}
	return userID, nil
}
