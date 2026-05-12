CREATE INDEX IF NOT EXISTS idx_transit_cancellation_start_date
  ON transit.cancellation (start_date, trip_id)
  INCLUDE (route_id, start_time, feed_timestamp);
