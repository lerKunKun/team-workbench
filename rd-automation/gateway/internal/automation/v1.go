package automation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func verify(body []byte, value, secret, prefix string) bool {
	if secret == "" || !strings.HasPrefix(value, prefix) {
		return false
	}
	sig, err := hex.DecodeString(value[len(prefix):])
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write(body)
	return hmac.Equal(sig, m.Sum(nil))
}

func (s *Server) webhookBody(w http.ResponseWriter, r *http.Request, secret, header, prefix string) ([]byte, bool) {
	if secret == "" || s.cfg.Demo || !s.cfg.DingReady() || !s.cfg.Groups[s.cfg.V1Group] {
		fail(w, 404, "webhook disabled")
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	if err != nil {
		fail(w, 413, "body too large or unreadable")
		return nil, false
	}
	if !verify(b, r.Header.Get(header), secret, prefix) {
		fail(w, 401, "invalid signature")
		return nil, false
	}
	return b, true
}

func linkFor(s, host string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || strings.ContainsAny(s, "()<> \r\n") {
		return ""
	}
	return "\n[查看详情](" + s + ")"
}

func (s *Server) notify(w http.ResponseWriter, r *http.Request, source, key, kind, title, text string) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.st.event(ctx, source, key, kind, "notify", Payload{Group: s.cfg.V1Group, Title: title, Text: text}); err != nil {
		fail(w, 503, "outbox unavailable")
		return
	}
	status := http.StatusAccepted
	if source == "linear" {
		status = http.StatusOK
	} // Linear requires 200, not any 2xx.
	write(w, status, map[string]bool{"accepted": true})
}

func (s *Server) github(w http.ResponseWriter, r *http.Request) {
	b, ok := s.webhookBody(w, r, s.cfg.GitHubSecret, "X-Hub-Signature-256", "sha256=")
	if !ok {
		return
	}
	var x struct {
		Action     string
		Repository struct {
			FullName string `json:"full_name"`
		}
		PullRequest struct {
			Title   string
			HTMLURL string `json:"html_url"`
			Merged  bool
		} `json:"pull_request"`
		WorkflowRun struct {
			Name, Conclusion string
			HTMLURL          string `json:"html_url"`
		} `json:"workflow_run"`
	}
	if json.Unmarshal(b, &x) != nil {
		fail(w, 400, "invalid payload")
		return
	}
	kind := r.Header.Get("X-GitHub-Event")
	if kind == "ping" || !s.cfg.Repositories[x.Repository.FullName] {
		write(w, 200, map[string]bool{"ignored": true})
		return
	}
	var title, text string
	switch {
	case kind == "pull_request" && (x.Action == "opened" || x.Action == "reopened" || x.Action == "ready_for_review" || (x.Action == "closed" && x.PullRequest.Merged)):
		title = "GitHub PR：" + inline(compact(x.PullRequest.Title, 160))
		text = fmt.Sprintf("**%s**\n- 仓库：%s | %s%s", title, inline(x.Repository.FullName), inline(x.Action), linkFor(x.PullRequest.HTMLURL, "github.com"))
	case kind == "workflow_run" && x.Action == "completed" && (x.WorkflowRun.Conclusion == "failure" || x.WorkflowRun.Conclusion == "timed_out"):
		title = "GitHub CI 失败：" + inline(compact(x.WorkflowRun.Name, 160))
		text = fmt.Sprintf("**%s**\n- 仓库：%s | %s%s", title, inline(x.Repository.FullName), inline(x.WorkflowRun.Conclusion), linkFor(x.WorkflowRun.HTMLURL, "github.com"))
	default:
		write(w, 200, map[string]bool{"ignored": true})
		return
	}
	// GitHub's delivery header is not covered by the HMAC; use the signed body for deduplication.
	s.notify(w, r, "github", fmt.Sprintf("%x", sha256.Sum256(b)), kind, title, text)
}

func (s *Server) linearWebhook(w http.ResponseWriter, r *http.Request) {
	b, ok := s.webhookBody(w, r, s.cfg.LinearSecret, "Linear-Signature", "")
	if !ok {
		return
	}
	var x struct {
		Action, Type string
		Timestamp    int64 `json:"webhookTimestamp"`
		Data         struct{ TeamID, Title, Identifier, URL string }
		UpdatedFrom  map[string]json.RawMessage
	}
	if json.Unmarshal(b, &x) != nil {
		fail(w, 400, "invalid payload")
		return
	}
	delta := time.Since(time.UnixMilli(x.Timestamp))
	if delta > time.Minute || delta < -time.Minute {
		fail(w, 401, "timestamp outside replay window")
		return
	}
	_, status := x.UpdatedFrom["stateId"]
	_, assignee := x.UpdatedFrom["assigneeId"]
	if s.cfg.LinearTeam == "" || x.Data.TeamID != s.cfg.LinearTeam || x.Type != "Issue" || (x.Action != "create" && !(x.Action == "update" && (status || assignee))) {
		write(w, 200, map[string]bool{"ignored": true})
		return
	}
	title := "Linear：" + inline(compact(x.Data.Identifier+" "+x.Data.Title, 180))
	s.notify(w, r, "linear", fmt.Sprintf("%x", sha256.Sum256(b)), "issue_"+x.Action, title, "**"+title+"**"+linkFor(x.Data.URL, "linear.app"))
}

func (s *Server) daily(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo || !s.cfg.DingReady() || !s.cfg.LinearReady() || !s.cfg.Groups[s.cfg.V1Group] {
		fail(w, 503, "daily summary not configured")
		return
	}
	zone := time.FixedZone("UTC+8", 8*60*60)
	date := time.Now().In(zone).Format("2006-01-02")
	wload, err := s.activeLoad(r.Context())
	if err != nil {
		fail(w, 502, "Linear workload unavailable")
		return
	}
	members, err := s.st.members(r.Context())
	if err != nil {
		fail(w, 503, "roster unavailable")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s Linear 研发晨报**\n- 进行中：%d | 未分配：%d\n", date, wload.Total, wload.Unassigned)
	if wload.Truncated {
		b.WriteString("- 仅统计前 500 条进行中的任务\n")
	}
	for _, m := range members {
		if m.LinearID != "" {
			fmt.Fprintf(&b, "- %s：%d\n", inline(m.Name), wload.Count[m.LinearID])
		}
	}
	s.notify(w, r, "daily", date, "daily_digest", "研发晨报", compact(b.String(), 6000))
}

func (s *Server) manualNotify(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo || !s.cfg.DingReady() || !s.cfg.Groups[s.cfg.V1Group] {
		fail(w, 503, "notification not configured")
		return
	}
	var x struct {
		EventID         string `json:"event_id"`
		ConversationID  string `json:"conversation_id"`
		Title, Markdown string
	}
	if !decode(w, r, &x) {
		return
	}
	if x.EventID == "" || len(x.EventID) > 128 || (x.ConversationID != "" && x.ConversationID != s.cfg.V1Group) || strings.TrimSpace(x.Title) == "" || strings.TrimSpace(x.Markdown) == "" {
		fail(w, 400, "event_id, allowed group, title and markdown required")
		return
	}
	s.notify(w, r, "internal", x.EventID, "markdown", compact(x.Title, 200), compact(x.Markdown, 6000))
}
