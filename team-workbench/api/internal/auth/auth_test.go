package auth

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoginGuards(t *testing.T) {
	h := NewHandler(nil, nil, "https://workbench.example", true)
	for _, tc := range []struct {
		method, path, origin string
		want                 int
	}{{"GET", "/api/auth/me", "", 401}, {"POST", "/api/auth/login", "https://evil.example", 403}, {"POST", "/api/auth/login", "", 403}, {"POST", "/api/auth/login", "https://workbench.example", 400}} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{}`))
		r.Host = "workbench.example"
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s origin=%q: %d want %d", tc.method, tc.path, tc.origin, w.Code, tc.want)
		}
	}
}

func TestAdminOriginCannotMutateTeamHost(t *testing.T) {
	h := NewHandler(nil, nil, "https://kb.example.com", true)
	if err := h.AllowAdminOrigin("https://admin.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		host, origin string
		want         bool
	}{
		{"kb.example.com", "https://kb.example.com", true},
		{"admin.example.com", "https://admin.example.com", true},
		{"kb.example.com", "https://admin.example.com", false},
		{"admin.example.com", "https://kb.example.com", false},
		{"admin.example.com", "https://evil.example.com", false},
	} {
		r := httptest.NewRequest("POST", "https://"+c.host+"/api/auth/login", nil)
		r.Header.Set("Origin", c.origin)
		if got := h.SameOrigin(httptest.NewRecorder(), r); got != c.want {
			t.Errorf("%s from %s: %v", c.host, c.origin, got)
		}
	}
}

func TestDingTalkRedirectBoundary(t *testing.T) {
	for in, want := range map[string]string{"/roadmap/manage": "/roadmap/manage", "/projects?week=2026-11-23": "/projects?week=2026-11-23", "//example.org/path": "/projects", "https://example.org": "/projects", "/projects/../login": "/projects", "/projects/%5cevil": "/projects"} {
		if got := SafeNext(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestRevokedSessionAndLastAdmin(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated WORKBENCH_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := NewStore(pool)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := s.newDingState(ctx, "/roadmap")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.takeDingState(ctx, state); err != nil || got != "/roadmap" {
		t.Fatalf("state not consumed: %q %v", got, err)
	}
	if _, err = s.takeDingState(ctx, state); err == nil {
		t.Fatal("replayed DingTalk state accepted")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `TRUNCATE wb.audit,wb.account CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAccount(ctx, "", "admin-test", "Admin", "admin", "a strong demo test password")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateAccount(ctx, a.ID, "member-test", "Member", "member", "another demo test password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.StartSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Session(ctx, token); err != nil || got.ID != u.ID {
		t.Fatalf("valid session: %v", err)
	}
	if err = s.SetAccount(ctx, a.ID, u.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, token); err == nil {
		t.Fatal("disabled user's token retained access")
	}
	if err = s.SetAccount(ctx, a.ID, a.ID, "member", true); err != ErrLastAdmin {
		t.Fatalf("last admin: %v", err)
	}
	if _, err = s.Login(ctx, "admin-test", "wrong password", "test"); err != ErrLogin {
		t.Fatalf("wrong password: %v", err)
	}
}
