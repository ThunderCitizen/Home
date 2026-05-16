ALTER TABLE public.councillors ADD COLUMN IF NOT EXISTS status text DEFAULT ''::text NOT NULL;
