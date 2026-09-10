package transit

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type serviceQueryDeadline struct {
	deadline time.Time
	set      bool
	err      error
}

type serviceQueryDeadlineKey struct{}

type serviceDeadlineTracer struct {
	queries chan serviceQueryDeadline
}

func (t *serviceDeadlineTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	deadline, set := ctx.Deadline()
	return context.WithValue(ctx, serviceQueryDeadlineKey{}, serviceQueryDeadline{deadline: deadline, set: set})
}

func (t *serviceDeadlineTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	query := ctx.Value(serviceQueryDeadlineKey{}).(serviceQueryDeadline)
	query.err = data.Err
	t.queries <- query
}

func TestServiceQueryDeadlinesDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		CREATE TABLE transit.route (route_id text PRIMARY KEY, short_name text, long_name text, color text, text_color text);
		CREATE TABLE transit.route_pattern (pattern_id text PRIMARY KEY, route_id text);
		CREATE TABLE transit.route_pattern_stop (pattern_id text, stop_id text, sequence int, is_timepoint boolean);
		ALTER TABLE transit.stop ADD COLUMN latitude real, ADD COLUMN longitude real,
			ADD COLUMN is_transfer boolean, ADD COLUMN is_terminal boolean;
		CREATE TABLE transit.stop_visit (trip_id text, stop_id text, route_id text, observed_at timestamptz);
	`)
	tracer := &serviceDeadlineTracer{queries: make(chan serviceQueryDeadline, 1)}
	config := db.Config()
	config.ConnConfig.Tracer = tracer
	tracedDB, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tracedDB.Close)
	svc := NewService(tracedDB, nil)
	for _, reader := range []struct {
		name string
		read func(context.Context)
	}{
		{"route metadata", func(ctx context.Context) { svc.RouteMeta(ctx) }},
		{"stops", func(ctx context.Context) { svc.AllStops(ctx) }},
		{"stop analytics", func(ctx context.Context) { svc.StopAnalytics(ctx) }},
	} {
		t.Run(reader.name, func(t *testing.T) {
			// Observe the context passed to a real query: an HTTP request has
			// no deadline by default, while a caller may supply a shorter one.
			start := time.Now()
			reader.read(context.Background())
			query := <-tracer.queries
			if query.err != nil {
				t.Fatal(query.err)
			}
			if !query.set || query.deadline.Before(start.Add(30*time.Second)) || query.deadline.After(time.Now().Add(30*time.Second)) {
				t.Errorf("query deadline = %v (set=%v), want a 30s bound", query.deadline, query.set)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			want, _ := ctx.Deadline()
			reader.read(ctx)
			query = <-tracer.queries
			if query.err != nil {
				t.Fatal(query.err)
			}
			if !query.set || !query.deadline.Equal(want) {
				t.Errorf("query deadline = %v (set=%v), want caller deadline %v", query.deadline, query.set, want)
			}
		})
	}
}
