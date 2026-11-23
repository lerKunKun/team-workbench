package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	dingURL   = "https://api.dingtalk.com"
	linearURL = "https://api.linear.app/graphql"
)

var outbound = &http.Client{Timeout: 22 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func call(ctx context.Context, client *http.Client, method, url, service, header, secret string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s invalid request", service)
	}
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(header, secret)
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s transport failure", service)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s HTTP %d", service, res.StatusCode)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, output) != nil {
		return fmt.Errorf("%s invalid response", service)
	}
	return nil
}

type Ding struct {
	c      Config
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (d *Ding) getToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.expiry) {
		return d.token, nil
	}
	var res struct {
		AccessToken string
		ExpireIn    int
	}
	err := call(ctx, outbound, "POST", dingURL+"/v1.0/oauth2/accessToken", "dingtalk token", "", "",
		map[string]string{"appKey": d.c.DingID, "appSecret": d.c.DingSecret}, &res)
	if err != nil {
		return "", err
	}
	if res.AccessToken == "" {
		return "", fmt.Errorf("dingtalk token missing")
	}
	d.token = res.AccessToken
	d.expiry = time.Now().Add(time.Duration(max(0, res.ExpireIn-60)) * time.Second)
	return d.token, nil
}

func (d *Ding) api(ctx context.Context, method, path string, in, out any) error {
	if d.c.Demo {
		return nil
	}
	if !d.c.DingReady() {
		return fmt.Errorf("dingtalk disabled")
	}
	token, err := d.getToken(ctx)
	if err != nil {
		return err
	}
	err = call(ctx, outbound, method, dingURL+path, "dingtalk API", "x-acs-dingtalk-access-token", token, in, out)
	if err != nil && err.Error() == "dingtalk API HTTP 401" {
		d.mu.Lock()
		d.expiry = time.Time{}
		d.mu.Unlock()
	}
	return err
}

func (d *Ding) message(ctx context.Context, group, title, text string) error {
	if d.c.Demo {
		return nil
	}
	var res struct{ ProcessQueryKey string }
	p, _ := json.Marshal(map[string]string{"title": title, "text": text})
	err := d.api(ctx, "POST", "/v1.0/robot/groupMessages/send", map[string]any{
		"robotCode": d.c.Robot, "openConversationId": group, "msgKey": "sampleMarkdown", "msgParam": string(p),
	}, &res)
	if err == nil && res.ProcessQueryKey == "" {
		return fmt.Errorf("dingtalk API missing message receipt")
	}
	return err
}

func (d *Ding) sendCard(ctx context.Context, draft Draft) error {
	if d.c.Demo {
		return nil
	}
	var res struct {
		Success bool
		Result  struct {
			OutTrackId     string
			DeliverResults []struct {
				Success bool
				SpaceId string
			}
		}
	}
	err := d.api(ctx, "POST", "/v1.0/card/instances/createAndDeliver", map[string]any{
		"cardTemplateId": d.c.Template, "outTrackId": draft.TrackID, "callbackType": "STREAM", "userIdType": 1,
		"cardData": map[string]any{"cardParamMap": map[string]string{
			"draft_id": draft.ID, "summary": draft.Summary, "tasks_md": taskMarkdown(draft.Tasks), "status_text": "待确认：点击下方按钮写入 Linear", "is_confirmed": "false",
		}},
		"openSpaceId":             "dtv1.card//IM_GROUP." + draft.Group,
		"imGroupOpenSpaceModel":   map[string]bool{"supportForward": false},
		"imGroupOpenDeliverModel": map[string]string{"robotCode": d.c.Robot},
	}, &res)
	if err != nil {
		return err
	}
	if res.Success && res.Result.OutTrackId == draft.TrackID {
		for _, item := range res.Result.DeliverResults {
			if item.Success && item.SpaceId == draft.Group {
				return nil
			}
		}
	}
	return fmt.Errorf("dingtalk API card delivery not confirmed")
}

func (d *Ding) updateCard(ctx context.Context, draft Draft, text string) error {
	if d.c.Demo {
		return nil
	}
	var res struct{ Success, Result bool }
	err := d.api(ctx, "PUT", "/v1.0/card/instances", map[string]any{
		"outTrackId":        draft.TrackID,
		"cardUpdateOptions": map[string]bool{"updateCardDataByKey": true},
		"cardData":          map[string]any{"cardParamMap": map[string]string{"status_text": text, "is_confirmed": "true"}},
	}, &res)
	if err == nil && (!res.Success || !res.Result) {
		return fmt.Errorf("dingtalk API card update not confirmed")
	}
	return err
}

func (s *Server) linear(ctx context.Context, query string, variables map[string]any, output any) error {
	var response struct {
		Data   json.RawMessage
		Errors []json.RawMessage
	}
	if err := call(ctx, outbound, "POST", linearURL, "linear", "Authorization", s.cfg.LinearKey,
		map[string]any{"query": query, "variables": variables}, &response); err != nil {
		return err
	}
	if len(response.Errors) != 0 || len(response.Data) == 0 || string(response.Data) == "null" || json.Unmarshal(response.Data, output) != nil {
		return fmt.Errorf("linear GraphQL returned errors or invalid data")
	}
	return nil
}

type workload struct {
	Count             map[string]int
	Total, Unassigned int
	Truncated         bool
}

func (s *Server) activeLoad(ctx context.Context) (workload, error) {
	w := workload{Count: make(map[string]int)}
	if s.cfg.Demo {
		return w, nil
	}
	var after any
	for page := 0; page < 5; page++ {
		var res struct {
			Issues struct {
				Nodes    []struct{ Assignee *struct{ ID string } }
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		err := s.linear(ctx, `query Active($team: ID!, $after: String) {
			issues(first: 100, after: $after, filter: {team:{id:{eq:$team}},state:{type:{eq:"started"}}}) {
				nodes { assignee { id } } pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"team": s.cfg.LinearTeam, "after": after}, &res)
		if err != nil {
			return w, err
		}
		for _, i := range res.Issues.Nodes {
			w.Total++
			if i.Assignee != nil {
				w.Count[i.Assignee.ID]++
			} else {
				w.Unassigned++
			}
		}
		if !res.Issues.PageInfo.HasNextPage {
			return w, nil
		}
		if res.Issues.PageInfo.EndCursor == "" {
			return w, fmt.Errorf("linear pagination cursor missing")
		}
		after = res.Issues.PageInfo.EndCursor
	}
	w.Truncated = true
	return w, nil
}

func compact(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func plain(s string) string { return strings.Join(strings.Fields(s), " ") }

// Make untrusted model/event text inert in rendered Markdown.
func inline(s string) string {
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "<", "&lt;", ">", "&gt;", "*", "\\*", "_", "\\_", "`", "\\`", "@", "＠").Replace(plain(s))
}
