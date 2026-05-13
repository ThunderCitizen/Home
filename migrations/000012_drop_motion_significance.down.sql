ALTER TABLE council_motions ADD COLUMN IF NOT EXISTS significance text DEFAULT 'routine'::text NOT NULL;
ALTER TABLE council_motions ADD COLUMN IF NOT EXISTS llm_significance text DEFAULT ''::text NOT NULL;
