package automation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeModelOutput(t *testing.T) {
	id := uuid.NewString()
	model := parsedTasks{Summary: "check [link](https://example.com)", Tasks: []Task{
		{Category: "Bug", Title: "test", Description: "details", Assignee: "Alice", AssigneeID: uuid.NewString(), Priority: 9, Labels: []string{"Bug", "Bug", "Other"}},
	}}
	got, err := normalize(model, []Member{{Name: "Alice", LinearID: id}})
	if err != nil {
		t.Fatal(err)
	}
	x := got.Tasks[0]
	if x.AssigneeID != id || x.Priority != 3 || len(x.Labels) != 1 || x.Labels[0] != "Bug" {
		t.Fatalf("unexpected task %+v", x)
	}
	if strings.Contains(got.Summary, "[link](") {
		t.Fatal("card markdown contains unescaped link")
	}
	got, err = normalize(model, []Member{{Name: "Alice", LinearID: id}, {Name: "Alice", LinearID: uuid.NewString()}})
	if err != nil || got.Tasks[0].AssigneeID != "" {
		t.Fatal("ambiguous assignee must not be assigned")
	}
	if _, err := normalize(parsedTasks{}, nil); err == nil {
		t.Fatal("accepted empty batch")
	}
}

func TestIssueID(t *testing.T) {
	id := uuid.NewString()
	a, b := issueID(id, 0), issueID(id, 1)
	if a != issueID(id, 0) || a == b || uuid.MustParse(a).Version() != 4 {
		t.Fatalf("invalid stable issue ids: %s %s", a, b)
	}
}

func TestWebhookVerify(t *testing.T) {
	data := []byte(`{"action":"opened"}`)
	key := "test-signing-key"
	m := hmac.New(sha256.New, []byte(key))
	_, _ = m.Write(data)
	sig := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !verify(data, sig, key, "sha256=") || verify(data, sig, "wrong", "sha256=") || verify(append(data, ' '), sig, key, "sha256=") || verify(data, sig, key, "invalid=") || verify(data, sig, "", "sha256=") {
		t.Fatal("unexpected verification result")
	}
}

func TestDisabledAndUnauthorized(t *testing.T) {
	s := NewServer(Config{InternalToken: "local-token"}, nil).Handler()
	for _, tc := range []struct {
		method, path, token string
		code                int
	}{
		{"GET", "/internal/status", "", http.StatusUnauthorized},
		{"POST", "/internal/demo/intake", "local-token", http.StatusNotFound},
		{"POST", "/webhooks/github", "", http.StatusNotFound},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-Internal-Token", tc.token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("%s: got %d, want %d", tc.path, w.Code, tc.code)
		}
	}
}
