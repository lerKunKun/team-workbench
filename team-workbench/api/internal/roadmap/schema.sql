CREATE TABLE IF NOT EXISTS wb.roadmap (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL, title text NOT NULL,
 state text NOT NULL DEFAULT 'triage' CHECK(state IN ('triage','evaluating','confirmed','delivered','declined')),
 revision integer NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS wb.feedback (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), item_id uuid NOT NULL REFERENCES wb.roadmap,
 original_item_id uuid NOT NULL REFERENCES wb.roadmap, author text NOT NULL, body text NOT NULL,
 source text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wb_feedback_item ON wb.feedback(item_id);
CREATE TABLE IF NOT EXISTS wb.plan (
 item_id uuid NOT NULL REFERENCES wb.roadmap, version integer NOT NULL, body text NOT NULL,
 source text NOT NULL, author text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(item_id,version)
);
CREATE TABLE IF NOT EXISTS wb.generation (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), item_id uuid NOT NULL REFERENCES wb.roadmap,
 revision integer NOT NULL, actor text NOT NULL, status text NOT NULL DEFAULT 'queued'
 CHECK(status IN ('queued','running','done','failed','stale')),
 tries integer NOT NULL DEFAULT 0, lease_until timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS wb_generation_active ON wb.generation(item_id,revision) WHERE status IN ('queued','running');
