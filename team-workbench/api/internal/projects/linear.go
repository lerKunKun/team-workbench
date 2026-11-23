package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"team-workbench/internal/auth"
	"time"
)

type Linear struct {
	key, team string
	ids       []string
	allowed   map[string]bool
	http      *http.Client
}

func NewLinear(key, team, csv string) *Linear {
	if key == "" || !auth.IsUUID(team) {
		return nil
	}
	l := &Linear{key: key, team: team, allowed: map[string]bool{}, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, id := range strings.Split(csv, ",") {
		id = strings.TrimSpace(id)
		if !auth.IsUUID(id) {
			return nil
		}
		if !l.allowed[id] {
			l.ids = append(l.ids, id)
			l.allowed[id] = true
		}
	}
	if len(l.ids) == 0 || len(l.ids) > 20 {
		return nil
	}
	return l
}
func (*Linear) Kind() string { return "linear" }
func (l *Linear) query(ctx context.Context, q string, v map[string]any, out any) error {
	b, err := json.Marshal(map[string]any{"query": q, "variables": v})
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, "POST", "https://api.linear.app/graphql", bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", l.key)
	resp, err := l.http.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	var e struct {
		Data   json.RawMessage
		Errors []json.RawMessage
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&e) != nil || len(e.Errors) > 0 || len(e.Data) == 0 || string(e.Data) == "null" || json.Unmarshal(e.Data, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func goodURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != "linear.app" || u.User != nil {
		return ""
	}
	return s
}
func (l *Linear) Projects(ctx context.Context) ([]Project, error) {
	var data struct {
		Projects struct {
			Nodes []struct {
				ID, Name, Description, URL string
				Lead                       *struct{ Name string }
				Teams                      struct{ Nodes []struct{ ID string } }
			}
		}
	}
	err := l.query(ctx, `query($ids:[ID!]!){projects(first:25,filter:{id:{in:$ids}}){nodes{id name description url lead{name} teams(first:25){nodes{id}}}}}`, map[string]any{"ids": l.ids}, &data)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, p := range data.Projects.Nodes {
		if !l.allowed[p.ID] {
			continue
		}
		for _, t := range p.Teams.Nodes {
			if t.ID == l.team {
				owner := ""
				if p.Lead != nil {
					owner = p.Lead.Name
				}
				out = append(out, Project{p.ID, p.Name, p.Description, owner, goodURL(p.URL)})
				break
			}
		}
	}
	return out, nil
}
func (l *Linear) Issues(ctx context.Context, project string) ([]Issue, error) {
	if !l.allowed[project] {
		return nil, ErrUnavailable
	}
	var after any
	out := []Issue{}
	seen := map[string]bool{}
	for page := 0; page < 5; page++ {
		var data struct {
			Issues struct {
				Nodes []struct {
					ID, Identifier, Title, DueDate, URL string
					Project                             *struct{ ID string }
					Team                                struct{ ID string }
					State                               struct{ Name, Type string }
					Assignee                            *struct{ Name string }
					InverseRelations                    struct {
						Nodes []struct {
							Type  string
							Issue struct {
								Identifier string
								State      struct{ Type string }
							}
						}
					}
				}
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		err := l.query(ctx, `query($id:ID!,$team:ID!,$after:String){issues(first:100,after:$after,filter:{project:{id:{eq:$id}},team:{id:{eq:$team}}}){nodes{id identifier title dueDate url project{id} team{id} state{name type} assignee{name} inverseRelations(first:15){nodes{type issue{identifier state{type}}}}} pageInfo{hasNextPage endCursor}}}`, map[string]any{"id": project, "team": l.team, "after": after}, &data)
		if err != nil {
			return nil, err
		}
		for _, i := range data.Issues.Nodes {
			if seen[i.ID] || i.Team.ID != l.team || i.Project == nil || i.Project.ID != project {
				continue
			}
			seen[i.ID] = true
			owner, blocker := "", ""
			if i.Assignee != nil {
				owner = i.Assignee.Name
			}
			for _, r := range i.InverseRelations.Nodes {
				if r.Type == "blocks" && r.Issue.State.Type != "completed" && r.Issue.State.Type != "canceled" {
					blocker = "等待 " + r.Issue.Identifier
					break
				}
			}
			out = append(out, Issue{i.Identifier, i.Title, project, owner, i.DueDate, i.State.Name, blocker, goodURL(i.URL), i.State.Type == "completed" || i.State.Type == "canceled"})
		}
		pi := data.Issues.PageInfo
		if !pi.HasNextPage {
			return out, nil
		}
		if pi.EndCursor == "" || pi.EndCursor == after {
			return nil, ErrUnavailable
		}
		after = pi.EndCursor
	}
	return nil, ErrUnavailable
}
