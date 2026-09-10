-- One atomic performance migration. golang-migrate sends a file as one
-- PostgreSQL command, so concurrent index operations cannot be combined here.
CREATE INDEX idx_transit_stop_delay_route_date_trip
    ON transit.stop_delay USING btree (route_id, date, trip_id);
CREATE INDEX idx_transit_cancellation_route_date_trip
    ON transit.cancellation USING btree (route_id, start_date, trip_id);
CREATE INDEX idx_transit_stop_delay_updated
    ON transit.stop_delay USING btree (last_updated);

DROP INDEX IF EXISTS transit.idx_transit_stop_delay_route_stop_date;
DROP INDEX IF EXISTS transit.idx_transit_stop_delay_service_date;
DROP INDEX IF EXISTS transit.idx_transit_stop_delay_first_stop_band;
DROP INDEX IF EXISTS transit.idx_transit_stop_delay_timepoint_band;
DROP INDEX IF EXISTS transit.idx_transit_stop_delay_last_updated;
DROP INDEX IF EXISTS transit.idx_transit_cancellation_route_start;
DROP INDEX IF EXISTS transit.idx_transit_route_pattern_route;
DROP INDEX IF EXISTS gtfs.idx_gtfs_calendar_dates_date;
DROP INDEX IF EXISTS gtfs.idx_gtfs_stop_times_stop;
DROP INDEX IF EXISTS transit.idx_transit_scheduled_stop_first_dep;
DROP INDEX IF EXISTS transit.idx_transit_scheduled_stop_tp_dep;
