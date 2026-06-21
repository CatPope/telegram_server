package strategy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BroadcastResolver interface {
	ResolveBroadcast(ctx context.Context, minGrade string) (ResolveResult, error)
}

type BroadcastAllStrategy struct {
	Resolver BroadcastResolver
}

func (s *BroadcastAllStrategy) Name() string { return "broadcast-all" }

func (s *BroadcastAllStrategy) Resolve(ctx context.Context, r Request) (ResolveResult, error) {
	return s.Resolver.ResolveBroadcast(ctx, normalizeGrade(r.MinGrade))
}

type PgBroadcastResolver struct {
	pool *pgxpool.Pool
}

func NewPgBroadcastResolver(pool *pgxpool.Pool) *PgBroadcastResolver {
	return &PgBroadcastResolver{pool: pool}
}

func (r *PgBroadcastResolver) ResolveBroadcast(ctx context.Context, minGrade string) (ResolveResult, error) {
	const q = `
		SELECT id, grade, personal_supergroup_chat_id, bot_is_admin_in_supergroup
		FROM users
		WHERE status = 'active'
		ORDER BY id`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("broadcast resolver: query: %w", err)
	}
	defer rows.Close()
	res := ResolveResult{}
	for rows.Next() {
		var (
			userID         int64
			grade          string
			personalChatID *int64
			botIsAdmin     bool
		)
		if scanErr := rows.Scan(&userID, &grade, &personalChatID, &botIsAdmin); scanErr != nil {
			return ResolveResult{}, fmt.Errorf("broadcast resolver: scan: %w", scanErr)
		}
		if gradeRank(grade) < gradeRank(minGrade) {
			res.Skipped = append(res.Skipped, ResolveError{UserID: userID, Code: ResolveCodeGradeInsufficient})
			continue
		}
		if personalChatID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: userID, Code: ResolveCodeRecipientNotLinked})
			continue
		}
		if !botIsAdmin {
			res.Skipped = append(res.Skipped, ResolveError{UserID: userID, Code: ResolveCodeBotNotAdmin})
			continue
		}
		res.Recipients = append(res.Recipients, RecipientHandle{
			UserID:  userID,
			ChatID:  *personalChatID,
			TopicID: 0,
			Channel: "general",
		})
	}
	if err := rows.Err(); err != nil {
		return ResolveResult{}, fmt.Errorf("broadcast resolver: rows: %w", err)
	}
	return res, nil
}
