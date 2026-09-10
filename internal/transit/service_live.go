package transit

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// Live loads the dashboard from the database for this request.
func (s *Service) Live(ctx context.Context) (*liveData, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Read independent inputs in parallel. The cancellation loader returns
	// both trip details and incidents in one pass through today's schedule.
	var (
		alerts    []ActiveAlert
		fleetSize int
		details   map[string][]CancelledTrip
		incidents []CancelIncident
		noSvc     []string
	)
	today := ServiceDate()
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		v, err := s.reporter.repo.CurrentAlerts(gctx)
		if err == nil {
			alerts = v
		}
		return nil
	})
	g.Go(func() error {
		v, _ := s.reporter.repo.FleetSize(gctx)
		fleetSize = v
		return nil
	})
	g.Go(func() error {
		d, i, err := LoadLiveCancellations(gctx, s.db, today)
		if err != nil {
			return fmt.Errorf("cancellations: %w", err)
		}
		details = d
		incidents = i
		return nil
	})
	g.Go(func() error {
		v, err := NoServiceRoutes(gctx, s.db, today)
		if err != nil {
			return fmt.Errorf("no-service routes: %w", err)
		}
		noSvc = v
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if details == nil {
		details = make(map[string][]CancelledTrip)
	}
	// Fallback for the (rare) case where the schedule-walk found
	// no streaks but the details map has trips — surface them as
	// single-trip incidents so the live panel isn't blank when
	// CancelledTrips isn't.
	if len(incidents) == 0 && len(details) > 0 {
		incidents = IncidentsFromDetails(details)
	}
	return &liveData{
		dashboard: &DashboardReport{
			Alerts:         alerts,
			CancelledTrips: details,
			FleetSize:      fleetSize,
		},
		incidents: incidents,
		noService: noSvc,
	}, nil
}
