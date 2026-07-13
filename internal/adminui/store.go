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
	CreatedBy    string // registering requester's app id; "" for pre-0008 rows
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
	// RequestBuckets is the requests chart series: 'received' counts per
	// (app, bucket) over the spec's window. The bucket unit varies with the
	// dashboard's 일/주/월/연 toggle (hourly / daily / monthly).
	RequestBuckets(ctx context.Context, spec SeriesSpec) ([]AppDayCount, error)
	DeliveryKPICounts(ctx context.Context) (KPICounts, error)

	// Dashboard diagnostics (운영 재구성 v2). Both roll over the same 24h
	// window as DeliveryKPICounts, so the headline KPI and these two cards
	// always describe the same slice of time. PipelineStageCounts feeds the
	// system-wide funnel (어디서 막히나); FailureCauseCounts feeds the
	// error_code distribution (왜 실패하나).
	PipelineStageCounts(ctx context.Context) ([]StageCount, error)
	FailureCauseCounts(ctx context.Context) ([]ErrorCodeCount, error)

	// DeliveryLatency is the received→delivered elapsed-time summary over
	// messages delivered in the last 24h (얼마나 빠른가). Anchored on the
	// delivered event, not received, so a message received before the window
	// but delivered inside it still counts — see the query for the boundary,
	// retry, and population semantics.
	DeliveryLatency(ctx context.Context) (LatencyStats, error)

	// LatencySamples returns the individual received→delivered latencies
	// behind DeliveryLatency (with the delivering app), newest delivery
	// first, capped at limit — the dashboard's strip plot draws each
	// completed trace as a dot whose tooltip names the app.
	LatencySamples(ctx context.Context, limit int) ([]LatencySample, error)

	// Delivery status page aggregates. StageCounts feeds the per-app
	// funnel (appID "" = all apps); RecentFailures lists the newest
	// failure-stage rows matching the page filters; DeliveryDailyCounts
	// feeds the filtered per-day trend chart; FailureErrorCodes fills the
	// error-code filter dropdown from what actually occurred in the window.
	StageCounts(ctx context.Context, days int, appID string) ([]AppStageCount, error)
	RecentFailures(ctx context.Context, f FailureFilter) ([]FailureRow, error)
	DeliveryDailyCounts(ctx context.Context, f TrendFilter) ([]StageDayCount, error)
	FailureErrorCodes(ctx context.Context, days int) ([]string, error)

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

// AppDayCount is one point of the requests series: how many audit_log
// 'received' events an app produced in a bucket (hour, day, or month,
// depending on the SeriesSpec).
type AppDayCount struct {
	AppID string
	Day   time.Time
	Count int
}

// SeriesSpec describes the requests-chart window: Unit is the bucket size
// ("hour", "day", or "month" — anything else is rejected) and Buckets is
// how many consecutive buckets ending now the query covers.
type SeriesSpec struct {
	Unit    string
	Buckets int
}

// FailureFilter scopes RecentFailures. Zero-value strings mean "no filter";
// Days and Limit are always applied.
type FailureFilter struct {
	Days      int
	Limit     int
	AppID     string
	Stage     string
	ErrorCode string
}

// TrendFilter scopes DeliveryDailyCounts: the day window plus the delivery
// page's optional app/stage/error-code filters ("" = all).
type TrendFilter struct {
	Days      int
	AppID     string
	Stage     string
	ErrorCode string
}

// StageDayCount is one point of the delivery page's trend chart: how many
// audit_log events landed in a stage on a given (UTC) day.
type StageDayCount struct {
	Day   time.Time
	Stage string
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

// LatencyStats summarizes received→delivered elapsed time (in seconds) over
// the traces delivered in the last 24h. Count is the sample size — the
// number of completed traces the percentiles are computed from, NOT all
// traffic (in-flight and failed traces have no delivered event and are
// excluded). Zero Count means nothing completed in the window.
type LatencyStats struct {
	Count int
	P50   float64 // seconds
	P95   float64 // seconds
	Max   float64 // seconds
}

// LatencySample is one completed trace on the dashboard strip plot: its
// received→delivered elapsed time and the app that delivered it ("" when
// the delivered row carried no app_id).
type LatencySample struct {
	AppID string
	Secs  float64
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
	       a.created_at, a.created_by
	FROM apps a
	LEFT JOIN app_capabilities c ON c.app_id = a.id
	GROUP BY a.id, a.name, a.description, a.min_grade, a.active, a.created_at, a.created_by
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
		if scanErr := rows.Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities, &a.CreatedAt, &a.CreatedBy); scanErr != nil {
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
		       a.created_at, a.created_by
		FROM apps a
		LEFT JOIN app_capabilities c ON c.app_id = a.id
		WHERE a.id = $1
		GROUP BY a.id, a.name, a.description, a.min_grade, a.active, a.created_at, a.created_by`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var a App
	err := s.pool.QueryRow(ctx, q, id).Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities, &a.CreatedAt, &a.CreatedBy)
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

// seriesUnitSQL whitelists SeriesSpec.Unit → the SQL fragments interpolated
// into the RequestBuckets query. The map is the only source of those
// fragments, so no user input ever reaches the SQL text.
var seriesUnitSQL = map[string]struct {
	trunc    string // date_trunc unit
	interval string // bucket-stepping interval
}{
	"hour":  {"hour", "interval '1 hour'"},
	"day":   {"day", "interval '1 day'"},
	"month": {"month", "interval '1 month'"},
}

func (s *pgStore) RequestBuckets(ctx context.Context, spec SeriesSpec) ([]AppDayCount, error) {
	// Buckets are UTC end to end: the chart axis in dashboard.go is built
	// from time.Now().UTC(), so bucketing here must not depend on the DB
	// session timezone or edge-of-window buckets would misalign.
	unit, ok := seriesUnitSQL[spec.Unit]
	if !ok {
		return nil, fmt.Errorf("adminui: request buckets: unknown unit %q", spec.Unit)
	}
	q := fmt.Sprintf(`
		SELECT app_id, date_trunc('%s', at AT TIME ZONE 'UTC') AS bucket, count(*)
		FROM audit_log
		WHERE stage = 'received' AND app_id IS NOT NULL
		  AND at >= (date_trunc('%s', now() AT TIME ZONE 'UTC') - ($1 - 1) * %s) AT TIME ZONE 'UTC'
		GROUP BY app_id, bucket
		ORDER BY app_id, bucket`, unit.trunc, unit.trunc, unit.interval)

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, spec.Buckets)
	if err != nil {
		return nil, fmt.Errorf("adminui: request buckets: %w", err)
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

func (s *pgStore) StageCounts(ctx context.Context, days int, appID string) ([]AppStageCount, error) {
	// A rolling window (now() - N days) rather than day bucketing, so no
	// timezone alignment is needed — unlike RequestBuckets there is no
	// bucket axis to match on the Go side. appID '' disables the app
	// filter ($3 short-circuit) — one prepared shape for both cases.
	const q = `
		SELECT app_id, stage, count(*)
		FROM audit_log
		WHERE app_id IS NOT NULL
		  AND stage = ANY($1)
		  AND at >= now() - $2 * interval '1 day'
		  AND ($3 = '' OR app_id = $3)
		GROUP BY app_id, stage
		ORDER BY app_id, stage`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFunnelStages, days, appID)
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

func (s *pgStore) RecentFailures(ctx context.Context, f FailureFilter) ([]FailureRow, error) {
	// ORDER BY id alone: BIGSERIAL is insertion order, so the PK index
	// satisfies the sort without materializing every failure row. The
	// optional filters use the same ''-short-circuit shape as StageCounts.
	// f.Stage narrows within the failure-stage set — a non-failure stage
	// simply matches nothing.
	const q = `
		SELECT at, stage, COALESCE(app_id, ''), recipient_user_id,
		       recipient_chat_id, COALESCE(error_code, ''), COALESCE(trace_id, '')
		FROM audit_log
		WHERE stage = ANY($1) AND at >= now() - $2 * interval '1 day'
		  AND ($4 = '' OR app_id = $4)
		  AND ($5 = '' OR stage = $5)
		  AND ($6 = '' OR COALESCE(NULLIF(error_code, ''), 'unknown') = $6)
		ORDER BY id DESC
		LIMIT $3`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFailureStages, f.Days, f.Limit, f.AppID, f.Stage, f.ErrorCode)
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

func (s *pgStore) DeliveryLatency(ctx context.Context) (LatencyStats, error) {
	// Elapsed time from a trace's first 'received' to its last 'delivered'.
	// Design decisions (each a correctness call, not a cost one):
	//   - Anchor on delivered in the 24h window; look back 25h for received
	//     (1h slack) so a message whose received fell just outside the 24h
	//     window is still paired — real relay latency is far under an hour,
	//     so the slack captures every boundary case without an unbounded
	//     full-history scan of received rows (both windows stay index-backed).
	//   - min(received)→max(delivered): caller-visible completion. There is
	//     one received per trace; multiple delivered rows come from
	//     multi-recipient fan-out (one delivered per recipient), so max is
	//     the time until the LAST recipient succeeded. Rate-limit retries
	//     happen before the delivered write, so they're already included.
	//   - Population is delivered traces only. In-flight and failed traces
	//     have no delivered event and are excluded — the percentiles describe
	//     successful deliveries, a different denominator than the KPI counts.
	//   - delivered_at >= received_at guards against a clock-skewed pair
	//     producing a negative latency.
	const q = `
		WITH deliv AS (
			SELECT trace_id, max(at) AS delivered_at
			FROM audit_log
			WHERE stage = 'delivered' AND trace_id IS NOT NULL
			  AND at >= now() - interval '24 hours'
			GROUP BY trace_id
		),
		recv AS (
			SELECT trace_id, min(at) AS received_at
			FROM audit_log
			WHERE stage = 'received' AND trace_id IS NOT NULL
			  AND at >= now() - interval '25 hours'
			GROUP BY trace_id
		),
		lat AS (
			SELECT EXTRACT(EPOCH FROM (d.delivered_at - r.received_at)) AS secs
			FROM deliv d JOIN recv r USING (trace_id)
			WHERE d.delivered_at >= r.received_at
		)
		SELECT count(*),
		       COALESCE(percentile_cont(0.5)  WITHIN GROUP (ORDER BY secs), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY secs), 0),
		       COALESCE(max(secs), 0)
		FROM lat`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var st LatencyStats
	if err := s.pool.QueryRow(ctx, q).Scan(&st.Count, &st.P50, &st.P95, &st.Max); err != nil {
		return LatencyStats{}, fmt.Errorf("adminui: delivery latency: %w", err)
	}
	return st, nil
}

func (s *pgStore) LatencySamples(ctx context.Context, limit int) ([]LatencySample, error) {
	// Same pairing semantics as DeliveryLatency (see the rationale there);
	// this returns the individual per-trace values instead of percentiles,
	// newest delivery first so the cap keeps the most recent traces.
	// DISTINCT ON keeps the newest delivered row per trace so its app_id
	// rides along (GROUP BY max(at) would lose the row's app).
	const q = `
		WITH deliv AS (
			SELECT DISTINCT ON (trace_id)
			       trace_id, at AS delivered_at, COALESCE(app_id, '') AS app_id
			FROM audit_log
			WHERE stage = 'delivered' AND trace_id IS NOT NULL
			  AND at >= now() - interval '24 hours'
			ORDER BY trace_id, at DESC
		),
		recv AS (
			SELECT trace_id, min(at) AS received_at
			FROM audit_log
			WHERE stage = 'received' AND trace_id IS NOT NULL
			  AND at >= now() - interval '25 hours'
			GROUP BY trace_id
		)
		SELECT d.app_id, EXTRACT(EPOCH FROM (d.delivered_at - r.received_at)) AS secs
		FROM deliv d JOIN recv r USING (trace_id)
		WHERE d.delivered_at >= r.received_at
		ORDER BY d.delivered_at DESC
		LIMIT $1`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("adminui: latency samples: %w", err)
	}
	defer rows.Close()

	var samples []LatencySample
	for rows.Next() {
		var sm LatencySample
		if scanErr := rows.Scan(&sm.AppID, &sm.Secs); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan latency sample: %w", scanErr)
		}
		samples = append(samples, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return samples, nil
}

func (s *pgStore) DeliveryDailyCounts(ctx context.Context, f TrendFilter) ([]StageDayCount, error) {
	// UTC day buckets (like RequestBuckets) so the Go-side axis built from
	// time.Now().UTC() lines up. Counts both pipeline and failure stages so
	// the trend chart can show flow and failures on one axis; the optional
	// filters use the same ''-short-circuit shape as StageCounts.
	const q = `
		SELECT date_trunc('day', at AT TIME ZONE 'UTC') AS day, stage, count(*)
		FROM audit_log
		WHERE (stage = ANY($1) OR stage = ANY($2))
		  AND at >= (date_trunc('day', now() AT TIME ZONE 'UTC') - ($3 - 1) * interval '1 day') AT TIME ZONE 'UTC'
		  AND ($4 = '' OR app_id = $4)
		  AND ($5 = '' OR stage = $5)
		  AND ($6 = '' OR COALESCE(NULLIF(error_code, ''), 'unknown') = $6)
		GROUP BY day, stage
		ORDER BY day, stage`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFunnelStages, deliveryFailureStages, f.Days, f.AppID, f.Stage, f.ErrorCode)
	if err != nil {
		return nil, fmt.Errorf("adminui: delivery daily counts: %w", err)
	}
	defer rows.Close()

	var counts []StageDayCount
	for rows.Next() {
		var c StageDayCount
		if scanErr := rows.Scan(&c.Day, &c.Stage, &c.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan daily count: %w", scanErr)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return counts, nil
}

func (s *pgStore) FailureErrorCodes(ctx context.Context, days int) ([]string, error) {
	// Distinct error codes seen on failure stages in the window, for the
	// filter dropdown — same 'unknown' collapse as FailureCauseCounts so
	// the dropdown values match what the page displays.
	const q = `
		SELECT DISTINCT COALESCE(NULLIF(error_code, ''), 'unknown') AS code
		FROM audit_log
		WHERE stage = ANY($1) AND at >= now() - $2 * interval '1 day'
		ORDER BY code`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q, deliveryFailureStages, days)
	if err != nil {
		return nil, fmt.Errorf("adminui: failure error codes: %w", err)
	}
	defer rows.Close()

	var codes []string
	for rows.Next() {
		var code string
		if scanErr := rows.Scan(&code); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan error code: %w", scanErr)
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return codes, nil
}
