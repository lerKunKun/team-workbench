package auth

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
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const authCallback = "/api/auth/dingtalk/callback"
const dingStateCookie = "wb_ding_state"

var errDingAuth = errors.New("DingTalk authentication failed")

type DingConfig struct{ Corp, Client, Secret, Redirect string }

func SafeNext(s string) string {
	u, e := url.Parse(s)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(s, "/") || strings.ContainsAny(u.Path, "\\\r\n\t") || path.Clean(u.Path) != u.Path {
		return "/projects"
	}
	if u.Path == "/api/integrations/roadmap/sso/authorize" && u.RawQuery != "" {
		return u.RequestURI()
	}
	for _, root := range []string{"/projects", "/roadmap", "/admin"} {
		if u.Path == root || strings.HasPrefix(u.Path, root+"/") {
			return u.RequestURI()
		}
	}
	return "/projects"
}
func (h *Handler) RegisterDing(m *http.ServeMux, c DingConfig) {
	sso := c.Corp != "" && c.Client != "" && c.Secret != "" && h.secure
	qr := sso && c.Redirect == h.origin+authCallback
	workbenchURL, _ := url.Parse(h.origin)
	adminURL, _ := url.Parse(h.adminOrigin)
	m.HandleFunc("GET /api/auth/options", func(w http.ResponseWriter, r *http.Request) {
		workbench := workbenchURL != nil && workbenchURL.Host != "" && strings.EqualFold(r.Host, workbenchURL.Host)
		admin := adminURL != nil && adminURL.Host != "" && strings.EqualFold(r.Host, adminURL.Host)
		o := map[string]any{"password": true, "dingSso": sso && workbench, "dingQr": qr && workbench, "adminPortal": admin}
		if sso && workbench {
			o["corpId"] = c.Corp
			o["clientId"] = c.Client
		}
		JSON(w, 200, o)
	})
	if !sso {
		return
	}
	m.HandleFunc("POST /api/auth/dingtalk/sso", func(w http.ResponseWriter, r *http.Request) {
		if !h.SameOrigin(w, r) {
			return
		}
		var x struct{ Code string }
		if !Decode(w, r, &x) {
			return
		}
		if !validCode(x.Code) || !h.permittedAttempt("ding-sso") {
			Problem(w, 400, "ding_invalid", "免登码无效或请求过多")
			return
		}
		ctx, stop := context.WithTimeout(r.Context(), 22*time.Second)
		defer stop()
		id, err := c.staff(ctx, x.Code)
		if err == nil {
			var u Principal
			u, err = h.Store.dingAccount(ctx, c.Corp, "staff", id)
			if err == nil {
				var tok string
				tok, err = h.Store.StartSession(ctx, u.ID)
				if err == nil {
					h.cookie(w, tok, 43200)
					JSON(w, 200, u)
					return
				}
			}
		}
		Problem(w, 403, "ding_failed", "钉钉身份未通过验证或尚未获授权")
	})
	if !qr {
		return
	}
	m.HandleFunc("GET /api/auth/dingtalk/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !h.permittedAttempt("ding-start") {
			Problem(w, 429, "too_many_attempts", "尝试次数过多，请稍候")
			return
		}
		state, err := h.Store.newDingState(r.Context(), SafeNext(r.URL.Query().Get("next")))
		if err != nil {
			Problem(w, 503, "unavailable", "登录暂不可用")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dingStateCookie, Value: state, Path: authCallback, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
		query := url.Values{"redirect_uri": {c.Redirect}, "response_type": {"code"}, "client_id": {c.Client}, "scope": {"openid corpid"}, "corpId": {c.Corp}, "state": {state}, "prompt": {"consent"}}
		http.Redirect(w, r, "https://login.dingtalk.com/oauth2/auth?"+query.Encode(), http.StatusSeeOther)
	})
	m.HandleFunc("GET "+authCallback, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		fail := func(stage string, err error) {
			// Only static stages and errors constructed by the provider wrapper are safe to log.
			if err == nil {
				log.Printf("DingTalk QR callback rejected: stage=%s", stage)
			} else {
				log.Printf("DingTalk QR callback rejected: stage=%s result=%s", stage, err)
			}
			http.Redirect(w, r, "/login?error=dingtalk", http.StatusSeeOther)
		}
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("authCode")
		if code == "" {
			code = r.URL.Query().Get("code")
		}
		cookie, err := r.Cookie(dingStateCookie)
		if err != nil || len(state) != 43 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
			fail("state_cookie", nil)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dingStateCookie, Path: authCallback, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		next, err := h.Store.takeDingState(r.Context(), state)
		if err != nil {
			fail("expired_or_consumed_state", nil)
			return
		}
		if !validCode(code) {
			fail("invalid_code", nil)
			return
		}
		ctx, stop := context.WithTimeout(r.Context(), 22*time.Second)
		defer stop()
		id, err := c.union(ctx, code)
		if err != nil {
			fail("provider_identity", err)
			return
		}
		u, err := h.Store.dingAccount(ctx, c.Corp, "union", id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				fail("unmapped_or_inactive_account", nil)
			} else {
				fail("account_lookup", nil)
			}
			return
		}
		token, err := h.Store.StartSession(ctx, u.ID)
		if err != nil {
			fail("session_create", nil)
			return
		}
		h.cookie(w, token, 43200)
		http.Redirect(w, r, SafeNext(next), http.StatusSeeOther)
	})
}
func validCode(code string) bool {
	return len(code) >= 4 && len(code) <= 512 && !strings.ContainsAny(code, "\t\r\n ")
}
func (s *Store) newDingState(ctx context.Context, next string) (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(state))
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `DELETE FROM wb.ding_state WHERE expires_at<now()`); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO wb.ding_state VALUES($1,$2,now()+interval '5 minutes')`, sum[:], next); e != nil {
		return "", e
	}
	return state, tx.Commit(ctx)
}
func (s *Store) takeDingState(ctx context.Context, state string) (string, error) {
	var next string
	sum := sha256.Sum256([]byte(state))
	e := s.Pool.QueryRow(ctx, `DELETE FROM wb.ding_state WHERE token_hash=$1 AND expires_at>now() RETURNING next_path`, sum[:]).Scan(&next)
	return next, e
}
func (s *Store) dingAccount(ctx context.Context, corp, kind, id string) (Principal, error) {
	if id == "" {
		return Principal{}, errDingAuth
	}
	var u string
	e := s.Pool.QueryRow(ctx, `SELECT account_id::text FROM wb.ding_identity WHERE corp_id=$1 AND identity_kind=$2 AND external_id=$3`, corp, kind, id).Scan(&u)
	if e != nil {
		return Principal{}, e
	}
	return s.Principal(ctx, u)
}
func (s *Store) DingStaff(ctx context.Context, corp, id string) (Principal, error) {
	return s.dingAccount(ctx, corp, "staff", id)
}

var dingHTTP = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func dingCall(ctx context.Context, method, address, token string, body any, dst any) error {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return errDingAuth
		}
		reader = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, address, reader)
	if e != nil {
		return errDingAuth
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("x-acs-dingtalk-access-token", token)
	}
	res, e := dingHTTP.Do(r)
	if e != nil {
		return errDingAuth
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("provider HTTP %d", res.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(dst) != nil {
		return errDingAuth
	}
	return nil
}
func (c DingConfig) staff(ctx context.Context, code string) (string, error) {
	var token struct{ AccessToken string }
	if e := dingCall(ctx, "POST", "https://api.dingtalk.com/v1.0/oauth2/accessToken", "", map[string]string{"appKey": c.Client, "appSecret": c.Secret}, &token); e != nil || token.AccessToken == "" {
		return "", errDingAuth
	}
	var result struct {
		ErrCode *int `json:"errcode"`
		Result  struct {
			ID string `json:"userid"`
		}
	}
	if e := dingCall(ctx, "POST", "https://oapi.dingtalk.com/topapi/v2/user/getuserinfo?access_token="+url.QueryEscape(token.AccessToken), "", map[string]string{"code": code}, &result); e != nil || result.ErrCode == nil || *result.ErrCode != 0 || result.Result.ID == "" {
		return "", errDingAuth
	}
	return result.Result.ID, nil
}
func (c DingConfig) union(ctx context.Context, code string) (string, error) {
	var token struct{ AccessToken, CorpID string }
	if e := dingCall(ctx, "POST", "https://api.dingtalk.com/v1.0/oauth2/userAccessToken", "", map[string]string{"clientId": c.Client, "clientSecret": c.Secret, "code": code, "grantType": "authorization_code"}, &token); e != nil {
		return "", fmt.Errorf("user_token: %w", e)
	}
	if token.AccessToken == "" || token.CorpID != c.Corp {
		return "", errors.New("user_token_missing_or_corp_mismatch")
	}
	var user struct{ UnionID string }
	if e := dingCall(ctx, "GET", "https://api.dingtalk.com/v1.0/contact/users/me", token.AccessToken, nil, &user); e != nil {
		return "", fmt.Errorf("contact_me: %w", e)
	}
	if user.UnionID == "" {
		return "", errors.New("contact_me_missing_union_id")
	}
	return user.UnionID, nil
}
