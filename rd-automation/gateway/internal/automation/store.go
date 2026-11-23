package automation

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Store struct{ pool *pgxpool.Pool }

type Member struct {
	Name, DingID, LinearID, GitHubID, Modules, Role string
}

type Task struct {
	Category    string   `json:"category"`
	Title       string   `json:"title"`
	Description string   `json:"description_md"`
	Priority    int      `json:"priority"`
	Assignee    string   `json:"assignee_name"`
	AssigneeID  string   `json:"assignee_linear_id,omitempty"`
	Reason      string   `json:"assign_reason"`
	Labels      []string `json:"labels"`
	Missing     []string `json:"missing_info"`
}

type Draft struct {
	ID, TrackID, Group, Creator, Summary, Status string
	Tasks                                        []Task
	Delivered, CardUpdated                       bool
}

type Payload struct {
	Group   string `json:"group,omitempty"`
	Sender  string `json:"sender,omitempty"`
	Text    string `json:"text,omitempty"`
	DraftID string `json:"draft_id,omitempty"`
	Action  string `json:"action,omitempty"`
	Title   string `json:"title,omitempty"`
	Corp    string `json:"corp,omitempty"`
}

type Job struct {
	ID       string  `json:"job_id"`
	Lease    string  `json:"lease_id"`
	Kind     string  `json:"-"`
	Payload  Payload `json:"-"`
	Attempts int     `json:"-"`
}

type Issue struct {
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
}

func OpenStore(ctx context.Context, password string) (*Store, error) {
	u := url.URL{Scheme: "postgres", User: url.UserPassword("rd_app", password), Host: "postgres:5432", Path: "rd_platform", RawQuery: "sslmode=disable"}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		return nil, err
	}
	s := &Store{pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`SELECT pg_advisory_xact_lock(58204041)`,
		`CREATE TABLE IF NOT EXISTS app.schema_migration (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.schema_migration WHERE version=1)`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, schema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.schema_migration VALUES (1)`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS app.workbench_private_card (
		id uuid PRIMARY KEY, corp text NOT NULL, staff text NOT NULL, state text NOT NULL DEFAULT 'pending',
		delivered boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const memberCols = `display_name,dingtalk_user_id,coalesce(linear_user_id,''),coalesce(github_user_id,''),modules,role`

func (s *Store) member(ctx context.Context, id string) (Member, error) {
	var m Member
	err := s.pool.QueryRow(ctx, `SELECT `+memberCols+` FROM app.user_identity_mapping WHERE active AND dingtalk_user_id=$1`, id).
		Scan(&m.Name, &m.DingID, &m.LinearID, &m.GitHubID, &m.Modules, &m.Role)
	return m, err
}

func (s *Store) members(ctx context.Context) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+memberCols+` FROM app.user_identity_mapping WHERE active ORDER BY dingtalk_user_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
}

func enqueue(ctx context.Context, tx pgx.Tx, kind string, p Payload) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.outbox(job_id,kind,payload) VALUES ($1,$2,$3)`, uuid.New(), kind, p)
	return err
}

// Event deduplication and durable delivery happen in the same transaction.
func (s *Store) event(ctx context.Context, source, key, eventType, kind string, p Payload) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO app.integration_event(source,event_id,event_type) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, source, key, eventType)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err := enqueue(ctx, tx, kind, p); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) claim(ctx context.Context, id, track, group, actor, action string, demo bool, groups []string) (bool, error) {
	status := "processing"
	if action == "cancel" {
		status = "cancelled"
	} else if action != "confirm" {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var draftGroup string
	err = tx.QueryRow(ctx, `UPDATE app.ai_task_draft d SET status=$5, updated_at=now()
		WHERE d.draft_id=$1 AND d.card_track_id=$2 AND ($3='' OR d.conversation_id=$3) AND d.conversation_id=ANY($7::text[])
		AND d.status='pending' AND d.delivered
	AND (($6 AND d.creator_dingtalk_id=$4) OR (NOT $6 AND EXISTS (
			SELECT 1 FROM app.user_identity_mapping u WHERE u.dingtalk_user_id=$4 AND u.active
			AND (d.creator_dingtalk_id=$4 OR u.role='admin')))) RETURNING d.conversation_id`, id, track, group, actor, status, demo, groups).Scan(&draftGroup)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := enqueue(ctx, tx, "action", Payload{Group: draftGroup, Sender: actor, DraftID: id, Action: action}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) next(ctx context.Context) (Job, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT job_id FROM app.outbox WHERE done_at IS NULL AND failed_at IS NULL AND run_at<=now()
		AND (lease_until IS NULL OR lease_until<now()) ORDER BY run_at,job_id LIMIT 1 FOR UPDATE SKIP LOCKED)
		UPDATE app.outbox o SET lease_id=$1, lease_until=now()+interval '3 minutes', attempts=attempts+1
		FROM candidate c WHERE o.job_id=c.job_id RETURNING o.job_id::text,o.lease_id::text,o.kind,o.payload,o.attempts`, uuid.New()).
		Scan(&j.ID, &j.Lease, &j.Kind, &j.Payload, &j.Attempts)
	return j, err
}

func (s *Store) activeJob(ctx context.Context, id, lease, kind string) (Job, error) {
	j := Job{ID: id, Lease: lease, Kind: kind}
	err := s.pool.QueryRow(ctx, `SELECT payload,attempts FROM app.outbox
		WHERE job_id=$1 AND lease_id=$2 AND kind=$3 AND lease_until>now() AND done_at IS NULL AND failed_at IS NULL`, id, lease, kind).
		Scan(&j.Payload, &j.Attempts)
	return j, err
}

func (s *Store) finish(ctx context.Context, j Job, problem string) error {
	if problem == "" {
		_, err := s.pool.Exec(ctx, `UPDATE app.outbox SET done_at=now(), lease_until=NULL, lease_id=NULL, payload='{}' WHERE job_id=$1 AND lease_id=$2`, j.ID, j.Lease)
		return err
	}
	if j.Attempts >= 8 {
		_, err := s.pool.Exec(ctx, `UPDATE app.outbox SET failed_at=now(), last_error=$3,lease_until=NULL,lease_id=NULL WHERE job_id=$1 AND lease_id=$2`, j.ID, j.Lease, problem)
		return err
	}
	delay := time.Duration(5*(1<<(j.Attempts-1))) * time.Second
	_, err := s.pool.Exec(ctx, `UPDATE app.outbox SET run_at=$3,last_error=$4,lease_until=NULL,lease_id=NULL WHERE job_id=$1 AND lease_id=$2`, j.ID, j.Lease, time.Now().Add(delay), problem)
	return err
}

func (s *Store) saveDraft(ctx context.Context, d Draft) error {
	b, err := json.Marshal(d.Tasks)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO app.ai_task_draft(draft_id,card_track_id,conversation_id,creator_dingtalk_id,summary,tasks)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (draft_id) DO NOTHING`, d.ID, d.TrackID, d.Group, d.Creator, d.Summary, b)
	return err
}

func (s *Store) draft(ctx context.Context, id string) (Draft, error) {
	var d Draft
	err := s.pool.QueryRow(ctx, `SELECT draft_id::text,card_track_id,conversation_id,creator_dingtalk_id,summary,tasks,status,delivered,card_updated_at IS NOT NULL
		FROM app.ai_task_draft WHERE draft_id=$1`, id).
		Scan(&d.ID, &d.TrackID, &d.Group, &d.Creator, &d.Summary, &d.Tasks, &d.Status, &d.Delivered, &d.CardUpdated)
	return d, err
}

func (s *Store) delivered(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE app.ai_task_draft SET delivered=true,updated_at=now() WHERE draft_id=$1`, id)
	return err
}

func (s *Store) cardUpdated(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE app.ai_task_draft SET card_updated_at=now(),updated_at=now(),
		status=CASE WHEN status='processing' THEN 'confirmed' ELSE status END WHERE draft_id=$1`, id)
	return err
}

func (s *Store) issue(ctx context.Context, id string, index int) (Issue, error) {
	var i Issue
	err := s.pool.QueryRow(ctx, `SELECT identifier,url FROM app.linear_issue WHERE draft_id=$1 AND task_index=$2`, id, index).Scan(&i.Identifier, &i.URL)
	return i, err
}

func (s *Store) saveIssue(ctx context.Context, d, id string, index int, i Issue) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO app.linear_issue(issue_id,draft_id,task_index,identifier,url)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (draft_id,task_index) DO NOTHING`, id, d, index, i.Identifier, i.URL)
	return err
}
