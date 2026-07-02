-- Capital budgets are project/program records, not annual operating
-- service flows. Keep project identity stable across fiscal years so the
-- app can show approval, funding, procurement, and delivery history over
-- time.

CREATE TABLE public.capital_projects (
    id text NOT NULL,
    name text NOT NULL,
    official_name text DEFAULT ''::text NOT NULL,
    service text NOT NULL,
    category text NOT NULL,
    asset_type text DEFAULT ''::text NOT NULL,
    action text DEFAULT ''::text NOT NULL,
    lifecycle text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'planned'::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    benefits text DEFAULT ''::text NOT NULL,
    ward text DEFAULT ''::text NOT NULL,
    location text DEFAULT ''::text NOT NULL,
    source_context text DEFAULT ''::text NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_page integer,
    source_hash text NOT NULL,
    source text DEFAULT 'manual'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT capital_projects_status_check CHECK ((status = ANY (ARRAY['planned'::text, 'approved'::text, 'active'::text, 'complete'::text, 'deferred'::text, 'cancelled'::text])))
);

ALTER TABLE ONLY public.capital_projects
    ADD CONSTRAINT capital_projects_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.capital_projects
    ADD CONSTRAINT capital_projects_source_hash_key UNIQUE (source_hash);

CREATE TABLE public.capital_project_years (
    project_id text NOT NULL,
    fiscal_year integer NOT NULL,
    amount numeric(14,2) NOT NULL,
    budget_status text DEFAULT 'approved'::text NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_page integer,
    source text DEFAULT 'manual'::text NOT NULL,
    CONSTRAINT capital_project_years_amount_check CHECK ((amount >= (0)::numeric)),
    CONSTRAINT capital_project_years_budget_status_check CHECK ((budget_status = ANY (ARRAY['proposed'::text, 'approved'::text, 'forecast'::text, 'amended'::text])))
);

ALTER TABLE ONLY public.capital_project_years
    ADD CONSTRAINT capital_project_years_pkey PRIMARY KEY (project_id, fiscal_year);

CREATE TABLE public.capital_project_funding (
    project_id text NOT NULL,
    fiscal_year integer NOT NULL,
    funding_source text NOT NULL,
    funding_kind text DEFAULT 'other'::text NOT NULL,
    amount numeric(14,2) NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_page integer,
    source text DEFAULT 'manual'::text NOT NULL,
    CONSTRAINT capital_project_funding_amount_check CHECK ((amount >= (0)::numeric)),
    CONSTRAINT capital_project_funding_kind_check CHECK ((funding_kind = ANY (ARRAY['tax_levy'::text, 'grant'::text, 'reserve'::text, 'debenture'::text, 'internal_loan'::text, 'developer'::text, 'rate'::text, 'other'::text])))
);

ALTER TABLE ONLY public.capital_project_funding
    ADD CONSTRAINT capital_project_funding_pkey PRIMARY KEY (project_id, fiscal_year, funding_source);

CREATE TABLE public.capital_project_stakeholders (
    project_id text NOT NULL,
    name text NOT NULL,
    role text NOT NULL,
    organization text DEFAULT ''::text NOT NULL,
    stakeholder_type text DEFAULT 'internal'::text NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_page integer,
    source text DEFAULT 'manual'::text NOT NULL,
    CONSTRAINT capital_project_stakeholders_type_check CHECK ((stakeholder_type = ANY (ARRAY['internal'::text, 'agency'::text, 'vendor'::text, 'funder'::text, 'council'::text, 'public'::text, 'other'::text])))
);

ALTER TABLE ONLY public.capital_project_stakeholders
    ADD CONSTRAINT capital_project_stakeholders_pkey PRIMARY KEY (project_id, name, role);

CREATE TABLE public.capital_project_approvals (
    approval_id text NOT NULL,
    project_id text,
    fiscal_year integer,
    approval_stage text NOT NULL,
    approval_date date,
    approval_body text DEFAULT 'City Council'::text NOT NULL,
    meeting_id text,
    motion_id bigint,
    result text DEFAULT ''::text NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_page integer,
    source_hash text NOT NULL,
    source text DEFAULT 'manual'::text NOT NULL
);

ALTER TABLE ONLY public.capital_project_approvals
    ADD CONSTRAINT capital_project_approvals_pkey PRIMARY KEY (approval_id);

ALTER TABLE ONLY public.capital_project_approvals
    ADD CONSTRAINT capital_project_approvals_source_hash_key UNIQUE (source_hash);

CREATE TABLE public.capital_project_procurements (
    procurement_id text NOT NULL,
    project_id text NOT NULL,
    title text NOT NULL,
    procurement_type text DEFAULT ''::text NOT NULL,
    stage text DEFAULT ''::text NOT NULL,
    posted_at date,
    closed_at date,
    awarded_at date,
    awarded_vendor text DEFAULT ''::text NOT NULL,
    award_amount numeric(14,2),
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_hash text NOT NULL,
    source text DEFAULT 'manual'::text NOT NULL,
    CONSTRAINT capital_project_procurements_award_amount_check CHECK ((award_amount IS NULL) OR (award_amount >= (0)::numeric))
);

ALTER TABLE ONLY public.capital_project_procurements
    ADD CONSTRAINT capital_project_procurements_pkey PRIMARY KEY (procurement_id);

ALTER TABLE ONLY public.capital_project_procurements
    ADD CONSTRAINT capital_project_procurements_source_hash_key UNIQUE (source_hash);

CREATE TABLE public.capital_project_bids (
    bid_id text NOT NULL,
    procurement_id text NOT NULL,
    bidder text NOT NULL,
    bid_amount numeric(14,2),
    result text DEFAULT ''::text NOT NULL,
    source_url text DEFAULT ''::text NOT NULL,
    source_doc text DEFAULT ''::text NOT NULL,
    source_hash text NOT NULL,
    source text DEFAULT 'manual'::text NOT NULL,
    CONSTRAINT capital_project_bids_amount_check CHECK ((bid_amount IS NULL) OR (bid_amount >= (0)::numeric))
);

ALTER TABLE ONLY public.capital_project_bids
    ADD CONSTRAINT capital_project_bids_pkey PRIMARY KEY (bid_id);

ALTER TABLE ONLY public.capital_project_bids
    ADD CONSTRAINT capital_project_bids_source_hash_key UNIQUE (source_hash);

CREATE INDEX idx_capital_projects_service ON public.capital_projects USING btree (service);
CREATE INDEX idx_capital_projects_status ON public.capital_projects USING btree (status);
CREATE INDEX idx_capital_projects_patch_source ON public.capital_projects USING btree (source) WHERE (source <> 'manual'::text);
CREATE INDEX idx_capital_project_years_year ON public.capital_project_years USING btree (fiscal_year);
CREATE INDEX idx_capital_project_funding_year_kind ON public.capital_project_funding USING btree (fiscal_year, funding_kind);
CREATE INDEX idx_capital_project_approvals_meeting ON public.capital_project_approvals USING btree (meeting_id);
CREATE INDEX idx_capital_project_procurements_project ON public.capital_project_procurements USING btree (project_id);
CREATE INDEX idx_capital_project_bids_procurement ON public.capital_project_bids USING btree (procurement_id);

ALTER TABLE ONLY public.capital_project_years
    ADD CONSTRAINT capital_project_years_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.capital_projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.capital_project_funding
    ADD CONSTRAINT capital_project_funding_project_year_fkey FOREIGN KEY (project_id, fiscal_year) REFERENCES public.capital_project_years(project_id, fiscal_year) ON DELETE CASCADE;

ALTER TABLE ONLY public.capital_project_stakeholders
    ADD CONSTRAINT capital_project_stakeholders_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.capital_projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.capital_project_approvals
    ADD CONSTRAINT capital_project_approvals_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.capital_projects(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.capital_project_approvals
    ADD CONSTRAINT capital_project_approvals_meeting_id_fkey FOREIGN KEY (meeting_id) REFERENCES public.council_meetings(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.capital_project_approvals
    ADD CONSTRAINT capital_project_approvals_motion_id_fkey FOREIGN KEY (motion_id) REFERENCES public.council_motions(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.capital_project_procurements
    ADD CONSTRAINT capital_project_procurements_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.capital_projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.capital_project_bids
    ADD CONSTRAINT capital_project_bids_procurement_id_fkey FOREIGN KEY (procurement_id) REFERENCES public.capital_project_procurements(procurement_id) ON DELETE CASCADE;
