package projects

import (
	"context"
	"errors"
	"team-workbench/internal/auth"
	"testing"
	"time"
)

func TestRequestKey(t *testing.T) {
	if _, err := RequestKey("actor", "issue", DemoID, "anything"); err == nil {
		t.Fatal("invalid idempotency key accepted")
	}
	const valid = "1ffa29c0-1ccc-41bb-8ef9-028d7518c62c"
	a, err := RequestKey("actor", "issue", DemoID, valid)
	if err != nil || !auth.IsUUID(a) {
		t.Fatalf("invalid request reference: %v", err)
	}
	b, _ := RequestKey("actor", "issue", DemoID, valid)
	c, _ := RequestKey("other", "issue", DemoID, valid)
	if a != b || a == c {
		t.Fatal("request identity not stable and account scoped")
	}
}

func TestShanghaiWeek(t *testing.T) {
	for _, c := range []struct{ time, want string }{{"2026-11-22T15:59:59Z", "2026-11-16"}, {"2026-11-22T16:00:00Z", "2026-11-23"}} {
		at, _ := time.Parse(time.RFC3339, c.time)
		if got := WeekStart(at); got != c.want {
			t.Errorf("%s: %s", c.time, got)
		}
	}
}
func TestScopedProjects(t *testing.T) {
	p, _ := auth.NewPolicy()
	s := Service{Policy: p, Source: NewDemo()}
	u := auth.Principal{ID: "member", Role: "member", Projects: map[string]string{DemoID: "member"}}
	items, err := s.Visible(context.Background(), u)
	if err != nil || len(items) != 1 || items[0].ID != DemoID {
		t.Fatalf("visible=%+v err=%v", items, err)
	}
	if s.Can(u, "project.read", "7e200b76-933f-4dd3-9d53-85b8aacb43f2") {
		t.Fatal("unassigned project permitted")
	}
}
func TestUnknownSource(t *testing.T) {
	p, _ := auth.NewPolicy()
	s := Service{Policy: p}
	if _, err := s.Visible(context.Background(), auth.Principal{ID: "admin", Role: "admin"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
