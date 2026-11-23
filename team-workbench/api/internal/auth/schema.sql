CREATE SCHEMA IF NOT EXISTS wb;
CREATE TABLE IF NOT EXISTS wb.account (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), username text UNIQUE NOT NULL, name text NOT NULL,
 role text NOT NULL CHECK(role IN ('admin','member')), password_hash bytea NOT NULL, salt bytea NOT NULL,
 active boolean NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS wb.membership (
 account_id uuid REFERENCES wb.account ON DELETE CASCADE, project_id uuid NOT NULL,
 role text NOT NULL CHECK(role IN ('member','manager')), PRIMARY KEY (account_id,project_id)
);
CREATE TABLE IF NOT EXISTS wb.session (
 token_hash bytea PRIMARY KEY, account_id uuid NOT NULL REFERENCES wb.account ON DELETE CASCADE, expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS wb_session_account ON wb.session(account_id);
CREATE TABLE IF NOT EXISTS wb.audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, actor text NOT NULL, action text NOT NULL,
 target text NOT NULL, happened_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS wb.ding_identity (
 corp_id text, identity_kind text CHECK(identity_kind IN ('staff','union')), external_id text,
 account_id uuid NOT NULL REFERENCES wb.account ON DELETE CASCADE,
 PRIMARY KEY(corp_id,identity_kind,external_id), UNIQUE(corp_id,identity_kind,account_id)
);
CREATE TABLE IF NOT EXISTS wb.ding_state (
 token_hash bytea PRIMARY KEY, next_path text NOT NULL, expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS wb_ding_state_exp ON wb.ding_state(expires_at);
