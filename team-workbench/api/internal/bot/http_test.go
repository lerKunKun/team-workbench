package bot

import (
	"bytes"
	"net/http/httptest"
	"team-workbench/internal/projects"
	"testing"
)

func TestInternalDMBoundary(t *testing.T) {
	h := NewUI(nil, projects.Writer{}, "test-corp", projects.DemoID, "a-strong-test-internal-shared-token", "https://workbench.example")
	for _, tc := range []struct {
		token, body string
		status      int
	}{{"", `{}`, 401}, {"a-strong-test-internal-shared-token", `{"channel":"2","corp":"test-corp","staff":"x","text":"帮助"}`, 403}, {"a-strong-test-internal-shared-token", `{"channel":"1","corp":"other","staff":"x","text":"帮助"}`, 403}} {
		r := httptest.NewRequest("POST", "/internal/bot/intake", bytes.NewBufferString(tc.body))
		r.Header.Set("X-Workbench-Bot-Token", tc.token)
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("got %d want %d", w.Code, tc.status)
		}
	}
}
