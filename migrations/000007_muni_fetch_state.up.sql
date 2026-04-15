-- Track when we last checked the upstream muni bundle so boots inside
-- the check window can skip the DO Spaces download entirely.
CREATE TABLE IF NOT EXISTS public.muni_fetch_state (
    id integer PRIMARY KEY DEFAULT 1,
    last_checked_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT muni_fetch_state_singleton CHECK (id = 1)
);
