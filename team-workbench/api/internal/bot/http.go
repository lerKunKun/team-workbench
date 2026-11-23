package bot

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"team-workbench/internal/auth"
	"team-workbench/internal/projects"
)

type UI struct {
	Users                        *auth.Store
	Writer                       projects.Writer
	Corp, Project, Token, Origin string
}

func NewUI(s *auth.Store, w projects.Writer, corp, project, token, origin string) *UI {
	return &UI{s, w, corp, project, token, origin}
}

type message struct {
	Channel, Corp, Staff, Text string
	DraftID                    string `json:"draftId"`
}

func (b *UI) Routes() *http.ServeMux {
	m := http.NewServeMux()
	b.Register(m)
	return m
}
func (b *UI) Register(m *http.ServeMux) {
	check := func(f func(http.ResponseWriter, *http.Request, auth.Principal, message)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if len(b.Token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Workbench-Bot-Token")), []byte(b.Token)) != 1 {
				auth.Problem(w, 401, "unauthorized", "未授权")
				return
			}
			var in message
			if !auth.Decode(w, r, &in) {
				return
			}
			if !DirectAllowed(in.Channel, in.Corp, b.Corp, in.Staff) {
				auth.Problem(w, 403, "denied", "会话不允许")
				return
			}
			u, e := b.Users.DingStaff(r.Context(), in.Corp, in.Staff)
			if e != nil {
				auth.Problem(w, 403, "denied", "钉钉身份未绑定或已停用")
				return
			}
			f(w, r, u, in)
		}
	}
	m.HandleFunc("POST /internal/bot/intake", check(b.intake))
	m.HandleFunc("POST /internal/bot/execute", check(b.execute))
}
func (b *UI) Migrate(ctx context.Context) error {
	_, e := b.Users.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS wb.bot_draft (
	 id uuid PRIMARY KEY, account uuid NOT NULL REFERENCES wb.account, project_id text NOT NULL, kind text NOT NULL, input jsonb NOT NULL, expires_at timestamptz NOT NULL
	 );`)
	return e
}
func (b *UI) intake(w http.ResponseWriter, r *http.Request, u auth.Principal, in message) {
	c := Parse(in.Text)
	if c.Kind == "help" || c.Kind == "" {
		auth.JSON(w, 200, map[string]string{"type": "help", "summary": "项目助手 · 工作台", "tasks_md": "提交 Issue | 标题 | 问题说明\n\n更新进度 | 正常/有风险/阻塞 | 进度详情\n\n立项 | 标题 | 项目说明\n\n[打开项目看板](" + b.Origin + "/projects) · [Roadmap](" + b.Origin + "/roadmap)", "status_text": "仅支持授权测试项目的私聊。"})
		return
	}
	if !auth.IsUUID(in.DraftID) {
		auth.Problem(w, 400, "invalid", "草稿引用无效")
		return
	}
	scope := b.Project
	action := map[string]string{"issue": "project.issue", "update": "project.update", "propose": "project.propose"}[c.Kind]
	if c.Kind == "propose" {
		scope = ""
	} else if !auth.IsUUID(scope) {
		auth.Problem(w, 503, "unavailable", "测试项目未配置")
		return
	}
	if !b.Writer.Projects.Can(u, action, scope) {
		auth.Problem(w, 403, "denied", "无项目操作权限")
		return
	}
	input := projects.WriteInput{Title: c.Title, Body: c.Body, Health: c.Health}
	if _, e := b.Users.Pool.Exec(r.Context(), `INSERT INTO wb.bot_draft(id,account,project_id,kind,input,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '30 minutes') ON CONFLICT DO NOTHING`, in.DraftID, u.ID, scope, c.Kind, input); e != nil {
		auth.Problem(w, 503, "unavailable", "草稿无法保存")
		return
	}
	var recorded projects.WriteInput
	var kind string
	if e := b.Users.Pool.QueryRow(r.Context(), `SELECT kind,input FROM wb.bot_draft WHERE id=$1 AND account=$2 AND expires_at>now()`, in.DraftID, u.ID).Scan(&kind, &recorded); e != nil {
		auth.Problem(w, 409, "draft_unavailable", "当前草稿不可用")
		return
	}
	title := map[string]string{"issue": "提交测试 Issue", "update": "更新测试项目进度", "propose": "提交立项申请"}[kind]
	summary := recorded.Title
	if kind == "update" {
		summary = map[string]string{"onTrack": "正常", "atRisk": "有风险", "offTrack": "阻塞"}[recorded.Health]
	}
	text := inert(summary) + "\n\n" + inert(recorded.Body)
	auth.JSON(w, 200, map[string]string{"type": "draft", "summary": title, "tasks_md": text, "status_text": "仅写入测试环境；等待确认。"})
}
func inert(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "\\[", "]", "\\]", "@", "＠").Replace(s)
}
func (b *UI) execute(w http.ResponseWriter, r *http.Request, u auth.Principal, in message) {
	if !auth.IsUUID(in.DraftID) {
		auth.Problem(w, 400, "invalid", "草稿引用无效")
		return
	}
	var project, kind string
	var input projects.WriteInput
	e := b.Users.Pool.QueryRow(r.Context(), `SELECT project_id,kind,input FROM wb.bot_draft WHERE id=$1 AND account=$2 AND expires_at>now()`, in.DraftID, u.ID).Scan(&project, &kind, &input)
	if e != nil || (project != "" && project != b.Project) {
		auth.Problem(w, 403, "denied", "草稿已过期或不属于当前成员")
		return
	}
	res, e := b.Writer.Execute(r.Context(), u, project, kind, in.DraftID, input)
	if e != nil {
		status := 409
		if errors.Is(e, projects.ErrDenied) || errors.Is(e, pgx.ErrNoRows) {
			status = 403
		}
		auth.Problem(w, status, "operation_unverified", "操作未完成或结果待核对，请勿重复提交")
		return
	}
	auth.JSON(w, 200, res)
}
