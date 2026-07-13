package strategy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ResolveCodeUnknownRecipient   = "unknown_recipient"
	ResolveCodeRecipientInactive  = "recipient_inactive"
	ResolveCodeRecipientNotSubbed = "recipient_not_subscribed"
	ResolveCodeRecipientNotLinked = "recipient_not_linked"
	ResolveCodeTopicMissing       = "recipient_topic_missing"
	ResolveCodeBotNotAdmin        = "bot_not_admin"
)

type DirectResolver interface {
	ResolveDirect(ctx context.Context, userIDs []int64, appID string) (ResolveResult, error)
}

type DirectStrategy struct {
	Resolver DirectResolver
}

func (s *DirectStrategy) Name() string { return "direct" }

func (s *DirectStrategy) Resolve(ctx context.Context, r Request) (ResolveResult, error) {
	if r.AppID == "" {
		return ResolveResult{}, ErrAppNotFound
	}
	if len(r.Recipients) == 0 {
		return ResolveResult{}, ErrEmptyRecipients
	}
	return s.Resolver.ResolveDirect(ctx, r.Recipients, r.AppID)
}

type PgDirectResolver struct {
	pool *pgxpool.Pool
}

func NewPgDirectResolver(pool *pgxpool.Pool) *PgDirectResolver {
	return &PgDirectResolver{pool: pool}
}

func (r *PgDirectResolver) ResolveDirect(ctx context.Context, userIDs []int64, appID string) (ResolveResult, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM apps WHERE id=$1 AND active=true)`, appID).Scan(&exists); err != nil {
		return ResolveResult{}, fmt.Errorf("direct resolver: app lookup: %w", err)
	}
	if !exists {
		return ResolveResult{}, ErrAppNotFound
	}
	const q = `
		SELECT
			u.id,
			u.status,
			u.personal_supergroup_chat_id,
			u.bot_is_admin_in_supergroup,
			s.user_id IS NOT NULL AS subscribed,
			t.telegram_topic_id
		FROM unnest($1::bigint[]) AS req(user_id)
		LEFT JOIN users u ON u.id = req.user_id
		LEFT JOIN user_subscriptions s ON s.user_id = u.id AND s.app_id = $2
		LEFT JOIN user_topics t ON t.user_id = u.id AND t.app_id = $2`
	rows, err := r.pool.Query(ctx, q, userIDs, appID)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("direct resolver: query: %w", err)
	}
	defer rows.Close()
	seen := map[int64]struct{}{}
	res := ResolveResult{}
	idx := 0
	for rows.Next() {
		var (
			userID         *int64
			status         *string
			personalChatID *int64
			botIsAdmin     *bool
			subscribed     bool
			topicID        *int64
		)
		if scanErr := rows.Scan(&userID, &status, &personalChatID, &botIsAdmin, &subscribed, &topicID); scanErr != nil {
			return ResolveResult{}, fmt.Errorf("direct resolver: scan: %w", scanErr)
		}
		requested := userIDs[idx]
		idx++
		if userID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: requested, Code: ResolveCodeUnknownRecipient})
			continue
		}
		if _, dup := seen[*userID]; dup {
			continue
		}
		seen[*userID] = struct{}{}
		if status != nil && *status != "active" {
			res.Skipped = append(res.Skipped, ResolveError{UserID: *userID, Code: ResolveCodeRecipientInactive})
			continue
		}
		if !subscribed {
			res.Skipped = append(res.Skipped, ResolveError{UserID: *userID, Code: ResolveCodeRecipientNotSubbed})
			continue
		}
		if personalChatID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: *userID, Code: ResolveCodeRecipientNotLinked})
			continue
		}
		if botIsAdmin == nil || !*botIsAdmin {
			res.Skipped = append(res.Skipped, ResolveError{UserID: *userID, Code: ResolveCodeBotNotAdmin})
			continue
		}
		if topicID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: *userID, Code: ResolveCodeTopicMissing})
			continue
		}
		res.Recipients = append(res.Recipients, RecipientHandle{
			UserID:  *userID,
			ChatID:  *personalChatID,
			TopicID: *topicID,
			Channel: "supergroup",
		})
	}
	if err := rows.Err(); err != nil {
		return ResolveResult{}, fmt.Errorf("direct resolver: rows: %w", err)
	}
	return res, nil
}
