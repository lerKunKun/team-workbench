package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDingLoginOptionsAreScopedToHost(t *testing.T) {
	h := NewHandler(nil, nil, "https://kb.example.com", true)
	if err := h.AllowAdminOrigin("https://admin.example.com"); err != nil {
		t.Fatal(err)
	}
	mux := h.Router()
	h.RegisterDing(mux, DingConfig{Corp: "test-corp", Client: "test-client", Secret: "test-secret", Redirect: "https://kb.example.com" + authCallback})
	for _, tc := range []struct {
		host        string
		ding, admin bool
	}{
		{"kb.example.com", true, false},
		{"admin.example.com", false, true},
		{"other.example.com", false, false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", "https://"+tc.host+"/api/auth/options", nil))
			var got struct {
				DingQr, DingSso, AdminPortal, Password bool
				CorpID, ClientID                       string
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
				t.Fatalf("options response: status=%d err=%v", w.Code, err)
			}
			if !got.Password || got.DingQr != tc.ding || got.DingSso != tc.ding || got.AdminPortal != tc.admin || (!tc.ding && (got.CorpID != "" || got.ClientID != "")) {
				t.Fatalf("wrong login options: %+v", got)
			}
		})
	}
}

func TestDingReadFailureKeepsStatusWithoutProviderBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"Forbidden.AccessDenied.AccessTokenPermissionDenied","message":"mock-secret-that-must-not-be-logged"}`))
	}))
	defer s.Close()
	var out struct{}
	err := dingCall(context.Background(), "GET", s.URL, "", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "mock-secret") {
		t.Fatalf("expected sanitized HTTP status, got %v", err)
	}
}
