-- Retained route/day statistics. No raw event table is scanned or removed.
CREATE TABLE transit.route_day_summary (
    route_id text NOT NULL,
    date date NOT NULL,
    metric_version int NOT NULL,
    trip_count bigint NOT NULL,
    scheduled_count bigint NOT NULL,
    cancelled_count bigint NOT NULL,
    no_notice_count bigint NOT NULL,
    -- Only bands with observations/cancellations enter the rate denominator.
    cancellation_scheduled_count bigint NOT NULL,
    otp_count bigint NOT NULL,
    otp_on_time bigint NOT NULL,
    early_count bigint NOT NULL,
    late_count bigint NOT NULL,
    expected_timepoints bigint NOT NULL,
    observed_timepoints bigint NOT NULL,
    eligible_windows bigint NOT NULL,
    total_windows bigint NOT NULL,
    wait_observed_area float8 NOT NULL,
    wait_scheduled_area float8 NOT NULL,
    window_seconds float8 NOT NULL,
    cv_weighted_sum float8 NOT NULL,
    cv_weight float8 NOT NULL,
    schedule_id text,
    built_at timestamptz NOT NULL,
    is_final boolean NOT NULL,
    PRIMARY KEY (route_id, date)
);
CREATE INDEX route_day_summary_date ON transit.route_day_summary(date, route_id);

-- Called in the same transaction as daily chunks and rebuild provenance.
-- Also repairs summaries entirely from retained chunks after raw archival.
CREATE FUNCTION transit.refresh_route_day_summary(day date) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(23002, day - date '2000-01-01');
    DELETE FROM transit.route_day_summary WHERE date = day;
    INSERT INTO transit.route_day_summary (
        route_id,date,metric_version,trip_count,scheduled_count,cancelled_count,no_notice_count,
        cancellation_scheduled_count,otp_count,otp_on_time,early_count,late_count,
        expected_timepoints,observed_timepoints,eligible_windows,total_windows,
        wait_observed_area,wait_scheduled_area,window_seconds,cv_weighted_sum,cv_weight,
        schedule_id,built_at,is_final
    )
    SELECT c.route_id,c.date,
        CASE WHEN min(c.metric_version)=max(c.metric_version) THEN min(c.metric_version) ELSE 0 END,
        sum(c.trip_count),sum(c.scheduled_count),sum(c.cancelled_count),sum(c.no_notice_count),
        coalesce(sum(c.scheduled_count) FILTER (WHERE c.trip_count>0 OR c.cancelled_count>0),0),
        sum(c.otp_count),sum(c.otp_on_time),sum(c.early_count),sum(c.late_count),
        sum(c.expected_timepoints),sum(c.observed_timepoints),sum(c.eligible_windows),sum(c.total_windows),
        sum(c.wait_observed_area),sum(c.wait_scheduled_area),sum(c.window_seconds),sum(c.cv_weighted_sum),sum(c.cv_weight),
        b.schedule_id,max(c.built_at),
        coalesce(min(c.metric_version)>0 AND min(c.metric_version)=max(c.metric_version)
            AND b.version=min(c.metric_version)
            AND b.built_at >= (c.date + interval '1 day 4 hours') AT TIME ZONE 'America/Thunder_Bay'
            AND b.built_at >= max(c.built_at), false)
    FROM transit.route_band_chunk c
    LEFT JOIN transit.metric_rebuild b ON b.date=c.date
    WHERE c.date=day
    GROUP BY c.route_id,c.date,b.schedule_id,b.version,b.built_at;
END;
$$;

-- Existing history is tiny at route/band granularity; populate without GPS,
-- stop delays, cancellation events or a metric-recipe rebuild.
DO $$
DECLARE d date;
BEGIN
    FOR d IN SELECT DISTINCT date FROM transit.route_band_chunk ORDER BY date LOOP
        PERFORM transit.refresh_route_day_summary(d);
    END LOOP;
END;
$$;
