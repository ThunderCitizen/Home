CREATE TABLE transit_prepare.raw_partition (table_name text NOT NULL, month date NOT NULL, partition_name text NOT NULL UNIQUE, PRIMARY KEY(table_name,month));
DO $$
DECLARE
    item record; idx record; fk record; trg record;
    old_name text; key_name text; lower_month date; upper_month date; m date;
    lower_bound text; upper_bound text; cols text; max_id bigint;
BEGIN
    FOR item IN SELECT * FROM (VALUES
        ('vehicle_position','feed_timestamp',true),
        ('alert','feed_timestamp',true),
        ('stop_delay','date',false),
        ('vehicle_assignment','date',false),
        ('metric_passage','date',false),
        ('stop_visit','service_date',false),
        ('stop_visit_duplicate_archive','service_date',false),
        ('cancellation','start_date',false)
    ) AS t(table_name, key_column, timestamp_key) LOOP
        old_name := item.table_name;
        -- Copy columns/defaults/identity/checks, rebuilding indexes separately to
        -- widen identity primary keys with the partition column.
        EXECUTE format('CREATE TABLE transit_prepare.%I (LIKE transit.%I INCLUDING DEFAULTS INCLUDING IDENTITY INCLUDING CONSTRAINTS INCLUDING STORAGE) PARTITION BY RANGE (%I)', item.table_name, old_name, item.key_column);
        FOR idx IN SELECT c.contype, pg_get_constraintdef(c.oid) AS definition
            FROM pg_constraint c WHERE c.conrelid=format('transit.%I',old_name)::regclass AND c.contype IN ('p','u','f') LOOP
            IF item.table_name='cancellation' AND idx.contype='p' THEN
                CONTINUE;
            ELSIF item.table_name='cancellation' AND idx.contype='u' THEN
                EXECUTE 'ALTER TABLE transit_prepare.cancellation ADD UNIQUE NULLS NOT DISTINCT (trip_id,feed_timestamp,start_date)';
            ELSIF idx.contype='p' AND item.timestamp_key THEN
                EXECUTE format('ALTER TABLE transit_prepare.%I ADD PRIMARY KEY (id,feed_timestamp)',item.table_name);
            ELSE
                EXECUTE format('ALTER TABLE transit_prepare.%I ADD %s',item.table_name,idx.definition);
            END IF;
        END LOOP;
        FOR idx IN SELECT indexdef FROM pg_indexes p WHERE p.schemaname='transit' AND p.tablename=old_name
            AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid=to_regclass(format('%I.%I',p.schemaname,p.indexname))) LOOP
            -- Let PostgreSQL choose an unused index name; retain methods,
            -- predicates, included columns and storage parameters verbatim.
            EXECUTE format('CREATE INDEX ON transit_prepare.%I %s',item.table_name,substring(idx.indexdef FROM 'USING .*$'));
        END LOOP;
        IF item.table_name='cancellation' THEN
            EXECUTE format('SELECT min(to_date(start_date,''YYYYMMDD'')),max(to_date(start_date,''YYYYMMDD'')) FROM transit.%I WHERE start_date ~ ''^[0-9]{8}$'' AND pg_input_is_valid(start_date,''date'')',old_name) INTO lower_month,upper_month;
            lower_month:=date_trunc('month',lower_month)::date; upper_month:=date_trunc('month',upper_month)::date;
        ELSIF item.timestamp_key THEN
            EXECUTE format('SELECT date_trunc(''month'',(min(%I) AT TIME ZONE ''America/Thunder_Bay'')-interval ''4 hours'')::date, date_trunc(''month'',(max(%I) AT TIME ZONE ''America/Thunder_Bay'')-interval ''4 hours'')::date FROM transit.%I',item.key_column,item.key_column,old_name) INTO lower_month,upper_month;
        ELSE
            EXECUTE format('SELECT date_trunc(''month'',min(%I))::date,date_trunc(''month'',max(%I))::date FROM transit.%I',item.key_column,item.key_column,old_name) INTO lower_month,upper_month;
        END IF;
        m := date_trunc('month',(now() AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date;
        lower_month := least(coalesce(lower_month,m),m);
        upper_month := greatest(coalesce(upper_month,m), (m+interval '1 month')::date);
        m := lower_month;
        WHILE m <= upper_month LOOP
            IF item.timestamp_key THEN
                lower_bound := ((m+time '04:00') AT TIME ZONE 'America/Thunder_Bay')::text;
                upper_bound := (((m+interval '1 month')::date+time '04:00') AT TIME ZONE 'America/Thunder_Bay')::text;
            ELSIF item.table_name='cancellation' THEN
                lower_bound:=to_char(m,'YYYYMMDD');upper_bound:=to_char(m+interval '1 month','YYYYMMDD');
            ELSE
                lower_bound:=m::text; upper_bound:=(m+interval '1 month')::date::text;
            END IF;
            EXECUTE format('CREATE TABLE transit_prepare.%I PARTITION OF transit_prepare.%I FOR VALUES FROM (%L) TO (%L)', item.table_name||'_p'||to_char(m,'YYYYMM'),item.table_name,lower_bound,upper_bound);
            INSERT INTO transit_prepare.raw_partition VALUES(item.table_name,m,item.table_name||'_p'||to_char(m,'YYYYMM'));
            m:=(m+interval '1 month')::date;
        END LOOP;
        EXECUTE format('CREATE TABLE transit_prepare.%I PARTITION OF transit_prepare.%I DEFAULT',item.table_name||'_unassigned',item.table_name);
        IF item.table_name='stop_visit' THEN
            ALTER TABLE transit_prepare.stop_visit ALTER COLUMN service_date DROP EXPRESSION IF EXISTS;
        END IF;
    END LOOP;
END $$;

-- Preserve efficient identity-based delta reconciliation after the old id-only
-- primary key is replaced with a date-aware cancellation uniqueness constraint.
CREATE INDEX ON transit_prepare.cancellation(id);

-- These private copies are bulk-loaded and analyzed explicitly before handoff.
-- Autovacuum must not hold a leaf lock when the short schema swap starts.
DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='transit_prepare' AND c.relkind='r' LOOP
  EXECUTE format('ALTER TABLE transit_prepare.%I SET (autovacuum_enabled=false)',r.relname);
 END LOOP;
END $$;
