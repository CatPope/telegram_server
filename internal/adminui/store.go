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
	       COALESCE(array_agg(c.capability) FILTER (WHERE c.capability IS NOT NULL), '{}')
	FROM apps a
	LEFT JOIN app_capabilities c ON c.app_id = a.id
	GROUP BY a.id, a.name, a.description, a.min_grade, a.active
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
		if scanErr := rows.Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities); scanErr != nil {
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
		       COALESCE(array_agg(c.capability) FILTER (WHERE c.capability IS NOT NULL), '{}')
		FROM apps a
		LEFT JOIN app_capabilities c ON c.app_id = a.id
		WHERE a.id = $1
		GROUP BY a.id, a.name, a.description, a.min_grade, a.active`

	ctx, cancel := context.WithTimeout(ctx, storeQueryTimeout)
	defer cancel()

	var a App
	err := s.pool.QueryRow(ctx, q, id).Scan(&a.ID, &a.Name, &a.Description, &a.MinGrade, &a.Active, &a.Capabilities)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrAppNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("adminui: get app: %w", err)
	}
	return a, nil
}
