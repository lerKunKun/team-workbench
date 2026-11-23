package projects

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"strings"
	"team-workbench/internal/auth"
	"time"
)

var errWritePending = errors.New("operation outcome needs verification")
var ErrDenied = errors.New("project operation denied")
var errBad = errors.New("invalid project write")

type WriteInput struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Health string `json:"health,omitempty"`
}
type Receipt struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	URL        string `json:"url,omitempty"`
	Status     string `json:"status"`
	Demo       bool   `json:"demo"`
}
type Writer struct {
	DB       *pgxpool.Pool
	Projects Service
}

func RequestKey(actor, op, project, key string) (string, error) {
	if !auth.IsUUID(key) {
		return "", errBad
	}
	h := sha256.Sum256([]byte(actor + "\x00" + op + "\x00" + project + "\x00" + key))
	h[6] = (h[6] & 15) | 64
	h[8] = (h[8] & 63) | 128
	return uuid.UUID(h[:16]).String(), nil
}

func (w Writer) Migrate(ctx context.Context) error {
	_, e := w.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS wb.project_operation (
		id uuid PRIMARY KEY, actor text NOT NULL, project_id uuid, kind text NOT NULL,
		input jsonb NOT NULL, digest bytea NOT NULL, status text NOT NULL, receipt jsonb,
		created_at timestamptz NOT NULL DEFAULT now());`)
	return e
}

func (w Writer) Execute(ctx context.Context, u auth.Principal, project, op, key string, in WriteInput) (Receipt, error) {
	var out Receipt
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	grant := map[string]string{"issue": "project.issue", "update": "project.update", "propose": "project.propose"}[op]
	if grant == "" || !w.Projects.Can(u, grant, project) {
		return out, ErrDenied
	}
	if len(in.Body) > 8000 || in.Body == "" || (op != "update" && (in.Title == "" || len(in.Title) > 320)) || (op == "update" && in.Health != "onTrack" && in.Health != "atRisk" && in.Health != "offTrack") {
		return out, errBad
	}
	id, err := RequestKey(u.ID, op, project, key)
	if err != nil {
		return out, err
	}
	if op != "propose" {
		allowed := false
		ps, err := w.Projects.Visible(ctx, u)
		if err != nil {
			return out, err
		}
		for _, p := range ps {
			if p.ID == project {
				allowed = true
				break
			}
		}
		if !allowed {
			return out, ErrDenied
		}
	}
	data, _ := json.Marshal(in)
	digest := sha256.Sum256(data)
	initial := "started"
	out = Receipt{ID: id, Status: "recorded", Demo: w.Projects.Source != nil && w.Projects.Source.Kind() == "demo"}
	if op == "propose" {
		initial = "pending_approval"
		out.Status = initial
	}
	if out.Demo && op != "propose" {
		initial = "recorded"
		out.Identifier = "DEMO-" + strings.ToUpper(id[:8])
	}
	ctx, done := context.WithTimeout(ctx, 24*time.Second)
	defer done()
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var receipt any
	if initial != "started" {
		receipt = out
	}
	tag, err := tx.Exec(ctx, `INSERT INTO wb.project_operation(id,actor,project_id,kind,input,digest,status,receipt) VALUES($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, id, u.ID, project, op, in, digest[:], initial, receipt)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT digest,receipt FROM wb.project_operation WHERE id=$1`, id).Scan(&stored, &raw)
		if err != nil {
			return out, err
		}
		if string(stored) != string(digest[:]) {
			return out, errBad
		}
		if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
			return out, errWritePending
		}
		return out, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wb.audit(actor,action,target) VALUES ($1,$2,$3)`, u.ID, "project."+op, id); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	if initial != "started" {
		return out, nil
	}
	l, ok := w.Projects.Source.(*Linear)
	if !ok {
		return out, ErrUnavailable
	}
	if op == "issue" {
		out, err = l.addIssue(ctx, id, project, in)
	} else {
		out, err = l.addUpdate(ctx, id, project, in)
	}
	if err != nil {
		return out, errWritePending
	}
	commit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	if _, err = w.DB.Exec(commit, `UPDATE wb.project_operation SET status='recorded',receipt=$2 WHERE id=$1`, id, out); err != nil {
		return out, errWritePending
	}
	return out, nil
}

func (l *Linear) addIssue(ctx context.Context, id, project string, in WriteInput) (Receipt, error) {
	var res struct {
		IssueCreate struct {
			Success bool
			Issue   struct{ Identifier, URL string }
		}
	}
	input := map[string]any{"id": id, "projectId": project, "teamId": l.team, "title": in.Title, "description": in.Body, "useDefaultTemplate": false}
	e := l.query(ctx, `mutation($input:IssueCreateInput!){issueCreate(input:$input){success issue{identifier url}}}`, map[string]any{"input": input}, &res)
	i := res.IssueCreate.Issue
	if e != nil || !res.IssueCreate.Success || i.Identifier == "" || goodURL(i.URL) == "" {
		return Receipt{}, ErrUnavailable
	}
	return Receipt{ID: id, Identifier: i.Identifier, URL: i.URL, Status: "recorded"}, nil
}
func (l *Linear) addUpdate(ctx context.Context, id, project string, in WriteInput) (Receipt, error) {
	var res struct {
		ProjectUpdateCreate struct {
			Success       bool
			ProjectUpdate struct{ ID, URL string }
		}
	}
	e := l.query(ctx, `mutation($input:ProjectUpdateCreateInput!){projectUpdateCreate(input:$input){success projectUpdate{id url}}}`, map[string]any{"input": map[string]string{"projectId": project, "body": in.Body, "health": in.Health}}, &res)
	i := res.ProjectUpdateCreate.ProjectUpdate
	if e != nil || !res.ProjectUpdateCreate.Success || i.ID == "" {
		return Receipt{}, ErrUnavailable
	}
	return Receipt{ID: id, URL: goodURL(i.URL), Status: "recorded"}, nil
}

type Proposal struct {
	ID    string     `json:"id"`
	Input WriteInput `json:"input"`
	State string     `json:"state"`
}

func (w Writer) proposals(ctx context.Context) ([]Proposal, error) {
	r, err := w.DB.Query(ctx, `SELECT id::text,input,status FROM wb.project_operation WHERE kind='propose' ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := []Proposal{}
	for r.Next() {
		var p Proposal
		if err = r.Scan(&p.ID, &p.Input, &p.State); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, r.Err()
}
func (w Writer) approve(ctx context.Context, u auth.Principal, id string) error {
	if !auth.IsUUID(id) {
		return ErrDenied
	}
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state string
	err = tx.QueryRow(ctx, `SELECT status FROM wb.project_operation WHERE kind='propose' AND id=$1 FOR UPDATE`, id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if state == "approved" {
		return nil
	}
	if state != "pending_approval" {
		return errBad
	}
	if _, err = tx.Exec(ctx, `UPDATE wb.project_operation SET status='approved',receipt=jsonb_set(receipt,'{status}','"approved"') WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wb.audit(actor,action,target) VALUES ($1,'project.approve',$2)`, u.ID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (w Writer) Register(m *http.ServeMux, h *auth.Handler) {
	for endpoint, op := range map[string]string{"POST /api/projects/{id}/issues": "issue", "POST /api/projects/{id}/updates": "update", "POST /api/project-proposals": "propose"} {
		m.HandleFunc(endpoint, func(rw http.ResponseWriter, r *http.Request) {
			if !h.SameOrigin(rw, r) {
				return
			}
			u, ok := h.User(rw, r)
			if !ok {
				return
			}
			var in WriteInput
			if !auth.Decode(rw, r, &in) {
				return
			}
			out, err := w.Execute(r.Context(), u, r.PathValue("id"), op, r.Header.Get("Idempotency-Key"), in)
			if err != nil {
				writeFail(rw, err, out.ID)
				return
			}
			auth.JSON(rw, 201, out)
		})
	}
	m.HandleFunc("GET /api/admin/project-proposals", func(rw http.ResponseWriter, r *http.Request) {
		u, ok := h.User(rw, r)
		if !ok {
			return
		}
		if !h.Policy.Allows(u, "project.approve", "") {
			writeFail(rw, ErrDenied, "")
			return
		}
		items, e := w.proposals(r.Context())
		if e != nil {
			writeFail(rw, e, "")
			return
		}
		auth.JSON(rw, 200, items)
	})
	m.HandleFunc("POST /api/project-proposals/{id}/approve", func(rw http.ResponseWriter, r *http.Request) {
		if !h.SameOrigin(rw, r) {
			return
		}
		u, ok := h.User(rw, r)
		if !ok {
			return
		}
		if !h.Policy.Allows(u, "project.approve", "") {
			writeFail(rw, ErrDenied, "")
			return
		}
		if err := w.approve(r.Context(), u, r.PathValue("id")); err != nil {
			writeFail(rw, err, "")
			return
		}
		auth.JSON(rw, 200, map[string]string{"status": "approved"})
	})
}
func writeFail(w http.ResponseWriter, e error, id string) {
	switch {
	case errors.Is(e, errBad):
		auth.Problem(w, 400, "bad_input", "请检查标题、说明和操作编号")
	case errors.Is(e, ErrDenied):
		auth.Problem(w, 403, "forbidden", "项目操作权限不足")
	case errors.Is(e, errWritePending):
		auth.Problem(w, 409, "outcome_unknown", fmt.Sprintf("写入结果待核对，不要重复提交。操作编号：%s", id))
	default:
		auth.Problem(w, 503, "unavailable", "项目服务暂不可用")
	}
}
