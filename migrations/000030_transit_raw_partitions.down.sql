-- A rollback cannot recreate partitions already archived by the lifecycle.
DO $$ BEGIN RAISE EXCEPTION 'Raw partition migration is irreversible; restore a verified database backup'; END $$;
