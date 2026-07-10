package adminui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/audit"
)

// storeQueryTimeout bounds each read query so a hung DB can't hold page
// handlers (and pool connections) until the server's write timeout.
const storeQueryTimeout = 5 * time.Second

// App is a read-only projection of an app row (plus its granted
// capabilities) used to render the apps list/detail pages. It is never
// used to write — all mutations go through apiclient against /admin.
type App struct {
	ID           string
	Name         string
	Description  string
	MinGrade     string
	Active       bool
	Capabilities []string
	CreatedAt    time.Time
}

// HasCapability reports whether the app currently holds a capability —
// used by app_detail.html to pre-check the capability boxes.
func (a App) HasCapability(c string) bool {
	for _, have := range a.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// ErrAppNotFound is returned by Store.GetApp when no app has the given id.
var ErrAppNotFound = errors.New("adminui: app not found")

// Store is a read-only view over apps/app_capabilities. There is no
// "list apps" endpoint on the /admin API (see admin-ui-plan.md §A2), so the
// apps list/detail pages read the DB directly while every state change
// still goes through apiclient. Modeled as an interface so page handlers
// can be tested against a fake without a real database.
type Store interface {
	ListApps(ctx context.Context) ([]App, error)
	GetApp(ctx context.Context, id string) (App, error)

	// Users list (Phase A5 UI). There is no "list users" /admin API, so —
	// like ListApps — the users page reads the DB directly while every
	// mutation still goes through apiclient.
	ListUsers(ctx context.Context) ([]UserRow, error)

	// Dashboard aggregates (Phase A5 UI, 운영 재구성). All read-only.
	// DeliveryKPICounts is the dashboard's headline row: message flow over
	// the last 24 hours (rolling window, like StageCounts).
	DashboardStats(ctx context.Context) (DashboardStats, error)
	RequestSeries(ctx context.Context, days int) ([]AppDayCount, error)
	DeliveryKPICounts(ctx context.Context) (KPICounts, error)

	// Dashboard diagnostics (운영 재구성 v2). Both roll over the same 24h
	// window as DeliveryKPICounts, so the headline KPI and these two cards
	// always describe the same slice of time. PipelineStageCounts feeds the
	// system-wide funnel (어디서 막히나); FailureCauseCounts feeds the
	// error_code distribution (왜 실패하나).
	PipelineStageCounts(ctx context.Context) ([]StageCount, error)
	FailureCauseCounts(ctx context.Context) ([]ErrorCodeCount, error)

	// Delivery status page aggregates. StageCounts feeds the per-app
	// funnel; RecentFailures lists the newest failure-stage rows within
	// the same window so the 7d/24h toggle applies to both cards.
	StageCounts(ctx context.Context, days int) ([]AppStageCount, error)
	RecentFailures(ctx context.Context, days, limit int) ([]FailureRow, error)

	// VerifyAuditChain walks the audit_log hash chain in id order
	// (read-only). Unlike the other methods it does NOT apply
	// storeQueryTimeout — the caller passes its own, larger deadline, and
	// a deadline error carries the rows verified so far in the result.
	VerifyAuditChain(ctx context.Context) (audit.VerifyResult, error)
}

// UserRow is a read-only projection of a users row plus its subscribed
// app ids, for the users management page.
type UserRow struct {
	TelegramID    int64
	Username      string
	Grade         string
	Subscriptions []string
}

// DashboardStats are the four stat cards on the dashboard.
type DashboardStats struct {
	TotalApps  int
	ActiveApps int
	ActiveKeys int
	Users      int
}

// AppDayCount is one point of the requests-per-day series: how many
// audit_log 'received' events an app produced on a given day.
type AppDayCount struct {
	AppID string
	Day   time.Time
	Count int
}

// KPICounts is the dashboard's last-24h message flow: how many messages
// entered the pipeline, how many reached Telegram, and how many hit a
// failure stage (deliveryFailureStages — same set the delivery page uses).
type KPICounts struct {
	Received  int
	Delivered int
	Failed    int
}

// AppStageCount is one funnel cell: how many audit_log events an app
// produced in a pipeline stage within the delivery page's window.
type AppStageCount struct {
	AppID string
	Stage string
	Count int
}

// StageCount is one cell of the dashboard's system-wide funnel: how many
// audit_log events landed in a pipeline stage across all apps in the 24h
// window. Unlike AppStageCount there is no app dimension — the dashboard
// funnel is the whole relay's flow at a glance.
type StageCount struct {
	Stage string
	Count int
}

// ErrorCodeCount is one bar of the dashboard's failure-cause distribution:
// how many failures in the 24h window carried a given error_code.
type ErrorCodeCount struct {
	Code  string
	Count int
}

// FailureRow is a failure-stage audit_log row for the delivery page's
// recent-failures table. Recipient columns stay nullable pointers, matching
// the audit viewer's treatment of the same columns.
type FailureRow struct {
	At              time.Time
	Stage           string
	AppID           string
	RecipientUserID *int64
	RecipientChatID *int64
	ErrorCode       string
	TraceID         string
}

type pgStore struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store backed by pool. When DATABASE_URL isn't
// configured, cmd/adminui/main.go passes a nil pool here — return a nil
// Store in that case too, so s.store == nil is the one check page
// handlers need for "DB unavailable" rather than dialing.
func NewStore(pool *pgxpool.Pool) Store {
	if pool == nil {
		return nil
	}
	return &pgStore{pool: pool}
}

const listAppsQuery = `
	SELECT a.id, a.name, a.description, a.min_grade, a.active,
	       COALESCE(array_agg(c.capability) FILTER (WHERE c.capability IS NOT NULL), '{}'),
	       a.created_at
	FROM apps a
	LEFT JOIN app_capabilities c ON c.app_id = a.id
	GROUP BY a.id, a.name, a.description, a.min_grade, a.active, a.created_at
	ORDER BY a.id`

func (s *pgStore) ListApps(ctx context.Context) ([]App, error) {
	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, listAppsQuery)
	if err != nil {
		return nil, fmt.Errorf("adminui: list apps: %w", err)
	}
	defer rows.Close()

	var apps []App
	for rows.Next() {
		var a App
		if scanErr := rows.Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities, &a.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan app: %w", scanErr)
		}
		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return apps, nil
}

func (s *pgStore) GetApp(ctx context.Context, id string) (App, error) {
	const q = `
		SELECT a.id, a.name, a.description, a.min_grade, a.active,
		       COALESCE(array_agg(c.capability) FILTER (WHERE c.capability IS NOT NULL), '{}'),
		       a.created_at
		FROM apps a
		LEFT JOIN app_capabilities c ON c.app_id = a.id
		WHERE a.id = $1
		GROUP BY a.id, a.name, a.description, a.min_grade, a.active, a.created_at`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var a App
	err := s.pool.QueryRow(ctx, q, id).Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrAppNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("adminui: get app: %w", err)
	}
	return a, nil
}

func (s *pgStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	const q = `
		SELECT u.telegram_id, COALESCE(u.username, ''), u.grade,
		       COALESCE(array_agg(sub.app_id ORDER BY sub.app_id) FILTER (WHERE sub.app_id IS NOT NULL), '{}')
		FROM users u
		LEFT JOIN user_subscriptions sub ON sub.user_id = u.id
		GROUP BY u.id, u.telegram_id, u.username, u.grade
		ORDER BY u.telegram_id`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("adminui: list users: %w", err)
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var u UserRow
		if scanErr := rows.Scan(&u.TelegramID, &u.Username, &u.Grade, &u.Subscriptions); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan user: %w", scanErr)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return users, nil
}

func (s *pgStore) DashboardStats(ctx context.Context) (DashboardStats, error) {
	const q = `
		SELECT (SELECT count(*) FROM apps),
		       (SELECT count(*) FROM apps WHERE active),
		       (SELECT count(*) FROM app_keys WHERE revoked_at IS NULL),
		       (SELECT count(*) FROM users)`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var st DashboardStats
	if err := s.pool.QueryRow(ctx, q).Scan(&st.TotalApps, &st.ActiveApps, &st.ActiveKeys, &st.Users); err != nil {
		return DashboardStats{}, fmt.Errorf("adminui: dashboard stats: %w", err)
	}
	return st, nil
}

func (s *pgStore) RequestSeries(ctx context.Context, days int) ([]AppDayCount, error) {
	// Buckets are UTC days end to end: the chart axis in dashboard.go is
	// built from time.Now().UTC(), so bucketing here must not depend on
	// the DB session timezone or edge-of-window days would misalign.
	const q = `
		SELECT app_id, (at AT TIME ZONE 'UTC')::date AS day, count(*)
		FROM audit_log
		WHERE stage = 'received' AND app_id IS NOT NULL
		  AND at >= (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') - ($1 - 1) * interval '1 day'
		GROUP BY app_id, day
		ORDER BY app_id, day`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("adminui: request series: %w", err)
	}
	defer rows.Close()

	var series []AppDayCount
	for rows.Next() {
		var p AppDayCount
		if scanErr := rows.Scan(&p.AppID, &p.Day, &p.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan series: %w", scanErr)
		}
		series = append(series, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return series, nil
}

// deliveryFunnelStages is the pipeline the funnel counts. Failure stages
// are excluded here — they feed RecentFailures instead.
var deliveryFunnelStages = []string{
	"received", "validated", "dispatched", "delivered",
	"denied", "retried", "deferred",
}

// deliveryFailureStages are the audit stages that mean a message (or its
// caller) was rejected — what the recent-failures table shows. retried /
// deferred are in-flight states, not failures, so they stay out.
var deliveryFailureStages = []string{
	"denied", "telegram_auth_failed", "bot_not_admin",
	"intrusion_kick", "intrusion_unmitigated",
}

func (s *pgStore) StageCounts(ctx context.Context, days int) ([]AppStageCount, error) {
	// A rolling window (now() - N days) rather than day bucketing, so no
	// timezone alignment is needed — unlike RequestSeries there is no
	// day axis to match on the Go side.
	const q = `
		SELECT app_id, stage, count(*)
		FROM audit_log
		WHERE app_id IS NOT NULL
		  AND stage = ANY($1)
		  AND at >= now() - $2 * interval '1 day'
		GROUP BY app_id, stage
		ORDER BY app_id, stage`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFunnelStages, days)
	if err != nil {
		return nil, fmt.Errorf("adminui: stage counts: %w", err)
	}
	defer rows.Close()

	var counts []AppStageCount
	for rows.Next() {
		var c AppStageCount
		if scanErr := rows.Scan(&c.AppID, &c.Stage, &c.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan stage count: %w", scanErr)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return counts, nil
}

func (s *pgStore) RecentFailures(ctx context.Context, days, limit int) ([]FailureRow, error) {
	// ORDER BY id alone: BIGSERIAL is insertion order, so the PK index
	// satisfies the sort without materializing every failure row.
	const q = `
		SELECT at, stage, COALESCE(app_id, ''), recipient_user_id,
		       recipient_chat_id, COALESCE(error_code, ''), COALESCE(trace_id, '')
		FROM audit_log
		WHERE stage = ANY($1) AND at >= now() - $2 * interval '1 day'
		ORDER BY id DESC
		LIMIT $3`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFailureStages, days, limit)
	if err != nil {
		return nil, fmt.Errorf("adminui: recent failures: %w", err)
	}
	defer rows.Close()

	var failures []FailureRow
	for rows.Next() {
		var f FailureRow
		if scanErr := rows.Scan(&f.At, &f.Stage, &f.AppID, &f.RecipientUserID, &f.RecipientChatID, &f.ErrorCode, &f.TraceID); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan failure: %w", scanErr)
		}
		failures = append(failures, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return failures, nil
}

// verifyChainQuery streams the chain in id order. The canonical payload is
// recomputed by the same audit_chain_payload the writer hashes with
// (migrations/0007), so Go only compares hashes and never re-serializes
// row fields.
const verifyChainQuery = `
	SELECT id, at, stage, prev_hash, row_hash,
	       audit_chain_payload(
	           at, trace_id, message_id, stage, app_id, capability,
	           capability_set_ver, endpoint, route_strategy, delivery_channel,
	           recipient_user_id, recipient_chat_id, error_code, details_json)
	FROM audit_log
	ORDER BY id`

func (s *pgStore) VerifyAuditChain(ctx context.Context) (audit.VerifyResult, error) {
	rows, err := s.pool.Query(ctx, verifyChainQuery)
	if err != nil {
		return audit.VerifyResult{}, fmt.Errorf("adminui: verify chain: %w", err)
	}
	defer rows.Close()

	return audit.VerifyChain(func() (audit.ChainRow, bool, error) {
		if !rows.Next() {
			return audit.ChainRow{}, false, rows.Err()
		}
		var r audit.ChainRow
		if scanErr := rows.Scan(&r.ID, &r.At, &r.Stage, &r.PrevHash, &r.RowHash, &r.Payload); scanErr != nil {
			return audit.ChainRow{}, false, fmt.Errorf("adminui: scan chain row: %w", scanErr)
		}
		return r, true, nil
	})
}

func (s *pgStore) DeliveryKPICounts(ctx context.Context) (KPICounts, error) {
	// One pass over the 24h window; FILTER splits the three counters.
	// Rolling window (no day bucketing), matching StageCounts' 24h mode.
	const q = `
		SELECT count(*) FILTER (WHERE stage = 'received'),
		       count(*) FILTER (WHERE stage = 'delivered'),
		       count(*) FILTER (WHERE stage = ANY($1))
		FROM audit_log
		WHERE at >= now() - interval '24 hours'`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var k KPICounts
	if err := s.pool.QueryRow(ctx, q, deliveryFailureStages).Scan(&k.Received, &k.Delivered, &k.Failed); err != nil {
		return KPICounts{}, fmt.Errorf("adminui: delivery kpi: %w", err)
	}
	return k, nil
}

func (s *pgStore) PipelineStageCounts(ctx context.Context) ([]StageCount, error) {
	// System-wide funnel over the 24h window (matching DeliveryKPICounts).
	// Only the four pipeline stages (funnelStageOrder) are scanned — the
	// dashboard funnel shows flow, not failures. app_id is NOT filtered:
	// this is the whole relay's aggregate, unlike the per-app StageCounts.
	const q = `
		SELECT stage, count(*)
		FROM audit_log
		WHERE stage = ANY($1) AND at >= now() - interval '24 hours'
		GROUP BY stage`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, funnelStageOrder)
	if err != nil {
		return nil, fmt.Errorf("adminui: pipeline stage counts: %w", err)
	}
	defer rows.Close()

	var counts []StageCount
	for rows.Next() {
		var c StageCount
		if scanErr := rows.Scan(&c.Stage, &c.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan pipeline stage: %w", scanErr)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return counts, nil
}

func (s *pgStore) FailureCauseCounts(ctx context.Context) ([]ErrorCodeCount, error) {
	// error_code distribution over the same failure stages RecentFailures
	// uses, 24h window. NULL/empty error_code collapses to 'unknown' so a
	// failure without a code still gets a bar. Ordered count desc for a
	// ranked list; error_code breaks ties for a stable order.
	const q = `
		SELECT COALESCE(NULLIF(error_code, ''), 'unknown') AS code, count(*)
		FROM audit_log
		WHERE stage = ANY($1) AND at >= now() - interval '24 hours'
		GROUP BY code
		ORDER BY count(*) DESC, code`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFailureStages)
	if err != nil {
		return nil, fmt.Errorf("adminui: failure cause counts: %w", err)
	}
	defer rows.Close()

	var counts []ErrorCodeCount
	for rows.Next() {
		var c ErrorCodeCount
		if scanErr := rows.Scan(&c.Code, &c.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan failure cause: %w", scanErr)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return counts, nil
}
