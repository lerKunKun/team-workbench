package auth

import (
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
)

type Principal struct {
	ID       string            `json:"id"`
	Username string            `json:"username"`
	Name     string            `json:"name"`
	Role     string            `json:"role"`
	Projects map[string]string `json:"projects"`
}

// Project assignments come from the database, never from the browser or bot payload.
// Evaluate role grants with Casbin. Resolve the caller's exact project-domain
// role per request, so revocation does not depend on an enforcer cache refresh.
type Policy struct{ engine *casbin.SyncedEnforcer }

func NewPolicy() (*Policy, error) {
	m, err := model.NewModelFromString(`[request_definition]
r = sub, dom, act
[policy_definition]
p = sub, dom, act
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = r.sub == p.sub && r.dom == p.dom && r.act == p.act`)
	if err != nil {
		return nil, err
	}
	e, err := casbin.NewSyncedEnforcer(m)
	if err != nil {
		return nil, err
	}
	grants := [][]string{
		{"admin", "system", "admin.users"}, {"admin", "system", "admin.audit"},
		{"admin", "system", "project.approve"}, {"admin", "system", "project.propose"}, {"member", "system", "project.propose"},
	}
	for _, role := range []string{"admin", "manager", "member"} {
		for _, action := range []string{"project.read", "project.issue", "project.update", "roadmap.read", "feedback.create"} {
			grants = append(grants, []string{role, "project", action})
		}
		if role != "member" {
			grants = append(grants, []string{role, "project", "roadmap.manage"})
		}
	}
	if _, err := e.AddPolicies(grants); err != nil {
		return nil, err
	}
	return &Policy{e}, nil
}

func (p *Policy) Allows(who Principal, action, projectID string) bool {
	if who.ID == "" || (who.Role != "admin" && who.Role != "member") {
		return false
	}
	domain, role := "system", who.Role
	if projectID != "" {
		domain = "project"
		if role != "admin" {
			role = who.Projects[projectID]
			if role != "member" && role != "manager" {
				return false
			}
		}
	}
	allowed, err := p.engine.Enforce(role, domain, action)
	return err == nil && allowed
}
