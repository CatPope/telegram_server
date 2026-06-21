package strategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const ResolveCodeGradeInsufficient = "grade_insufficient"

type TopicResolver interface {
	ResolveTopic(ctx context.Context, appID, minGrade string) (ResolveResult, error)
}

type TopicStrategy struct {
	Resolver TopicResolver
}

func (s *TopicStrategy) Name() string { return "topic" }

func (s *TopicStrategy) Resolve(ctx context.Context, r Request) (ResolveResult, error) {
	if r.AppID == "" {
		return ResolveResult{}, ErrAppNotFound
	}
	return s.Resolver.ResolveTopic(ctx, r.AppID, normalizeGrade(r.MinGrade))
}

type PgTopicResolver struct {
	pool *pgxpool.Pool
}

func NewPgTopicResolver(pool *pgxpool.Pool) *PgTopicResolver {
	return &PgTopicResolver{pool: pool}
}

func (r *PgTopicResolver) ResolveTopic(ctx context.Context, appID, requestMinGrade string) (ResolveResult, error) {
	var (
		exists      bool
		appMinGrade string
	)
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM apps WHERE id=$1 AND active=true),
		        COALESCE((SELECT min_grade FROM apps WHERE id=$1), 'user')`,
		appID).Scan(&exists, &appMinGrade); err != nil {
		return ResolveResult{}, fmt.Errorf("topic resolver: app lookup: %w", err)
	}
	if !exists {
		return ResolveResult{}, ErrAppNotFound
	}
	effectiveGrade := maxGrade(appMinGrade, requestMinGrade)
	const q = `
		SELECT
			u.id,
			u.grade,
			u.personal_supergroup_chat_id,
			u.bot_is_admin_in_supergroup,
			t.telegram_topic_id
		FROM user_subscriptions s
		JOIN users u ON u.id = s.user_id AND u.status = 'active'
		LEFT JOIN user_topics t ON t.user_id = u.id AND t.app_id = s.app_id
		WHERE s.app_id = $1
		ORDER BY u.id`
	rows, err := r.pool.Query(ctx, q, appID)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("topic resolver: query: %w", err)
	}
	defer rows.Close()
	res := ResolveResult{}
	for rows.Next() {
		var (
			userID         int64
			grade          string
			personalChatID *int64
			botIsAdmin     bool
			topicID        *int64
		)
		if scanErr := rows.Scan(&userID, &grade, &personalChatID, &botIsAdmin, &topicID); scanErr != nil {
			return ResolveResult{}, fmt.Errorf("topic resolver: scan: %w", scanErr)
		}
		if gradeRank(grade) < gradeRank(effectiveGrade) {
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
		if topicID == nil {
			res.Skipped = append(res.Skipped, ResolveError{UserID: userID, Code: ResolveCodeTopicMissing})
			continue
		}
		res.Recipients = append(res.Recipients, RecipientHandle{
			UserID:  userID,
			ChatID:  *personalChatID,
			TopicID: *topicID,
			Channel: "supergroup",
		})
	}
	if err := rows.Err(); err != nil {
		return ResolveResult{}, fmt.Errorf("topic resolver: rows: %w", err)
	}
	return res, nil
}

// gradeRank ranks known grades. Unknown / empty input returns 0 to enforce
// fail-closed comparison: a row whose stored grade is empty or unrecognized
// cannot satisfy a request minGrade of 'user'/'developer'/'admin'.
// normalizeGrade still maps an empty request-side input to 'user', so
// request.MinGrade="" yields rank 1 via normalize; only DB-side empties
// rank as 0.
func gradeRank(g string) int {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "admin":
		return 3
	case "developer", "dev":
		return 2
	case "user":
		return 1
	default:
		return 0
	}
}

func normalizeGrade(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	switch g {
	case "dev":
		return "developer"
	case "":
		return "user"
	default:
		return g
	}
}

func maxGrade(a, b string) string {
	if gradeRank(a) >= gradeRank(b) {
		return a
	}
	return b
}

// GradeRankExported is the package-public form of gradeRank, used by bot
// handlers (apps catalogue) to mirror the same fail-closed semantics as
// the dispatch path.
func GradeRankExported(g string) int { return gradeRank(g) }
