package roadmap

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"team-workbench/internal/auth"
	"testing"
)

func TestConfirmIdempotencyAndPlanScope(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated WORKBENCH_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := New(p, nil, nil)
	if err = auth.NewStore(p).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const project = "e98503aa-4127-4f1e-8996-a82239824aa9"
	u := auth.Principal{ID: "owner", Role: "member", Projects: map[string]string{project: "manager"}}
	i, err := s.Submit(ctx, u, project, "Test item", "User feedback", "web")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Move(ctx, u, i.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err = s.Move(ctx, u, i.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SavePlan(ctx, u, i.ID, "# Plan\n\nManual draft.", false); err != nil || n != 1 {
		t.Fatalf("draft: %d %v", n, err)
	}
	if body, err := s.Download(ctx, u, i.ID, 1); err != nil || body != "# Plan\n\nManual draft." {
		t.Fatalf("download %q %v", body, err)
	}
	if _, err = s.Download(ctx, auth.Principal{ID: "outsider", Role: "member"}, i.ID, 1); err != ErrHidden {
		t.Fatalf("wrong scope: %v", err)
	}
	if _, err = s.SavePlan(ctx, u, i.ID, "", true); err != ErrNoModel {
		t.Fatalf("wanted no-model state: %v", err)
	}
}
