package bot

import (
	"context"
	"fmt"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/registry"
)

// TopicProvisioner creates and tears down forum topics in the user's
// personal supergroup to mirror user_subscriptions. It is the single owner
// of telego.CreateForumTopic / CloseForumTopic in this codebase so the
// audit logging and idempotency contract stay in one place.
type TopicProvisioner struct {
	Bot        *telego.Bot
	UserTopics *registry.UserTopicStore
}

func NewTopicProvisioner(bot *telego.Bot, userTopics *registry.UserTopicStore) *TopicProvisioner {
	return &TopicProvisioner{Bot: bot, UserTopics: userTopics}
}

// EnsureForSubscribedApps walks the user's subscriptions and creates a
// forum topic for every (user_id, app_id) that does not yet have one.
// Idempotent against retries because UserTopicStore.Add is ON CONFLICT
// DO NOTHING and missing-topic detection comes from a LEFT JOIN.
func (p *TopicProvisioner) EnsureForSubscribedApps(ctx context.Context, userID, chatID int64) ([]string, error) {
	apps, err := p.UserTopics.ListSubscribedAppsWithoutTopic(ctx, userID)
	if err != nil {
		return nil, err
	}
	created := make([]string, 0, len(apps))
	for _, app := range apps {
		topic, err := p.Bot.CreateForumTopic(ctx, &telego.CreateForumTopicParams{
			ChatID: telego.ChatID{ID: chatID},
			Name:   app,
		})
		if err != nil {
			middleware.Log("error", "create_forum_topic_failed", map[string]any{
				"user_id": userID,
				"chat_id": chatID,
				"app_id":  app,
				"error":   err.Error(),
			})
			continue
		}
		if topic == nil {
			continue
		}
		if addErr := p.UserTopics.Add(ctx, userID, app, int64(topic.MessageThreadID)); addErr != nil {
			middleware.Log("error", "user_topic_add_failed", map[string]any{
				"user_id": userID,
				"app_id":  app,
				"error":   addErr.Error(),
			})
			continue
		}
		created = append(created, app)
	}
	return created, nil
}

// Close archives the (user, app) topic on the Telegram side and removes
// the row. Caller (apps handler) controls the order so the row delete
// only happens after the telego call succeeds.
func (p *TopicProvisioner) Close(ctx context.Context, userID, chatID int64, appID string) error {
	topicID, err := p.UserTopics.GetTopicID(ctx, userID, appID)
	if err != nil {
		return fmt.Errorf("provisioner: lookup: %w", err)
	}
	if err := p.Bot.CloseForumTopic(ctx, &telego.CloseForumTopicParams{
		ChatID:          telego.ChatID{ID: chatID},
		MessageThreadID: int(topicID),
	}); err != nil {
		return fmt.Errorf("provisioner: close forum topic: %w", err)
	}
	return p.UserTopics.Remove(ctx, userID, appID)
}
