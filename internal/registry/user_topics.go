package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserTopic struct {
	UserID    int64
	AppID     string
	TopicID   int64
	CreatedAt string
}

var ErrTopicNotFound = errors.New("registry: user topic not found")

type UserTopicStore struct {
	pool *pgxpool.Pool
}

func NewUserTopicStore(pool *pgxpool.Pool) *UserTopicStore {
	return &UserTopicStore{pool: pool}
}

// Add records that the bot created (or rediscovered) a forum topic for
// (user_id, app_id) → telegram_topic_id. Conflict on (user_id, app_id) is
// treated as a no-op (idempotent against my_chat_member retries).
func (s *UserTopicStore) Add(ctx context.Context, userID int64, appID string, topicID int64) error {
	const q = `
		INSERT INTO user_topics (user_id, app_id, telegram_topic_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, app_id) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, userID, appID, topicID)
	if err != nil {
		return fmt.Errorf("registry: add user topic: %w", err)
	}
	return nil
}

// Remove deletes the (user_id, app_id) mapping. Caller should call
// telego.CloseForumTopic on the Telegram side separately so the topic
// remains archived in the user's supergroup.
func (s *UserTopicStore) Remove(ctx context.Context, userID int64, appID string) error {
	const q = `DELETE FROM user_topics WHERE user_id = $1 AND app_id = $2`
	_, err := s.pool.Exec(ctx, q, userID, appID)
	if err != nil {
		return fmt.Errorf("registry: remove user topic: %w", err)
	}
	return nil
}

func (s *UserTopicStore) GetTopicID(ctx context.Context, userID int64, appID string) (int64, error) {
	const q = `SELECT telegram_topic_id FROM user_topics WHERE user_id = $1 AND app_id = $2`
	var id int64
	err := s.pool.QueryRow(ctx, q, userID, appID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrTopicNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("registry: get topic id: %w", err)
	}
	return id, nil
}

func (s *UserTopicStore) ListForUser(ctx context.Context, userID int64) ([]UserTopic, error) {
	const q = `
		SELECT user_id, app_id, telegram_topic_id, COALESCE(created_at::text,'')
		FROM user_topics WHERE user_id = $1 ORDER BY app_id`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("registry: list topics: %w", err)
	}
	defer rows.Close()
	var out []UserTopic
	for rows.Next() {
		var t UserTopic
		if scanErr := rows.Scan(&t.UserID, &t.AppID, &t.TopicID, &t.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("registry: scan topic: %w", scanErr)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListSubscribedAppsWithoutTopic finds apps the user is subscribed to but
// does not yet have a forum topic for. Used after the user grants the bot
// admin permissions so we can provision topics in one pass.
func (s *UserTopicStore) ListSubscribedAppsWithoutTopic(ctx context.Context, userID int64) ([]string, error) {
	const q = `
		SELECT s.app_id
		FROM user_subscriptions s
		LEFT JOIN user_topics t ON t.user_id = s.user_id AND t.app_id = s.app_id
		WHERE s.user_id = $1 AND t.app_id IS NULL
		ORDER BY s.app_id`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("registry: list missing topics: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if scanErr := rows.Scan(&a); scanErr != nil {
			return nil, fmt.Errorf("registry: scan missing topic: %w", scanErr)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
