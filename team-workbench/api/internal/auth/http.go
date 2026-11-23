package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const cookieName = "wb_session"

type Handler struct {
	Store       *Store
	Policy      *Policy
	origin      string
	adminOrigin string
	secure      bool
	mu          sync.Mutex
	attempt     map[string]*attempt
	total       attempt
}
type attempt struct {
	hits  int
	until time.Time
}

func NewHandler(s *Store, p *Policy, origin string, secure bool) *Handler {
	return &Handler{Store: s, Policy: p, origin: origin, secure: secure, attempt: make(map[string]*attempt)}
}
func (h *Handler) AllowAdminOrigin(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || s == h.origin {
		return errors.New("invalid separate admin origin")
	}
	h.adminOrigin = s
	return nil
}
func (h *Handler) Router() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/auth/login", h.login)
	m.HandleFunc("POST /api/auth/logout", h.logout)
	m.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := h.User(w, r); ok {
			JSON(w, 200, u)
		}
	})
	m.HandleFunc("GET /api/admin/users", h.admin(h.users, "admin.users"))
	m.HandleFunc("POST /api/admin/users", h.admin(h.create, "admin.users"))
	m.HandleFunc("PATCH /api/admin/users/{id}", h.admin(h.update, "admin.users"))
	m.HandleFunc("PUT /api/admin/users/{id}/projects/{projectId}", h.admin(h.membership, "admin.users"))
	m.HandleFunc("GET /api/admin/audit", h.admin(h.audits, "admin.audit"))
	return m
}

func JSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func Problem(w http.ResponseWriter, code int, key, msg string) {
	JSON(w, code, map[string]any{"error": map[string]string{"code": key, "message": msg}})
}
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	var extra any
	if err := d.Decode(dst); err != nil || d.Decode(&extra) != io.EOF {
		Problem(w, 400, "invalid_body", "请求内容无效")
		return false
	}
	return true
}

func (h *Handler) SameOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	urlOrigin, err := url.Parse(origin)
	if err != nil || origin == "" || (origin != h.origin && origin != h.adminOrigin) || !strings.EqualFold(urlOrigin.Host, r.Host) {
		Problem(w, 403, "origin_denied", "请求来源不允许")
		return false
	}
	return true
}
func (h *Handler) CurrentUser(ctx context.Context, r *http.Request) (Principal, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || len(c.Value) != 43 || h.Store == nil {
		return Principal{}, ErrLogin
	}
	return h.Store.Session(ctx, c.Value)
}

func (h *Handler) User(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	u, err := h.CurrentUser(r.Context(), r)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrLogin) {
			Problem(w, 401, "login_required", "请先登录")
		} else {
			Problem(w, 503, "service_unavailable", "认证服务暂不可用")
		}
		return Principal{}, false
	}
	return u, true
}
func (h *Handler) cookie(w http.ResponseWriter, value string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: seconds})
}
func (h *Handler) permittedAttempt(user string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if !h.total.until.After(now) {
		h.total = attempt{until: now.Add(time.Minute)}
	}
	if h.total.hits >= 60 {
		return false
	}
	h.total.hits++
	if len(h.attempt) >= 1000 {
		for k, v := range h.attempt {
			if !v.until.After(now) {
				delete(h.attempt, k)
			}
		}
		if len(h.attempt) >= 1000 {
			return false
		}
	}
	k := strings.ToLower(strings.TrimSpace(user))
	a := h.attempt[k]
	if a == nil || !a.until.After(now) {
		a = &attempt{until: now.Add(time.Minute)}
		h.attempt[k] = a
	}
	if a.hits >= 8 {
		return false
	}
	a.hits++
	return true
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.SameOrigin(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !Decode(w, r, &in) {
		return
	}
	if len(in.Username) < 3 || len(in.Username) > 64 || len(in.Password) < 1 || len(in.Password) > 256 {
		Problem(w, 400, "invalid_credentials", "请输入账号和密码")
		return
	}
	if !h.permittedAttempt(in.Username) {
		w.Header().Set("Retry-After", "60")
		Problem(w, 429, "too_many_attempts", "尝试次数过多，请稍候")
		return
	}
	u, err := h.Store.Login(r.Context(), in.Username, in.Password, "")
	if errors.Is(err, ErrLogin) || errors.Is(err, pgx.ErrNoRows) {
		Problem(w, 401, "invalid_credentials", "账号或密码不正确")
		return
	}
	if err != nil {
		Problem(w, 503, "service_unavailable", "认证服务暂不可用")
		return
	}
	token, err := h.Store.StartSession(r.Context(), u.ID)
	if err != nil {
		Problem(w, 503, "service_unavailable", "认证服务暂不可用")
		return
	}
	h.cookie(w, token, 43200)
	JSON(w, 200, u)
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.SameOrigin(w, r) {
		return
	}
	if c, err := r.Cookie(cookieName); err == nil && len(c.Value) == 43 {
		if h.Store.EndSession(r.Context(), c.Value) != nil {
			Problem(w, 503, "service_unavailable", "退出暂未完成，请重试")
			return
		}
	}
	h.cookie(w, "", -1)
	w.WriteHeader(204)
}

func (h *Handler) admin(fn func(http.ResponseWriter, *http.Request, Principal), grant string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && !h.SameOrigin(w, r) {
			return
		}
		u, ok := h.User(w, r)
		if !ok {
			return
		}
		if !h.Policy.Allows(u, grant, "") {
			Problem(w, 403, "forbidden", "无管理权限")
			return
		}
		fn(w, r, u)
	}
}
func (h *Handler) users(w http.ResponseWriter, r *http.Request, _ Principal) {
	v, err := h.Store.Accounts(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	JSON(w, 200, v)
}
func (h *Handler) audits(w http.ResponseWriter, r *http.Request, _ Principal) {
	v, err := h.Store.Audits(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	JSON(w, 200, v)
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request, u Principal) {
	var in struct{ Username, Name, Role, Password string }
	if !Decode(w, r, &in) {
		return
	}
	a, err := h.Store.CreateAccount(r.Context(), u.ID, in.Username, in.Name, in.Role, in.Password)
	if err != nil {
		storeError(w, err)
		return
	}
	JSON(w, 201, Account{Principal: a, Active: true})
}
func (h *Handler) update(w http.ResponseWriter, r *http.Request, u Principal) {
	var in struct {
		Role   string
		Active *bool
	}
	if !Decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if !IsUUID(id) || in.Active == nil {
		storeError(w, ErrInput)
		return
	}
	if err := h.Store.SetAccount(r.Context(), u.ID, id, in.Role, *in.Active); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) membership(w http.ResponseWriter, r *http.Request, u Principal) {
	var in struct{ Role *string }
	if !Decode(w, r, &in) {
		return
	}
	id, p := r.PathValue("id"), r.PathValue("projectId")
	if !IsUUID(id) || !IsUUID(p) || in.Role == nil {
		storeError(w, ErrInput)
		return
	}
	role := *in.Role
	if err := h.Store.SetMembership(r.Context(), u.ID, id, p, role); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func IsUUID(s string) bool { u, err := uuid.Parse(s); return err == nil && u.String() == s }
func storeError(w http.ResponseWriter, err error) {
	var e *pgconn.PgError
	switch {
	case errors.Is(err, ErrInput):
		Problem(w, 400, "invalid_fields", "账号或授权字段无效")
	case errors.Is(err, ErrLastAdmin):
		Problem(w, 409, "last_admin", "至少保留一名启用的管理员")
	case errors.Is(err, pgx.ErrNoRows), errors.As(err, &e) && e.Code == "23503":
		Problem(w, 404, "not_found", "目标不存在")
	case errors.As(err, &e) && e.Code == "23505":
		Problem(w, 409, "duplicate", "账号或绑定已存在")
	default:
		Problem(w, 503, "service_unavailable", "暂时无法完成，请稍后重试")
	}
}
