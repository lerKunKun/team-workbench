package roadmap

import (
	"context"
	_ "embed"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"team-workbench/internal/auth"
	"time"
)

//go:embed schema.sql
var schema string
var ErrHidden = errors.New("roadmap resource unavailable")
var ErrNoModel = errors.New("model unavailable")
var ErrInvalid = errors.New("invalid roadmap input")
var ErrConflict = errors.New("roadmap state changed")

type ProjectLookup func(context.Context, auth.Principal, string) (string, []string, error)
type Service struct {
	db       *pgxpool.Pool
	policy   *auth.Policy
	model    *Model
	lookup   ProjectLookup
	identity *auth.Store
}

func New(db *pgxpool.Pool, p *auth.Policy, m *Model) *Service {
	if p == nil {
		p, _ = auth.NewPolicy()
	}
	return &Service{db: db, policy: p, model: m, identity: auth.NewStore(db)}
}
func (s *Service) WithProjects(l ProjectLookup) *Service { s.lookup = l; return s }
func (s *Service) Migrate(ctx context.Context) error     { _, err := s.db.Exec(ctx, schema); return err }

type Item struct {
	ID         string         `json:"id"`
	Project    string         `json:"projectId"`
	Title      string         `json:"title"`
	State      string         `json:"state"`
	Count      int            `json:"feedbackCount"`
	Versions   int            `json:"planCount"`
	Sources    map[string]int `json:"sources"`
	Generation string         `json:"generation"`
	Updated    time.Time      `json:"updatedAt"`
	Revision   int            `json:"-"`
}
type Feedback struct {
	ID       string    `json:"id"`
	Body     string    `json:"body"`
	Source   string    `json:"source"`
	Original string    `json:"originalItemId"`
	At       time.Time `json:"createdAt"`
}
type Plan struct {
	Version int       `json:"version"`
	Source  string    `json:"source"`
	At      time.Time `json:"createdAt"`
}
type Detail struct {
	Item       Item       `json:"item"`
	Feedback   []Feedback `json:"feedback"`
	Plans      []Plan     `json:"plans"`
	ModelReady bool       `json:"modelReady"`
}

func (s *Service) Submit(ctx context.Context, u auth.Principal, p, title, body, source string) (Item, error) {
	var i Item
	if !auth.IsUUID(p) || !s.policy.Allows(u, "feedback.create", p) {
		return i, ErrHidden
	}
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if len(title) < 1 || len(title) > 400 || len(body) < 1 || len(body) > 8000 || (source != "web" && source != "dingtalk") {
		return i, ErrInvalid
	}
	if s.lookup != nil {
		if _, _, err := s.lookup(ctx, u, p); err != nil {
			return i, err
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(ctx)
	i = Item{Project: p, Title: title, State: "triage", Count: 1, Sources: map[string]int{source: 1}}
	if err = tx.QueryRow(ctx, `INSERT INTO wb.roadmap(project_id,title) VALUES($1,$2) RETURNING id::text,updated_at`, p, title).Scan(&i.ID, &i.Updated); err != nil {
		return i, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wb.feedback(item_id,original_item_id,author,body,source) VALUES($1,$1,$2,$3,$4)`, i.ID, u.ID, body, source); err != nil {
		return i, err
	}
	if err = logAction(ctx, tx, u.ID, "feedback.create", i.ID); err != nil {
		return i, err
	}
	return i, tx.Commit(ctx)
}

const itemsQuery = `SELECT r.id::text,r.project_id::text,r.title,r.state,r.revision,r.updated_at,
(SELECT count(*) FROM wb.feedback f WHERE f.item_id=r.id),(SELECT count(*) FROM wb.plan p WHERE p.item_id=r.id),
coalesce((SELECT jsonb_object_agg(source,n) FROM (SELECT source,count(*) n FROM wb.feedback f WHERE f.item_id=r.id GROUP BY source) x),'{}'::jsonb),
coalesce((SELECT status FROM wb.generation g WHERE g.item_id=r.id AND g.revision=r.revision ORDER BY created_at DESC LIMIT 1),'')
FROM wb.roadmap r `

func scanItem(row pgx.Row) (Item, error) {
	var i Item
	err := row.Scan(&i.ID, &i.Project, &i.Title, &i.State, &i.Revision, &i.Updated, &i.Count, &i.Versions, &i.Sources, &i.Generation)
	return i, err
}

func (s *Service) List(ctx context.Context, u auth.Principal) ([]Item, error) {
	ids := []string{}
	for id := range u.Projects {
		if auth.IsUUID(id) {
			ids = append(ids, id)
		}
	}
	rows, err := s.db.Query(ctx, itemsQuery+`WHERE ($1::boolean OR project_id=ANY($2::uuid[])) ORDER BY updated_at DESC LIMIT 500`, u.Role == "admin", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		if s.policy.Allows(u, "roadmap.manage", i.Project) {
			out = append(out, i)
		}
	}
	return out, rows.Err()
}
func (s *Service) get(ctx context.Context, u auth.Principal, id, act string) (Item, error) {
	if !auth.IsUUID(id) {
		return Item{}, ErrHidden
	}
	i, err := scanItem(s.db.QueryRow(ctx, itemsQuery+`WHERE r.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrHidden
	}
	if err != nil {
		return i, err
	}
	if !s.policy.Allows(u, act, i.Project) {
		return i, ErrHidden
	}
	return i, nil
}
func (s *Service) Details(ctx context.Context, u auth.Principal, id string) (Detail, error) {
	i, err := s.get(ctx, u, id, "roadmap.manage")
	d := Detail{Item: i, Feedback: []Feedback{}, Plans: []Plan{}, ModelReady: s.model != nil}
	if err != nil {
		return d, err
	}
	rows, err := s.db.Query(ctx, `SELECT id::text,body,source,original_item_id::text,created_at FROM wb.feedback WHERE item_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var f Feedback
		if err = rows.Scan(&f.ID, &f.Body, &f.Source, &f.Original, &f.At); err != nil {
			rows.Close()
			return d, err
		}
		d.Feedback = append(d.Feedback, f)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return d, err
	}
	rows, err = s.db.Query(ctx, `SELECT version,source,created_at FROM wb.plan WHERE item_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Plan
		if err = rows.Scan(&p.Version, &p.Source, &p.At); err != nil {
			return d, err
		}
		d.Plans = append(d.Plans, p)
	}
	return d, rows.Err()
}
func logAction(ctx context.Context, tx pgx.Tx, u, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO wb.audit(actor,action,target) VALUES($1,$2,$3)`, u, action, target)
	return err
}
func (s *Service) lock(ctx context.Context, tx pgx.Tx, u auth.Principal, id string) (Item, error) {
	var i Item
	i.ID = id
	if !auth.IsUUID(id) {
		return i, ErrHidden
	}
	err := tx.QueryRow(ctx, `SELECT project_id::text,state,revision,title FROM wb.roadmap WHERE id=$1 FOR UPDATE`, id).Scan(&i.Project, &i.State, &i.Revision, &i.Title)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrHidden
	}
	if err == nil && !s.policy.Allows(u, "roadmap.manage", i.Project) {
		return i, ErrHidden
	}
	return i, err
}

var states = map[string]bool{"triage": true, "evaluating": true, "confirmed": true, "delivered": true, "declined": true}

func (s *Service) Move(ctx context.Context, u auth.Principal, id, state string) error {
	if !states[state] {
		return ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	i, err := s.lock(ctx, tx, u, id)
	if err != nil {
		return err
	}
	if i.State == state {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE wb.roadmap SET state=$2,revision=revision+1,updated_at=now() WHERE id=$1`, id, state); err != nil {
		return err
	}
	if state == "confirmed" && s.model != nil {
		if err = s.enqueue(ctx, tx, u.ID, id, i.Revision+1); err != nil {
			return err
		}
	}
	if err = logAction(ctx, tx, u.ID, "roadmap."+state, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) Merge(ctx context.Context, u auth.Principal, target, source string) error {
	if target == source {
		return ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, b := target, source
	if a > b {
		a, b = b, a
	}
	x, err := s.lock(ctx, tx, u, a)
	if err != nil {
		return err
	}
	y, err := s.lock(ctx, tx, u, b)
	if err != nil {
		return err
	}
	if x.Project != y.Project {
		return ErrHidden
	}
	if x.ID != target {
		x, y = y, x
	}
	if y.State != "triage" && y.State != "evaluating" {
		return ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE wb.feedback SET item_id=$1 WHERE item_id=$2`, target, source)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wb.roadmap SET revision=revision+1,updated_at=now(),state=CASE WHEN id=$2 THEN 'declined' ELSE state END WHERE id IN ($1,$2)`, target, source); err != nil {
		return err
	}
	if err = logAction(ctx, tx, u.ID, "feedback.merge", source+":"+target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, user, id string, rev int) error {
	_, err := tx.Exec(ctx, `INSERT INTO wb.generation(item_id,revision,actor) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, rev, user)
	return err
}
func (s *Service) SavePlan(ctx context.Context, u auth.Principal, id, body string, generate bool) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	i, err := s.lock(ctx, tx, u, id)
	if err != nil {
		return 0, err
	}
	if i.State != "confirmed" {
		return 0, ErrConflict
	}
	if generate {
		if s.model == nil {
			return 0, ErrNoModel
		}
		if err = s.enqueue(ctx, tx, u.ID, id, i.Revision); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 48000 {
		return 0, ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE wb.roadmap SET revision=revision+1,updated_at=now() WHERE id=$1`, id); err != nil {
		return 0, err
	}
	n, err := addPlan(ctx, tx, id, u.ID, body, "manual")
	if err != nil {
		return 0, err
	}
	if err = logAction(ctx, tx, u.ID, "plan.manual", id); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
func addPlan(ctx context.Context, tx pgx.Tx, id, u, body, src string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `INSERT INTO wb.plan(item_id,version,body,source,author) VALUES($1,(SELECT coalesce(max(version),0)+1 FROM wb.plan WHERE item_id=$1),$2,$3,$4) RETURNING version`, id, body, src, u).Scan(&n)
	return n, err
}
func (s *Service) Download(ctx context.Context, u auth.Principal, id string, n int) (string, error) {
	if _, err := s.get(ctx, u, id, "roadmap.manage"); err != nil {
		return "", err
	}
	var b string
	err := s.db.QueryRow(ctx, `SELECT body FROM wb.plan WHERE item_id=$1 AND version=$2`, id, n).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrHidden
	}
	return b, err
}
