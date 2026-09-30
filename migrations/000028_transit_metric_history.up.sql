-- Retain schedules independently of the mutable GTFS presentation tables.
CREATE TABLE transit.metric_schedule (
    id text PRIMARY KEY,
    source text NOT NULL,
    published_at timestamptz NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE transit.metric_service (
    schedule_id text NOT NULL REFERENCES transit.metric_schedule(id),
    service_id text NOT NULL,
    date date NOT NULL,
    PRIMARY KEY (schedule_id, date, service_id)
);
CREATE INDEX metric_service_date ON transit.metric_service (date, schedule_id);
CREATE TABLE transit.metric_trip (
    schedule_id text NOT NULL REFERENCES transit.metric_schedule(id),
    trip_id text NOT NULL,
    route_id text NOT NULL,
    service_id text NOT NULL,
    direction_id int NOT NULL,
    first_departure int NOT NULL,
    stops jsonb NOT NULL,
    PRIMARY KEY (schedule_id, trip_id)
);
CREATE INDEX metric_trip_route ON transit.metric_trip (schedule_id, route_id, service_id);

-- A trip ID repeats on each service day. Keep existing observations intact.
ALTER TABLE transit.stop_visit ADD COLUMN service_date date
    GENERATED ALWAYS AS (((observed_at AT TIME ZONE 'America/Thunder_Bay') - interval '4 hours')::date) STORED;
-- Some local recovery databases allowed duplicate trip/stop/day rows. Retain
-- those complete records separately instead of deleting historical evidence.
CREATE TABLE transit.stop_visit_duplicate_archive AS SELECT * FROM transit.stop_visit WITH NO DATA;
WITH ranked AS (
    SELECT ctid, row_number() OVER (
        PARTITION BY service_date, trip_id, stop_id ORDER BY observed_at, ctid
    ) AS n FROM transit.stop_visit
), moved AS (
    DELETE FROM transit.stop_visit v USING ranked r
    WHERE v.ctid=r.ctid AND r.n>1 RETURNING v.*
)
INSERT INTO transit.stop_visit_duplicate_archive SELECT * FROM moved;
ALTER TABLE transit.stop_visit DROP CONSTRAINT stop_visit_pkey;
ALTER TABLE transit.stop_visit ADD PRIMARY KEY (service_date, trip_id, stop_id);

-- Reconstructed passages are kept separate from the recorder's observations.
CREATE TABLE transit.metric_passage (
    date date NOT NULL,
    schedule_id text NOT NULL REFERENCES transit.metric_schedule(id),
    detector_version int NOT NULL,
    trip_id text NOT NULL,
    stop_id text NOT NULL,
    observed_at timestamptz NOT NULL,
    source_a bigint NOT NULL,
    source_b bigint NOT NULL,
    PRIMARY KEY (date, trip_id, stop_id)
);
CREATE TABLE transit.metric_rebuild (
    date date PRIMARY KEY,
    version int NOT NULL,
    schedule_id text NOT NULL,
    built_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE transit.vehicle_position
    ADD COLUMN IF NOT EXISTS measurement_timestamp timestamptz,
    ADD COLUMN IF NOT EXISTS trip_start_date text,
    ADD COLUMN IF NOT EXISTS current_stop_sequence int;
ALTER TABLE transit.route_band_chunk
    ADD COLUMN metric_version int NOT NULL DEFAULT 0,
    ADD COLUMN otp_count int NOT NULL DEFAULT 0,
    ADD COLUMN otp_on_time int NOT NULL DEFAULT 0,
    ADD COLUMN early_count int NOT NULL DEFAULT 0,
    ADD COLUMN late_count int NOT NULL DEFAULT 0,
    ADD COLUMN expected_timepoints int NOT NULL DEFAULT 0,
    ADD COLUMN observed_timepoints int NOT NULL DEFAULT 0,
    ADD COLUMN eligible_windows int NOT NULL DEFAULT 0,
    ADD COLUMN total_windows int NOT NULL DEFAULT 0,
    ADD COLUMN wait_observed_area float8 NOT NULL DEFAULT 0,
    ADD COLUMN wait_scheduled_area float8 NOT NULL DEFAULT 0,
    ADD COLUMN window_seconds float8 NOT NULL DEFAULT 0,
    ADD COLUMN cv_weighted_sum float8 NOT NULL DEFAULT 0,
    ADD COLUMN cv_weight float8 NOT NULL DEFAULT 0;
