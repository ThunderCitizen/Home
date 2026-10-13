-- A durable close journal. No raw records are removed by this migration.
CREATE TABLE transit.archive_month (
    month date PRIMARY KEY CHECK (extract(day FROM month)=1),
    state text NOT NULL CHECK (state IN ('preparing','sealed','frozen','uploaded','published','complete')),
    metric_version int NOT NULL,
    attempts int NOT NULL DEFAULT 0,
    started_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    sealed_at timestamptz,
    frozen_at timestamptz,
    raw_prefix text,
    raw_manifest jsonb,
    raw_verified_at timestamptz,
    report_base_url text,
    report_manifest jsonb,
    published_at timestamptz,
    completed_at timestamptz,
    last_error text,
    CHECK (state NOT IN ('uploaded','published','complete') OR
        (raw_prefix IS NOT NULL AND raw_manifest IS NOT NULL AND raw_verified_at IS NOT NULL
         AND report_base_url IS NOT NULL AND report_manifest IS NOT NULL))
);

-- Writers and the closer share a month lock. Sealing waits for already-running
-- recorder transactions; subsequent stale feed echoes are ignored, not queued.
CREATE FUNCTION transit.guard_archive_month() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_data jsonb;
    day date;
    mon date;
    status text;
BEGIN
    row_data := CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;
    IF TG_ARGV[0]='timestamp' THEN
        day := (((row_data->>TG_ARGV[1])::timestamptz AT TIME ZONE 'America/Thunder_Bay') - interval '4 hours')::date;
    ELSIF TG_ARGV[0]='compact' THEN
        -- Invalid legacy dates remain in the default partition for inspection.
        IF row_data->>TG_ARGV[1] !~ '^\d{8}$' THEN RETURN NEW; END IF;
        BEGIN
            day := to_date(row_data->>TG_ARGV[1], 'YYYYMMDD');
        EXCEPTION WHEN OTHERS THEN RETURN NEW;
        END;
    ELSE
        day := (row_data->>TG_ARGV[1])::date;
    END IF;
    mon := date_trunc('month',day)::date;
    IF TG_ARGV[2]='derived' THEN
        -- Rebuild readers hold a shared lock on another pooled connection.
        -- Do not queue behind a closer waiting for that reader to finish.
        IF NOT pg_try_advisory_xact_lock_shared(23010,extract(year FROM mon)::int*12+extract(month FROM mon)::int) THEN
            RAISE EXCEPTION 'month % is closing; retry rebuild',mon USING ERRCODE='40001';
        END IF;
    ELSE
        PERFORM pg_advisory_xact_lock_shared(23010,extract(year FROM mon)::int*12+extract(month FROM mon)::int);
    END IF;
    SELECT state INTO status FROM transit.archive_month WHERE month=mon;
    IF TG_ARGV[2]='raw' AND status IN ('sealed','frozen','uploaded','published','complete') THEN
        RETURN NULL;
    END IF;
    IF TG_ARGV[2]='derived' AND status IN ('frozen','uploaded','published','complete') THEN
        RAISE EXCEPTION 'month % is frozen for archival; restore into an analysis schema to rebuild',mon;
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.vehicle_position
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('timestamp','feed_timestamp','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.alert
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('timestamp','feed_timestamp','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.cancellation
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('compact','start_date','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.stop_visit
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','service_date','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.stop_visit_duplicate_archive
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','service_date','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.stop_delay
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.vehicle_assignment
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','raw');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.metric_passage
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','derived');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.route_band_chunk
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','derived');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.route_day_summary
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','derived');
CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.metric_rebuild
 FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','derived');
