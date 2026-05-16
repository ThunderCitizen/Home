-- Councillor status ("Stepping down", "Not seeking re-election", …) is
-- editorial election context, not the curated council record. It now ships
-- as a code-level overlay in internal/data (rendered straight into the
-- /councillors view model), never through the signed muni bundle. The
-- muni extract no longer emits this column; drop it from the table so the
-- schema mirrors the factual-record-only model. Apply is a tolerant reader,
-- so older signed bundles that still carry a `status` column keep applying.
ALTER TABLE public.councillors DROP COLUMN IF EXISTS status;
