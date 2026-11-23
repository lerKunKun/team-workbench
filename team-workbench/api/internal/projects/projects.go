package projects

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"team-workbench/internal/auth"
	"time"
)

const DemoID = "e98503aa-4127-4f1e-8996-a82239824aa9"

var ErrUnavailable = errors.New("project source unavailable")
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

type Project struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Owner   string `json:"owner"`
	URL     string `json:"url"`
}
type Issue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ProjectID string `json:"projectId"`
	Owner     string `json:"owner"`
	Due       string `json:"dueDate"`
	State     string `json:"state"`
	Blocker   string `json:"blocker"`
	URL       string `json:"linearUrl"`
	Done      bool   `json:"done"`
}
type Source interface {
	Projects(context.Context) ([]Project, error)
	Issues(context.Context, string) ([]Issue, error)
	Kind() string
}
type Service struct {
	Source Source
	Policy *auth.Policy
}

func WeekStart(t time.Time) string {
	t = t.In(shanghai)
	return t.AddDate(0, 0, -(int(t.Weekday())+6)%7).Format("2006-01-02")
}
func (s Service) Can(u auth.Principal, act, id string) bool { return s.Policy.Allows(u, act, id) }
func (s Service) Visible(ctx context.Context, u auth.Principal) ([]Project, error) {
	if s.Source == nil {
		return nil, ErrUnavailable
	}
	all, err := s.Source.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, p := range all {
		if s.Can(u, "project.read", p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

type report struct {
	Projects   []Project `json:"projects"`
	Deliveries []Issue   `json:"deliveries"`
	Overdue    []Issue   `json:"overdue"`
	Source     string    `json:"source"`
	Week       string    `json:"week"`
	AsOf       time.Time `json:"asOf"`
}

func (s Service) Register(mux *http.ServeMux, h *auth.Handler) {
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		u, ok := h.User(w, r)
		if !ok {
			return
		}
		items, err := s.Visible(r.Context(), u)
		if err != nil {
			problem(w)
			return
		}
		now := time.Now()
		week := r.URL.Query().Get("week")
		if week == "" {
			week = WeekStart(now)
		}
		start, err := time.ParseInLocation("2006-01-02", week, shanghai)
		if err != nil || start.Weekday() != time.Monday {
			auth.Problem(w, 400, "invalid_week", "请选择周一作为周起始日期")
			return
		}
		end := start.AddDate(0, 0, 7).Format("2006-01-02")
		o := report{Projects: items, Deliveries: []Issue{}, Overdue: []Issue{}, Source: s.Source.Kind(), Week: week, AsOf: now}
		today := now.In(shanghai).Format("2006-01-02")
		for _, p := range items {
			xs, err := s.Source.Issues(r.Context(), p.ID)
			if err != nil {
				problem(w)
				return
			}
			for _, i := range xs {
				if i.ProjectID != p.ID {
					continue
				}
				if owner := r.URL.Query().Get("owner"); owner != "" && !strings.Contains(strings.ToLower(i.Owner), strings.ToLower(owner)) {
					continue
				}
				if state := r.URL.Query().Get("state"); state != "" && state != i.State {
					continue
				}
				if i.Due >= week && i.Due < end {
					o.Deliveries = append(o.Deliveries, i)
				} else if i.Due != "" && i.Due < today && !i.Done {
					o.Overdue = append(o.Overdue, i)
				}
			}
		}
		sortIssues(o.Deliveries)
		sortIssues(o.Overdue)
		auth.JSON(w, 200, o)
	})
	mux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := h.User(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !auth.IsUUID(id) || !s.Can(u, "project.read", id) {
			notFound(w)
			return
		}
		items, err := s.Visible(r.Context(), u)
		if err != nil {
			problem(w)
			return
		}
		for _, p := range items {
			if p.ID == id {
				xs, err := s.Source.Issues(r.Context(), id)
				if err != nil {
					problem(w)
					return
				}
				out := []Issue{}
				for _, i := range xs {
					if i.ProjectID == id {
						out = append(out, i)
					}
				}
				sortIssues(out)
				auth.JSON(w, 200, map[string]any{"project": p, "issues": out, "source": s.Source.Kind()})
				return
			}
		}
		notFound(w)
	})
}
func sortIssues(xs []Issue) {
	slices.SortFunc(xs, func(a, b Issue) int {
		if x := strings.Compare(a.Due, b.Due); x != 0 {
			return x
		}
		return strings.Compare(a.ID, b.ID)
	})
}
func problem(w http.ResponseWriter) {
	auth.Problem(w, 503, "projects_unavailable", "项目数据暂不可用，请检查 Linear 配置或稍后重试")
}
func notFound(w http.ResponseWriter) {
	auth.Problem(w, 404, "not_found", "项目不存在或不可访问")
}

type demo struct{}

func NewDemo() Source     { return demo{} }
func (demo) Kind() string { return "demo" }
func (demo) Projects(context.Context) ([]Project, error) {
	return []Project{{DemoID, "工作台演示项目", "用于体验交付、阻塞与 Roadmap。不会写入真实 Linear。", "演示负责人", ""}}, nil
}
func (demo) Issues(_ context.Context, p string) ([]Issue, error) {
	if p != DemoID {
		return nil, ErrUnavailable
	}
	now := time.Now().In(shanghai)
	date := func(d int) string { return now.AddDate(0, 0, d).Format("2006-01-02") }
	return []Issue{
		{"DEMO-12", "完成扫码登录联调", p, "王晓", date(0), "进行中", "等待测试应用回调地址确认", "", false},
		{"DEMO-14", "验收本周交付看板", p, "李晨", date(0), "待验收", "", "", false},
		{"DEMO-16", "整理反馈收件箱", p, "", date(0), "待办", "", "", false},
		{"DEMO-09", "提交首版测试记录", p, "赵敏", date(-8), "进行中", "等待测试数据", "", false},
	}, nil
}
