package roadmap

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"team-workbench/internal/auth"
)

func (s *Service) Register(mux *http.ServeMux, h *auth.Handler) {
	wrap := func(f func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" && !h.SameOrigin(w, r) {
				return
			}
			u, ok := h.User(w, r)
			if ok {
				f(w, r, u)
			}
		}
	}
	mux.HandleFunc("GET /api/roadmap", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		if u.Role != "admin" {
			allowed := false
			for p := range u.Projects {
				if h.Policy.Allows(u, "roadmap.manage", p) {
					allowed = true
					break
				}
			}
			if !allowed {
				auth.Problem(w, 403, "forbidden", "无路线图管理权限")
				return
			}
		}
		x, e := s.List(r.Context(), u)
		reply(w, 200, x, e)
	}))
	mux.HandleFunc("GET /api/roadmap/{id}", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		x, e := s.Details(r.Context(), u, r.PathValue("id"))
		reply(w, 200, x, e)
	}))
	mux.HandleFunc("PATCH /api/roadmap/{id}/status", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		var in struct{ State string }
		if !auth.Decode(w, r, &in) {
			return
		}
		reply(w, 200, map[string]bool{"ok": true}, s.Move(r.Context(), u, r.PathValue("id"), in.State))
	}))
	mux.HandleFunc("POST /api/roadmap/{id}/merge-feedback", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		var in struct{ SourceID string }
		if !auth.Decode(w, r, &in) {
			return
		}
		reply(w, 200, map[string]bool{"ok": true}, s.Merge(r.Context(), u, r.PathValue("id"), in.SourceID))
	}))
	mux.HandleFunc("POST /api/roadmap/{id}/plans", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		var in struct {
			Body     string
			Generate bool
		}
		if !auth.Decode(w, r, &in) {
			return
		}
		n, e := s.SavePlan(r.Context(), u, r.PathValue("id"), in.Body, in.Generate)
		status := 201
		if in.Generate {
			status = 202
		}
		reply(w, status, map[string]int{"version": n}, e)
	}))
	mux.HandleFunc("GET /api/roadmap/{id}/plans/{version}/download", wrap(func(w http.ResponseWriter, r *http.Request, u auth.Principal) {
		n, e := strconv.Atoi(r.PathValue("version"))
		if e != nil || n < 1 {
			reply(w, 0, nil, ErrHidden)
			return
		}
		body, e := s.Download(r.Context(), u, r.PathValue("id"), n)
		if e != nil {
			reply(w, 0, nil, e)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="plan.md"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, body)
	}))
}
func reply(w http.ResponseWriter, status int, x any, err error) {
	switch {
	case err == nil:
		auth.JSON(w, status, x)
	case errors.Is(err, ErrHidden):
		auth.Problem(w, 404, "not_found", "该条目不存在或不可访问")
	case errors.Is(err, ErrInvalid):
		auth.Problem(w, 400, "bad_input", "请检查填写内容")
	case errors.Is(err, ErrConflict):
		auth.Problem(w, 409, "state_conflict", "当前状态不允许此操作")
	case errors.Is(err, ErrNoModel):
		auth.Problem(w, 503, "model_unavailable", "模型未配置，可先保存人工草稿")
	default:
		auth.Problem(w, 503, "unavailable", "暂不可用，请稍后重试")
	}
}
