package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"team-workbench/internal/auth"
	"team-workbench/internal/bot"
	"team-workbench/internal/projects"
	"team-workbench/internal/roadmap"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dsn := os.Getenv("WORKBENCH_DATABASE_URL")
	if dsn == "" {
		return errors.New("WORKBENCH_DATABASE_URL is required")
	}
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer p.Close()
	origin := os.Getenv("WORKBENCH_ORIGIN")
	if origin == "" {
		origin = "http://localhost:3100"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid WORKBENCH_ORIGIN")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1")) {
		return errors.New("HTTPS origin required outside local development")
	}
	s := auth.NewStore(p)
	policy, err := auth.NewPolicy()
	if err != nil {
		return err
	}
	h := auth.NewHandler(s, policy, origin, u.Scheme == "https")
	if o := os.Getenv("WORKBENCH_ADMIN_ORIGIN"); o != "" {
		if err = h.AllowAdminOrigin(o); err != nil {
			return err
		}
	}
	m := h.Router()
	var source projects.Source
	if os.Getenv("WORKBENCH_MODE") == "demo" {
		source = projects.NewDemo()
	} else if l := projects.NewLinear(os.Getenv("LINEAR_API_KEY"), os.Getenv("LINEAR_TEST_TEAM_ID"), os.Getenv("LINEAR_TEST_PROJECT_ID")); l != nil {
		source = l
	}
	pr := projects.Service{Source: source, Policy: policy}
	pr.Register(m, h)
	writer := projects.Writer{DB: p, Projects: pr}
	writer.Register(m, h)
	dm := bot.NewUI(s, writer, os.Getenv("DINGTALK_CORP_ID"), os.Getenv("LINEAR_TEST_PROJECT_ID"), os.Getenv("WORKBENCH_BOT_TOKEN"), origin)
	dm.Register(m)
	var model *roadmap.Model
	if source != nil && source.Kind() == "linear" {
		model = roadmap.NewModel(os.Getenv("LLM_API_BASE_URL"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL"))
	}
	rm := roadmap.New(p, policy, model).WithProjects(func(ctx context.Context, a auth.Principal, id string) (string, []string, error) {
		visible, e := pr.Visible(ctx, a)
		if e != nil {
			return "", nil, e
		}
		for _, x := range visible {
			if x.ID != id {
				continue
			}
			xs, e := source.Issues(ctx, id)
			if e != nil {
				return "", nil, e
			}
			titles := []string{}
			for _, i := range xs {
				if i.ProjectID == id {
					titles = append(titles, i.ID+" "+i.Title)
					if len(titles) == 20 {
						break
					}
				}
			}
			return x.Name, titles, nil
		}
		return "", nil, roadmap.ErrHidden
	})
	rm.Register(m, h)
	bridge := roadmap.NewBridge(rm, s, os.Getenv("ROADMAP_FEEDBACK_KEY"), os.Getenv("ROADMAP_SSO_KEY"), os.Getenv("ROADMAP_PROJECT_MAP"), os.Getenv("ROADMAP_OPERATOR_ID"))
	if key := os.Getenv("ROADMAP_DISPATCH_KEY"); key != "" {
		bridge.WithN8N(key, os.Getenv("ROADMAP_HOOK_KEY"))
	}
	bridge.Register(m, h)
	startup, ready := context.WithTimeout(ctx, 20*time.Second)
	defer ready()
	if err = s.Migrate(startup); err != nil {
		return errors.New("account database migration failed")
	}
	if err = rm.Migrate(startup); err != nil {
		return errors.New("roadmap database migration failed")
	}
	if err = bridge.Migrate(startup); err != nil {
		return errors.New("roadmap integration migration failed")
	}
	if err = writer.Migrate(startup); err != nil {
		return errors.New("project operation migration failed")
	}
	if err = dm.Migrate(startup); err != nil {
		return errors.New("private bot migration failed")
	}
	if len(os.Args) > 1 {
		if len(os.Args) != 3 || os.Args[1] != "bootstrap-admin" {
			return errors.New("usage: api bootstrap-admin USERNAME; password from WORKBENCH_BOOTSTRAP_PASSWORD_FILE")
		}
		path := os.Getenv("WORKBENCH_BOOTSTRAP_PASSWORD_FILE")
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
			return errors.New("password file must be private and at most 1024 bytes")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return errors.New("could not read password file")
		}
		if _, err = s.CreateAccount(startup, "", os.Args[2], "系统管理员", "admin", strings.TrimRight(string(b), "\r\n")); err != nil {
			return errors.New("administrator bootstrap failed (existing account or invalid fields)")
		}
		fmt.Println("Administrator created. Remove the bootstrap password file after storing it securely.")
		return nil
	}
	m.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		c, stop := context.WithTimeout(r.Context(), 2*time.Second)
		defer stop()
		if p.Ping(c) != nil {
			auth.Problem(w, 503, "database_unavailable", "数据库暂不可用")
			return
		}
		auth.JSON(w, 200, map[string]string{"status": "ok"})
	})
	h.RegisterDing(m, auth.DingConfig{Corp: os.Getenv("DINGTALK_CORP_ID"), Client: os.Getenv("DINGTALK_CLIENT_ID"), Secret: os.Getenv("DINGTALK_CLIENT_SECRET"), Redirect: os.Getenv("DINGTALK_QR_REDIRECT_URL")})
	go rm.Work(ctx)
	addr := os.Getenv("WORKBENCH_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8091"
	}
	srv := &http.Server{Addr: addr, Handler: m, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 45 * time.Second}
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- srv.ListenAndServe() }()
	log.Print("workbench API listening on ", addr)
	select {
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return srv.Shutdown(c)
	}
	return nil
}
