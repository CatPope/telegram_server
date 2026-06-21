package strategy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DirectDMResolver interface {
	ResolveDirectDM(ctx context.Context, telegramIDs []int64) (ResolveResult, error)
}

type DirectDMStrategy struct {
	Resolver DirectDMResolver
}

func (s *DirectDMStrategy) Name() string { return "direct-dm" }

func (s *DirectDMStrategy) Resolve(ctx context.Context, r Request) (ResolveResult, error) {
	if len(r.Recipients) == 0 {
		return ResolveResult{}, ErrEmptyRecipients
	}
	return s.Resolver.ResolveDirectDM(ctx, r.Recipients)
}

type PgDirectDMResolver struct {
	pool *pgxpool.Pool
}

func NewPgDirectDMResolver(pool *pgxpool.Pool) *PgDirectDMResolver {
	return &PgDirectDMResolver{pool: pool}
}

func (r *PgDirectDMResolver) ResolveDirectDM(ctx context.Context, telegramIDs []int64) (ResolveResult, error) {
	const q = `
		SELECT req.tg_id, u.id, u.telegram_id, u.status
		FROM unnest($1::bigint[]) AS req(tg_id)
		LEFT JOIN users u ON u.telegram_id = req.tg_id`
	rows, err := r.pool.Query(ctx, q, telegramIDs)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("direct-dm resolver: query: %w", err)
	}
	defer rows.Close()
	seen := map[int64]struct{}{}
	res := ResolveResult{}
	for rows.Next() {
		var (
			requestedTGID int64
			userID        *int64
			telegramID    *int64
			status        *string
		)
		if scanErr := rows.Scan(&requestedTGID, &userID, &telegramID, &status); scanErr != nil {
			return ResolveResult{}, fmt.Errorf("direct-dm resolver: scan: %w", scanErr)
		}
		if userID == nil || telegramID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: requestedTGID, Code: ResolveCodeUnknownRecipient})
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
		res.Recipients = append(res.Recipients, RecipientHandle{
			UserID:  *userID,
			ChatID:  *telegramID,
			TopicID: 0,
			Channel: "dm",
		})
	}
	if err := rows.Err(); err != nil {
		return ResolveResult{}, fmt.Errorf("direct-dm resolver: rows: %w", err)
	}
	return res, nil
}
