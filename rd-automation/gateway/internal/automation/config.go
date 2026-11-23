package automation

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	Listen, DBPassword, InternalToken, HookToken, N8nURL string
	DingID, DingSecret, Robot, Template                  string
	Groups                                               map[string]bool
	LinearKey, LinearTeam, LLMURL, LLMKey, Model         string
	Demo, Stream, JSONMode                               bool
	V1Group, GitHubSecret, LinearSecret                  string
	Repositories                                         map[string]bool
	LabelIDs                                             map[string]string
	WorkbenchMode                                        bool
	WorkbenchCorp, WorkbenchAPI, WorkbenchToken          string
}

func Load() (Config, error) {
	e := os.Getenv
	c := Config{
		Listen: e("LISTEN_ADDR"), DBPassword: e("APP_DB_PASSWORD"), InternalToken: e("INTERNAL_API_TOKEN"),
		HookToken: e("N8N_HOOK_TOKEN"), N8nURL: e("N8N_BASE_URL"), DingID: e("DINGTALK_CLIENT_ID"),
		DingSecret: e("DINGTALK_CLIENT_SECRET"), Robot: e("DINGTALK_ROBOT_CODE"), Template: e("DINGTALK_CARD_TEMPLATE_ID"),
		Groups: csvSet(e("DINGTALK_ALLOWED_GROUPS")), LinearKey: e("LINEAR_API_KEY"), LinearTeam: e("LINEAR_TEAM_ID"),
		LLMURL: strings.TrimRight(e("LLM_API_BASE_URL"), "/"), LLMKey: e("LLM_API_KEY"), Model: e("LLM_MODEL"),
		V1Group: e("V1_CONVERSATION_ID"), GitHubSecret: e("GITHUB_WEBHOOK_SECRET"), LinearSecret: e("LINEAR_WEBHOOK_SECRET"),
		Repositories: csvSet(e("GITHUB_ALLOWED_REPOSITORIES")), JSONMode: true,
		WorkbenchCorp: e("DINGTALK_CORP_ID"), WorkbenchAPI: strings.TrimRight(e("WORKBENCH_API_URL"), "/"), WorkbenchToken: e("WORKBENCH_BOT_TOKEN"),
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.N8nURL == "" {
		c.N8nURL = "http://n8n:5678"
	}
	for name, flag := range map[string]*bool{"DEMO_MODE": &c.Demo, "DINGTALK_STREAM_ENABLED": &c.Stream, "LLM_JSON_MODE": &c.JSONMode, "DINGTALK_WORKBENCH_DM": &c.WorkbenchMode} {
		if v, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.ParseBool(v)
			if err != nil {
				return c, fmt.Errorf("%s must be true or false", name)
			}
			*flag = parsed
		}
	}
	if c.DBPassword == "" || len(c.InternalToken) < 32 || len(c.HookToken) < 32 || c.HookToken == c.InternalToken {
		return c, fmt.Errorf("database password and distinct internal/hook tokens (32+ chars) required")
	}
	if c.LLMURL != "" {
		u, err := url.Parse(c.LLMURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			// Do not let an invalid optional integration take down the base services.
			c.LLMURL = ""
		}
	}
	c.LabelIDs = make(map[string]string)
	for v := range csvSet(e("LINEAR_LABEL_IDS")) {
		name, id, _ := strings.Cut(v, "=")
		name = strings.TrimSpace(name)
		if parsed, err := uuid.Parse(strings.TrimSpace(id)); err == nil && allowedLabels[name] {
			c.LabelIDs[name] = parsed.String()
		}
	}
	return c, nil
}

func csvSet(s string) map[string]bool {
	m := make(map[string]bool)
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			m[v] = true
		}
	}
	return m
}

func (c Config) DingReady() bool { return c.DingID != "" && c.DingSecret != "" && c.Robot != "" }
func (c Config) LinearReady() bool {
	_, err := uuid.Parse(c.LinearTeam)
	return err == nil && c.LinearKey != ""
}
func (c Config) V2Ready() bool {
	return c.DingReady() && c.Template != "" && len(c.Groups) != 0 && c.LinearReady() && c.LLMURL != "" && c.LLMKey != "" && c.Model != ""
}
