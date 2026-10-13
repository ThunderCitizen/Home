-- Rollback removes lifecycle guards; it cannot restore already archived raw data.
DROP FUNCTION transit.guard_archive_month() CASCADE;
DROP TABLE transit.archive_month;
