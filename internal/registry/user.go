package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID                     int64
	TelegramID             int64
	Username               string
	Grade                  string
	PreferredLang          string
	AgreedAt               *time.Time
	PersonalSupergroupID   *int64
	PersonalLinkedAt       *time.Time
	BotIsAdmin             bool
	Status                 string
	Anonymized             bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

var ErrUserNotFound = errors.New("registry: user not found")

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// UpsertOnStart inserts a fresh users row for an unknown telegram_id and
// returns the resulting User. If the telegram_id already exists it is a
// no-op that returns the existing row. agreed_at remains NULL until the
// user accepts the PIPA notice via MarkAgreed.
func (s *UserStore) UpsertOnStart(ctx context.Context, telegramID int64, username, preferredLang string) (User, error) {
	const q = `
		INSERT INTO users (telegram_id, username, preferred_lang)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''))
		ON CONFLICT (telegram_id) DO UPDATE
		  SET username = COALESCE(EXCLUDED.username, users.username),
		      updated_at = now()
		RETURNING id, telegram_id, COALESCE(username,''), grade,
		          COALESCE(preferred_lang,''), agreed_at,
		          personal_supergroup_chat_id, personal_supergroup_linked_at,
		          bot_is_admin_in_supergroup, status, anonymized,
		          created_at, updated_at`
	var u User
	if err := s.pool.QueryRow(ctx, q, telegramID, username, preferredLang).Scan(
		&u.ID, &u.TelegramID, &u.Username, &u.Grade,
		&u.PreferredLang, &u.AgreedAt,
		&u.PersonalSupergroupID, &u.PersonalLinkedAt,
		&u.BotIsAdmin, &u.Status, &u.Anonymized,
		&u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return User{}, fmt.Errorf("registry: upsert user: %w", err)
	}
	return u, nil
}

func (s *UserStore) GetByTelegramID(ctx context.Context, telegramID int64) (User, error) {
	const q = `
		SELECT id, telegram_id, COALESCE(username,''), grade,
		       COALESCE(preferred_lang,''), agreed_at,
		       personal_supergroup_chat_id, personal_supergroup_linked_at,
		       bot_is_admin_in_supergroup, status, anonymized,
		       created_at, updated_at
		FROM users WHERE telegram_id = $1`
	var u User
	err := s.pool.QueryRow(ctx, q, telegramID).Scan(
		&u.ID, &u.TelegramID, &u.Username, &u.Grade,
		&u.PreferredLang, &u.AgreedAt,
		&u.PersonalSupergroupID, &u.PersonalLinkedAt,
		&u.BotIsAdmin, &u.Status, &u.Anonymized,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("registry: get user: %w", err)
	}
	return u, nil
}

// MarkAgreed sets agreed_at=now() if not yet set. Returns true on the
// transition (NULL -> now), false if the user had already agreed.
func (s *UserStore) MarkAgreed(ctx context.Context, userID int64) (bool, error) {
	const q = `
		UPDATE users
		SET agreed_at = now(), updated_at = now()
		WHERE id = $1 AND agreed_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return false, fmt.Errorf("registry: mark agreed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Anonymize implements /leave-all: NULL out PII columns, mark anonymized
// + status=anonymized. Caller is responsible for surrounding audit row.
func (s *UserStore) Anonymize(ctx context.Context, userID int64) error {
	const q = `
		UPDATE users
		SET username = NULL,
		    preferred_lang = NULL,
		    personal_supergroup_chat_id = NULL,
		    personal_supergroup_linked_at = NULL,
		    bot_is_admin_in_supergroup = false,
		    anonymized = true,
		    status = 'anonymized',
		    updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("registry: anonymize: %w", err)
	}
	return nil
}

// SetStatus transitions the user lifecycle (active / paused / anonymized).
// Refuses to move a user out of 'anonymized' since that is a one-way state.
func (s *UserStore) SetStatus(ctx context.Context, userID int64, status string) error {
	if status != "active" && status != "paused" {
		return fmt.Errorf("registry: invalid status %q", status)
	}
	const q = `
		UPDATE users SET status = $1, updated_at = now()
		WHERE id = $2 AND status <> 'anonymized'`
	_, err := s.pool.Exec(ctx, q, status, userID)
	if err != nil {
		return fmt.Errorf("registry: set status: %w", err)
	}
	return nil
}
