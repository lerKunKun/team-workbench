package roadmap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"team-workbench/internal/auth"
	"testing"
)

const bridgeProject = "005c1599-ab62-4f11-b881-49a3c79e8158"
const sourceProject = "b8564d40-6030-4fe1-9bf5-279bab27ea83"
const sourceItem = "b8564d40-6030-4fe1-9bf5-279bab27ea84"
const actorID = "b8564d40-6030-4fe1-9bf5-279bab27ea85"
const bridgeSecret = "this-is-at-least-32-bytes-bridge-secret"

func TestExternalImportRequiresServiceCredential(t *testing.T) {
	h := auth.NewHandler(nil, nil, "https://kb.example.com", true)
	m := h.Router()
	NewBridge(nil, nil, bridgeSecret, "", sourceProject+"="+bridgeProject, actorID).Register(m, h)
	r := httptest.NewRequest("POST", "/api/integrations/roadmap/feedback", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("missing credential: %d", w.Code)
	}
}

func TestBridgeDisablesCollidingCredentials(t *testing.T) {
	h := auth.NewHandler(nil, nil, "https://kb.example.com", true)
	m := h.Router()
	NewBridge(nil, nil, bridgeSecret, bridgeSecret, "", actorID).Register(m, h)
	for _, path := range []string{"/feedback", "/sso/exchange"} {
		r := httptest.NewRequest("POST", "/api/integrations/roadmap"+path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+bridgeSecret)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Errorf("%s accepted a colliding key: %d", path, w.Code)
		}
	}
}

func TestExternalImportCannotTargetUnmappedProject(t *testing.T) {
	b := NewBridge(nil, nil, bridgeSecret, "", sourceProject+"="+bridgeProject, actorID)
	if _, err := b.importItem(context.Background(), ExternalItem{ProjectID: sourceItem, ItemID: sourceItem, Title: "A", Summary: "B"}); err != ErrInvalid {
		t.Fatalf("unknown mapping: %v", err)
	}
}

func TestDispatchUsesPrivateWebhookAndPreservesImportStatus(t *testing.T) {
	const hookKey = "legacy-hook-separate-credential-12345"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook/roadmap-feedback" || r.Header.Get("X-Workflow-Token") != hookKey {
			t.Errorf("incorrect internal routing")
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer upstream.Close()
	const dispatchKey = "different-external-dispatch-token-12345"
	h := auth.NewHandler(nil, nil, "https://kb.example.com", true)
	m := h.Router()
	NewBridge(nil, nil, bridgeSecret, "", "", actorID).withDispatch(upstream.URL, dispatchKey, hookKey).Register(m, h)
	for _, tc := range []struct {
		token string
		want  int
	}{{bridgeSecret, 401}, {dispatchKey, 409}} {
		r := httptest.NewRequest("POST", "/api/integrations/roadmap/dispatch", strings.NewReader(`{"projectId":"`+sourceProject+`","itemId":"`+sourceItem+`","title":"A","summary":"B","votes":1}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("dispatch returned %d, want %d", w.Code, tc.want)
		}
	}
}

func TestRoadmapBridgeIdempotencyAndSingleUseGrant(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = auth.NewStore(pool).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	srv := New(pool, p, nil)
	if err = srv.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wb.account(id,username,name,role,password_hash,salt) VALUES($1,'bridge-pilot','Pilot','admin',decode('00','hex'),decode('00','hex')) ON CONFLICT (id) DO NOTHING`, actorID); err != nil {
		t.Fatal(err)
	}
	b := NewBridge(srv, auth.NewStore(pool), bridgeSecret, "", sourceProject+"="+bridgeProject, actorID)
	if err = b.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	x := ExternalItem{ProjectID: sourceProject, ItemID: sourceItem, Title: "Public request", Summary: "Approved excerpt", Votes: 9}
	first, err := b.importItem(ctx, x)
	if err != nil {
		t.Fatal(err)
	}
	x.Title = "Changed later"
	again, err := b.importItem(ctx, x)
	if err != nil || again.ID != first.ID || again.Title != first.Title {
		t.Fatalf("duplicate import changed item: %+v %v", again, err)
	}
	h := auth.NewHandler(nil, nil, "https://kb.example.com", true)
	m := h.Router()
	b.Register(m, h)
	r := httptest.NewRequest("POST", "/api/integrations/roadmap/feedback", strings.NewReader(`{"projectId":"`+sourceProject+`","itemId":"`+sourceItem+`","title":"A","summary":"B"}`))
	r.Header.Set("Authorization", "Bearer "+bridgeSecret)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"id":"`+first.ID+`"}` {
		t.Fatalf("unexpected import response %d %s", w.Code, w.Body.String())
	}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	grant, err := b.createGrant(ctx, auth.Principal{ID: actorID, Role: "admin"}, challenge)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.exchangeGrant(ctx, grant, strings.Repeat("b", 43)); err != ErrHidden {
		t.Fatalf("wrong verifier: %v", err)
	}
	if err = b.exchangeGrant(ctx, grant, verifier); err != nil {
		t.Fatal(err)
	}
	if err = b.exchangeGrant(ctx, grant, verifier); err != ErrHidden {
		t.Fatalf("replay accepted: %v", err)
	}
}
