CREATE INDEX idx_transit_stop_delay_route_stop_date
    ON transit.stop_delay USING btree (route_id, stop_id, date);
CREATE INDEX idx_transit_stop_delay_service_date
    ON transit.stop_delay USING btree (service_id, date);
CREATE INDEX idx_transit_stop_delay_first_stop_band
    ON transit.stop_delay USING btree (date, route_id, band) WHERE (is_first_stop = true);
CREATE INDEX idx_transit_stop_delay_timepoint_band
    ON transit.stop_delay USING btree (date, route_id, band) WHERE (is_timepoint = true);
CREATE INDEX idx_transit_stop_delay_last_updated
    ON transit.stop_delay USING brin (last_updated) WITH (pages_per_range='32');
CREATE INDEX idx_transit_cancellation_route_start
    ON transit.cancellation USING btree (feed_timestamp, trip_id, route_id, start_time, start_date) WHERE (start_time IS NOT NULL);
CREATE INDEX idx_transit_route_pattern_route
    ON transit.route_pattern USING btree (route_id);
CREATE INDEX idx_gtfs_calendar_dates_date
    ON gtfs.calendar_dates USING btree (date);
CREATE INDEX idx_gtfs_stop_times_stop
    ON gtfs.stop_times USING btree (stop_id);
CREATE INDEX idx_transit_scheduled_stop_first_dep
    ON transit.scheduled_stop USING btree (route_id, scheduled_departure) WHERE (stop_sequence = 1);
CREATE INDEX idx_transit_scheduled_stop_tp_dep
    ON transit.scheduled_stop USING btree (route_id, stop_id, scheduled_departure) WHERE (is_timepoint = true);

DROP INDEX IF EXISTS transit.idx_transit_stop_delay_route_date_trip;
DROP INDEX IF EXISTS transit.idx_transit_cancellation_route_date_trip;
DROP INDEX IF EXISTS transit.idx_transit_stop_delay_updated;
