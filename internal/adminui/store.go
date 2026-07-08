package adminui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// Dashboard aggregates (Phase A5 UI). All read-only.
	DashboardStats(ctx context.Context) (DashboardStats, error)
	RequestSeries(ctx context.Context, days int) ([]AppDayCount, error)
	ActiveKeyCounts(ctx context.Context) ([]AppKeyCount, error)
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

// AppKeyCount is the number of active (non-revoked) keys an app holds.
type AppKeyCount struct {
	AppID string
	Count int
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

func (s *pgStore) ActiveKeyCounts(ctx context.Context) ([]AppKeyCount, error) {
	const q = `
		SELECT app_id, count(*)
		FROM app_keys
		WHERE revoked_at IS NULL
		GROUP BY app_id
		ORDER BY app_id`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("adminui: key counts: %w", err)
	}
	defer rows.Close()

	var counts []AppKeyCount
	for rows.Next() {
		var c AppKeyCount
		if scanErr := rows.Scan(&c.AppID, &c.Count); scanErr != nil {
			return nil, fmt.Errorf("adminui: scan key count: %w", scanErr)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminui: rows: %w", err)
	}
	return counts, nil
}
