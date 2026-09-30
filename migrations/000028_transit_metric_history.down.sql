-- Restoring the old key would discard later service days. Fail atomically;
-- restore a pre-migration database backup when rolling back the recorder.
DO $$ BEGIN
    RAISE EXCEPTION 'Migration 28 preserves cross-day visits and archived timetables. Restore a pre-migration backup to roll back safely.';
END $$;
