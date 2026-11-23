package bot

import "strings"

type Command struct{ Kind, Title, Body, Health string }

func DirectAllowed(kind, corp, want, staff string) bool {
	return kind == "1" && want != "" && corp == want && staff != ""
}
func Parse(raw string) Command {
	s := strings.TrimSpace(raw)
	if len(s) > 8000 {
		return Command{}
	}
	if s == "帮助" || strings.EqualFold(s, "help") {
		return Command{Kind: "help"}
	}
	a := strings.SplitN(strings.ReplaceAll(s, "｜", "|"), "|", 3)
	if len(a) != 3 {
		return Command{}
	}
	for i := range a {
		a[i] = strings.TrimSpace(a[i])
	}
	if a[1] == "" || a[2] == "" {
		return Command{}
	}
	switch strings.ToLower(a[0]) {
	case "提交 issue", "issue", "建单":
		return Command{Kind: "issue", Title: a[1], Body: a[2]}
	case "立项", "立项申请":
		return Command{Kind: "propose", Title: a[1], Body: a[2]}
	case "更新进度", "进度":
		health := map[string]string{"正常": "onTrack", "有风险": "atRisk", "阻塞": "offTrack"}[a[1]]
		if health != "" {
			return Command{Kind: "update", Health: health, Body: a[2]}
		}
	}
	return Command{}
}
