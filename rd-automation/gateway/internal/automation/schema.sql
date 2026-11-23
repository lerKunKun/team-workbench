-- Executed under the app role in a transaction with a migration lock (version 1).
CREATE TABLE app.user_identity_mapping (
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 64),
    dingtalk_user_id text PRIMARY KEY,
    linear_user_id text UNIQUE,
    github_user_id text UNIQUE,
    modules text NOT NULL DEFAULT '',
    role text NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin')),
    active boolean NOT NULL DEFAULT true
);

CREATE TABLE app.integration_event (
    source text NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, event_id)
);

CREATE TABLE app.ai_task_draft (
    draft_id uuid PRIMARY KEY,
    card_track_id text UNIQUE NOT NULL,
    conversation_id text NOT NULL,
    creator_dingtalk_id text NOT NULL,
    summary text NOT NULL,
    tasks jsonb NOT NULL CHECK (jsonb_typeof(tasks) = 'array'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','confirmed','cancelled')),
    delivered boolean NOT NULL DEFAULT false,
    card_updated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.linear_issue (
    issue_id uuid PRIMARY KEY,
    draft_id uuid NOT NULL REFERENCES app.ai_task_draft(draft_id) ON DELETE CASCADE,
    task_index integer NOT NULL,
    identifier text NOT NULL,
    url text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (draft_id, task_index)
);

CREATE TABLE app.outbox (
    job_id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('intake','action','notify')),
    payload jsonb NOT NULL,
    run_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    lease_id uuid,
    lease_until timestamptz,
    done_at timestamptz,
    failed_at timestamptz,
    last_error text
);
CREATE INDEX app_outbox_pending ON app.outbox (run_at) WHERE done_at IS NULL AND failed_at IS NULL;
