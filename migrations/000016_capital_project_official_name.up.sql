ALTER TABLE public.capital_projects
    ADD COLUMN IF NOT EXISTS official_name text DEFAULT ''::text NOT NULL;

UPDATE public.capital_projects
SET official_name = name
WHERE official_name = '';
