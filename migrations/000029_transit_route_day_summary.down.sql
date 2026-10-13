-- This is a derived projection; the retained band summaries remain intact.
DROP FUNCTION transit.refresh_route_day_summary(date);
DROP TABLE transit.route_day_summary;
