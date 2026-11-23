package automation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const demoGroup = "__rd_demo__"

type Server struct {
	cfg  Config
	st   *Store
	ding *Ding
}

func NewServer(c Config, st *Store) *Server { return &Server{c, st, &Ding{c: c}} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.st.Ping(ctx) != nil {
			fail(w, 503, "database unavailable")
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /internal/status", s.auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]bool{"demo": s.cfg.Demo, "dingtalk_configured": s.cfg.DingReady(), "v2_configured": s.cfg.V2Ready(), "stream_requested": s.cfg.Stream, "v1_configured": s.cfg.DingReady() && s.cfg.Groups[s.cfg.V1Group]})
	}))
	mux.HandleFunc("POST /internal/process/intake", s.auth(s.process("intake", s.doIntake)))
	mux.HandleFunc("POST /internal/process/action", s.auth(s.process("action", s.doAction)))
	mux.HandleFunc("POST /internal/workbench/intake", s.auth(s.process("intake", s.workbenchIntake)))
	mux.HandleFunc("POST /internal/workbench/action", s.auth(s.process("action", s.workbenchAction)))
	mux.HandleFunc("POST /internal/demo/intake", s.auth(s.demoIntake))
	mux.HandleFunc("POST /internal/demo/action", s.auth(s.demoAction))
	mux.HandleFunc("GET /internal/demo/draft/{id}", s.auth(s.demoDraft))
	mux.HandleFunc("POST /internal/v1/daily", s.auth(s.daily))
	mux.HandleFunc("POST /internal/messages/send", s.auth(s.manualNotify))
	mux.HandleFunc("POST /webhooks/github", s.github)
	mux.HandleFunc("POST /webhooks/linear", s.linearWebhook)
	return mux
}

func write(w http.ResponseWriter, status int, x any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(x)
}

func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, x any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(x) != nil || d.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid JSON body")
		return false
	}
	return true
}

func same(a, b string) bool { return b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !same(r.Header.Get("X-Internal-Token"), s.cfg.InternalToken) {
			fail(w, 401, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) process(kind string, run func(context.Context, Job) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input Job
		if !decode(w, r, &input) {
			return
		}
		if !validID(input.ID) || !validID(input.Lease) {
			fail(w, 400, "invalid job reference")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 80*time.Second)
		defer cancel()
		j, err := s.st.activeJob(ctx, input.ID, input.Lease, kind)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "job lease not active")
			return
		}
		if err != nil {
			fail(w, 503, "job lookup failed")
			return
		}
		if err := run(ctx, j); err != nil {
			log.Printf("job %s %s processing: %s", kind, j.ID, errorLabel(err))
			fail(w, 502, "processing failed")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	}
}

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

func errorLabel(err error) string {
	// Error text from pgx can include query context, values, or message content.
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return "postgres SQLSTATE " + pgErr.SQLState()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	// Expected integration errors are constructed locally with no response bodies or keys.
	for _, prefix := range []string{"linear HTTP ", "linear GraphQL ", "linear issue", "model HTTP ", "model returned ", "model must ", "model transport ", "dingtalk token HTTP ", "dingtalk API ", "n8n HTTP ", "n8n transport failure", "n8n did not return success"} {
		if len(err.Error()) >= len(prefix) && err.Error()[:len(prefix)] == prefix {
			return err.Error()
		}
	}
	return "operation failed"
}

var hookClient = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (s *Server) Work(ctx context.Context) {
	for ctx.Err() == nil {
		j, err := s.st.next(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				log.Printf("outbox read: %s", errorLabel(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		var problem string
		if err := s.runJob(ctx, j); err != nil {
			problem = errorLabel(err)
			log.Printf("outbox %s job=%s attempt=%d: %s", j.Kind, j.ID, j.Attempts, problem)
		}
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
		if err := s.st.finish(finish, j, problem); err != nil {
			log.Printf("outbox status could not be saved: %s", errorLabel(err))
		}
		cancel()
	}
}

func (s *Server) runJob(ctx context.Context, j Job) error {
	if j.Kind == "notify" {
		if s.cfg.Demo != (j.Payload.Group == demoGroup) || (!s.cfg.Demo && (j.Payload.Group != s.cfg.V1Group || !s.cfg.Groups[j.Payload.Group])) {
			return fmt.Errorf("notification group disabled")
		}
		return s.ding.message(ctx, j.Payload.Group, j.Payload.Title, j.Payload.Text)
	}
	path := "dingtalk-ai-intake"
	if j.Kind == "action" {
		path = "dingtalk-card-action"
	}
	if j.Payload.Corp != "" {
		path = "workbench-private-" + j.Kind
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := call(ctx, hookClient, "POST", s.cfg.N8nURL+"/webhook/"+path, "n8n", "X-Workflow-Token", s.cfg.HookToken, j, &res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("n8n did not return success")
	}
	return nil
}

func (s *Server) demoIntake(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Demo {
		fail(w, 404, "demo disabled")
		return
	}
	var input struct{ Text string }
	if !decode(w, r, &input) {
		return
	}
	if input.Text = compact(input.Text, 1000); input.Text == "" {
		fail(w, 400, "text required")
		return
	}
	id := uuid.NewString()
	if _, err := s.st.pool.Exec(r.Context(), `INSERT INTO app.outbox(job_id,kind,payload) VALUES ($1,'intake',$2)`, id, Payload{Group: demoGroup, Sender: "demo", Text: input.Text}); err != nil {
		fail(w, 503, "outbox unavailable")
		return
	}
	write(w, 202, map[string]string{"draft_id": id})
}

func (s *Server) demoAction(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Demo {
		fail(w, 404, "demo disabled")
		return
	}
	var x Payload
	if !decode(w, r, &x) {
		return
	}
	if !validID(x.DraftID) || (x.Action != "confirm" && x.Action != "cancel") {
		fail(w, 400, "invalid draft action")
		return
	}
	ok, err := s.st.claim(r.Context(), x.DraftID, "draft_"+x.DraftID, demoGroup, "demo", x.Action, true, []string{demoGroup})
	if err != nil {
		fail(w, 503, "outbox unavailable")
		return
	}
	if !ok {
		fail(w, 409, "draft not pending or not delivered")
		return
	}
	write(w, 202, map[string]bool{"accepted": true})
}

func (s *Server) demoDraft(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Demo {
		fail(w, 404, "demo disabled")
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		fail(w, 400, "invalid draft id")
		return
	}
	d, err := s.st.draft(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && d.Group != demoGroup) {
		fail(w, 404, "draft not found")
		return
	}
	if err != nil {
		fail(w, 503, "draft unavailable")
		return
	}
	var n int
	if err := s.st.pool.QueryRow(r.Context(), `SELECT count(*) FROM app.linear_issue WHERE draft_id=$1`, id).Scan(&n); err != nil {
		fail(w, 503, "issue records unavailable")
		return
	}
	write(w, 200, map[string]any{"draft_id": d.ID, "status": d.Status, "delivered": d.Delivered, "card_updated": d.CardUpdated, "issue_count": n, "tasks": d.Tasks})
}
