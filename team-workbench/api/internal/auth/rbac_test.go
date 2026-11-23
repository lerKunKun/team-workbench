package auth

import "testing"

func TestProjectBoundary(t *testing.T) {
	policy, err := NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	a := Principal{ID: "u", Role: "member", Projects: map[string]string{"a": "manager", "b": "member"}}
	admin := Principal{ID: "admin", Role: "admin"}
	for _, tc := range []struct {
		who           Principal
		action, scope string
		allowed       bool
	}{
		{a, "project.read", "a", true}, {a, "roadmap.manage", "a", true},
		{a, "project.read", "b", true}, {a, "roadmap.manage", "b", false},
		{a, "project.read", "elsewhere", false}, {a, "project.issue", "elsewhere", false},
		{a, "admin.users", "a", false}, {a, "admin.users", "", false},
		{a, "project.propose", "", true}, {a, "project.approve", "", false},
		{admin, "project.approve", "", true}, {admin, "admin.users", "", true},
		{admin, "misspelled.action", "a", false}, {Principal{}, "project.read", "a", false},
	} {
		if got := policy.Allows(tc.who, tc.action, tc.scope); got != tc.allowed {
			t.Errorf("%s %s %q: got %v want %v", tc.who.ID, tc.action, tc.scope, got, tc.allowed)
		}
	}
}
