package roadmap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"team-workbench/internal/auth"
	"time"

	"github.com/jackc/pgx/v5"
)

const publicRoadmap = "https://roadmap-admin.example.com"

type ExternalItem struct {
	ProjectID string `json:"projectId"`
	ItemID    string `json:"itemId"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Votes     int    `json:"votes"`
}

type Bridge struct {
	service                           *Service
	users                             *auth.Store
	feedbackKey, ssoKey, actor        string
	projects                          map[string]string
	dispatchURL, dispatchKey, hookKey string
}

// WithN8N enables a private relay without exposing n8n's webhook on the edge.
func (b *Bridge) WithN8N(dispatchKey, hookKey string) *Bridge {
	return b.withDispatch("http://rd-n8n:5678", dispatchKey, hookKey)
}

func (b *Bridge) withDispatch(base, dispatchKey, hookKey string) *Bridge {
	b.dispatchURL, b.dispatchKey, b.hookKey = base+"/webhook/roadmap-feedback", dispatchKey, hookKey
	return b
}

func NewBridge(s *Service, users *auth.Store, feedbackKey, ssoKey, mappings, actor string) *Bridge {
	b := &Bridge{service: s, users: users, feedbackKey: feedbackKey, ssoKey: ssoKey, actor: actor, projects: map[string]string{}}
	for _, pair := range strings.Split(mappings, ",") {
		src, dst, ok := strings.Cut(pair, "=")
		src, dst = strings.TrimSpace(src), strings.TrimSpace(dst)
		if ok && auth.IsUUID(src) && auth.IsUUID(dst) {
			if _, duplicate := b.projects[src]; duplicate {
				b.projects = map[string]string{}
				break
			}
			b.projects[src] = dst
		}
	}
	return b
}

func (b *Bridge) Migrate(ctx context.Context) error {
	_, err := b.service.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS wb.roadmap_external (
	 external_id uuid PRIMARY KEY, external_project uuid NOT NULL, item_id uuid NOT NULL UNIQUE REFERENCES wb.roadmap(id)
	); CREATE TABLE IF NOT EXISTS wb.roadmap_sso_grant (
	 token_hash bytea PRIMARY KEY, account_id uuid NOT NULL REFERENCES wb.account(id) ON DELETE CASCADE, challenge text NOT NULL, expires_at timestamptz NOT NULL
	); CREATE INDEX IF NOT EXISTS wb_roadmap_sso_exp ON wb.roadmap_sso_grant(expires_at);`)
	return err
}

func (b *Bridge) importItem(ctx context.Context, x ExternalItem) (Item, error) {
	var i Item
	p := b.projects[x.ProjectID]
	x.Title, x.Summary = strings.TrimSpace(x.Title), strings.TrimSpace(x.Summary)
	if p == "" || !auth.IsUUID(x.ItemID) || len(x.Title) == 0 || len(x.Title) > 480 || len(x.Summary) > 16000 || x.Votes < 0 || x.Votes > 100000000 {
		return i, ErrInvalid
	}
	u, err := b.users.Principal(ctx, b.actor)
	if err != nil || !b.service.policy.Allows(u, "roadmap.manage", p) {
		return i, ErrHidden
	}
	if b.service.lookup != nil {
		if _, _, err = b.service.lookup(ctx, u, p); err != nil {
			return i, err
		}
	}
	tx, err := b.service.db.Begin(ctx)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,6842))`, x.ItemID); err != nil {
		return i, err
	}
	var id, source string
	err = tx.QueryRow(ctx, `SELECT item_id::text,external_project::text FROM wb.roadmap_external WHERE external_id=$1`, x.ItemID).Scan(&id, &source)
	if err == nil {
		if source != x.ProjectID {
			return i, ErrConflict
		}
		i, err = scanItem(tx.QueryRow(ctx, itemsQuery+`WHERE r.id=$1 AND r.project_id=$2`, id, p))
		if err != nil {
			return i, ErrConflict
		}
		return i, nil
	}
	if err != pgx.ErrNoRows {
		return i, err
	}
	i = Item{Project: p, Title: x.Title, State: "triage", Count: 1, Sources: map[string]int{"external": 1}}
	if err = tx.QueryRow(ctx, `INSERT INTO wb.roadmap(project_id,title) VALUES($1,$2) RETURNING id::text,updated_at`, p, x.Title).Scan(&i.ID, &i.Updated); err != nil {
		return i, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wb.roadmap_external VALUES($1,$2,$3)`, x.ItemID, x.ProjectID, i.ID); err != nil {
		return i, err
	}
	body := fmt.Sprintf("公开 Roadmap 条目 %s · 导入时票数：%d\n\n%s", x.ItemID, x.Votes, x.Summary)
	if _, err = tx.Exec(ctx, `INSERT INTO wb.feedback(item_id,original_item_id,author,body,source) VALUES($1,$1,$2,$3,'external')`, i.ID, "service:public-roadmap", body); err != nil {
		return i, err
	}
	if err = logAction(ctx, tx, u.ID, "roadmap.external.import", i.ID); err != nil {
		return i, err
	}
	return i, tx.Commit(ctx)
}

var grantPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (b *Bridge) createGrant(ctx context.Context, u auth.Principal, challenge string) (string, error) {
	if u.ID != b.actor || u.Role != "admin" || !grantPattern.MatchString(challenge) {
		return "", ErrHidden
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	tx, err := b.service.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM wb.roadmap_sso_grant WHERE expires_at<=now()`); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wb.roadmap_sso_grant VALUES($1,$2,$3,now()+interval '60 seconds')`, sum[:], u.ID, challenge); err != nil {
		return "", err
	}
	return raw, tx.Commit(ctx)
}

func (b *Bridge) exchangeGrant(ctx context.Context, raw, verifier string) error {
	if !grantPattern.MatchString(raw) || !grantPattern.MatchString(verifier) {
		return ErrHidden
	}
	sum := sha256.Sum256([]byte(raw))
	p := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(p[:])
	tx, err := b.service.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `DELETE FROM wb.roadmap_sso_grant g USING wb.account a WHERE g.account_id=a.id AND a.active AND a.role='admin' AND a.id=$3 AND g.token_hash=$1 AND g.challenge=$2 AND g.expires_at>now() RETURNING g.account_id::text`, sum[:], challenge, b.actor).Scan(&id)
	if err == pgx.ErrNoRows {
		return ErrHidden
	}
	if err != nil {
		return err
	}
	if err = logAction(ctx, tx, id, "roadmap.sso.exchange", "roadmap-admin.example.com"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func bridgeKey(w http.ResponseWriter, r *http.Request, key string) bool {
	if len(key) < 32 {
		auth.Problem(w, 503, "integration_unconfigured", "接入暂未配置")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
		auth.Problem(w, 401, "unauthorized", "接入凭据无效")
		return false
	}
	return true
}

func (b *Bridge) distinctCredentials(w http.ResponseWriter) bool {
	seen := map[string]bool{}
	for _, key := range []string{b.feedbackKey, b.ssoKey, b.dispatchKey, b.hookKey} {
		if key == "" {
			continue
		}
		if len(key) < 32 || seen[key] {
			auth.Problem(w, 503, "integration_unconfigured", "工作台接入需要独立凭据")
			return false
		}
		seen[key] = true
	}
	return true
}

func (b *Bridge) Register(m *http.ServeMux, h *auth.Handler) {
	m.HandleFunc("POST /api/integrations/roadmap/dispatch", b.dispatch)
	m.HandleFunc("POST /api/integrations/roadmap/feedback", func(w http.ResponseWriter, r *http.Request) {
		if !b.distinctCredentials(w) || !bridgeKey(w, r, b.feedbackKey) {
			return
		}
		if !auth.IsUUID(b.actor) {
			auth.Problem(w, 503, "integration_unconfigured", "缺少代理账号")
			return
		}
		var x ExternalItem
		if !auth.Decode(w, r, &x) {
			return
		}
		i, err := b.importItem(r.Context(), x)
		reply(w, 200, map[string]string{"id": i.ID}, err)
	})
	m.HandleFunc("GET /api/integrations/roadmap/sso/authorize", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !b.distinctCredentials(w) {
			return
		}
		if len(b.ssoKey) < 32 || !auth.IsUUID(b.actor) {
			auth.Problem(w, 503, "integration_unconfigured", "Roadmap 单点登录尚未启用")
			return
		}
		challenge, state := r.URL.Query().Get("challenge"), r.URL.Query().Get("state")
		if !grantPattern.MatchString(challenge) || !grantPattern.MatchString(state) {
			reply(w, 0, nil, ErrInvalid)
			return
		}
		u, err := h.CurrentUser(r.Context(), r)
		if errors.Is(err, auth.ErrLogin) || errors.Is(err, pgx.ErrNoRows) {
			back := "/api/integrations/roadmap/sso/authorize?" + url.Values{"challenge": {challenge}, "state": {state}}.Encode()
			http.Redirect(w, r, "/login?"+url.Values{"next": {back}}.Encode(), http.StatusSeeOther)
			return
		}
		if err != nil {
			reply(w, 0, nil, err)
			return
		}
		grant, err := b.createGrant(r.Context(), u, challenge)
		if err != nil {
			reply(w, 0, nil, err)
			return
		}
		q := url.Values{"grant": {grant}, "state": {state}}
		http.Redirect(w, r, publicRoadmap+"/workbench/api/sso/callback?"+q.Encode(), http.StatusSeeOther)
	})
	m.HandleFunc("POST /api/integrations/roadmap/sso/exchange", func(w http.ResponseWriter, r *http.Request) {
		if !b.distinctCredentials(w) || !bridgeKey(w, r, b.ssoKey) {
			return
		}
		var x struct{ Grant, Verifier string }
		if !auth.Decode(w, r, &x) {
			return
		}
		if err := b.exchangeGrant(r.Context(), x.Grant, x.Verifier); err != nil {
			reply(w, 0, nil, err)
			return
		}
		auth.JSON(w, 200, map[string]bool{"ok": true})
	})
}

var dispatchClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (b *Bridge) dispatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !b.distinctCredentials(w) {
		return
	}
	if b.dispatchURL == "" || len(b.hookKey) < 32 || len(b.feedbackKey) < 32 {
		auth.Problem(w, 503, "integration_unconfigured", "n8n 尚未配置")
		return
	}
	if !bridgeKey(w, r, b.dispatchKey) {
		return
	}
	var x ExternalItem
	if !auth.Decode(w, r, &x) {
		return
	}
	if !auth.IsUUID(x.ProjectID) || !auth.IsUUID(x.ItemID) || len(x.Title) > 480 || len(x.Summary) > 16000 || x.Votes < 0 || x.Votes > 100000000 {
		reply(w, 0, nil, ErrInvalid)
		return
	}
	data, _ := json.Marshal(x)
	up, err := http.NewRequestWithContext(r.Context(), http.MethodPost, b.dispatchURL, bytes.NewReader(data))
	if err != nil {
		reply(w, 0, nil, err)
		return
	}
	up.Header.Set("X-Workflow-Token", b.hookKey)
	up.Header.Set("Content-Type", "application/json")
	resp, err := dispatchClient.Do(up)
	if err != nil {
		reply(w, 0, nil, err)
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var result struct {
			ID string `json:"id"`
		}
		if err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result); err != nil || !auth.IsUUID(result.ID) {
			auth.Problem(w, 502, "integration_unavailable", "n8n 未确认接收")
			return
		}
		auth.JSON(w, 200, result)
	case http.StatusBadRequest:
		reply(w, 0, nil, ErrInvalid)
	case http.StatusConflict:
		reply(w, 0, nil, ErrConflict)
	default:
		auth.Problem(w, 502, "integration_unavailable", "n8n 未确认接收")
	}
}
