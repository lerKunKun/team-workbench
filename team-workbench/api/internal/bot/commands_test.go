package bot

import "testing"

func TestParseCommand(t *testing.T) {
	for _, x := range []struct{ input, kind, title, body string }{{"帮助", "help", "", ""}, {"提交 Issue | 刷新丢失筛选 | 在测试项目复现", "issue", "刷新丢失筛选", "在测试项目复现"}, {"立项 | 反馈导出 | 收集客户意见", "propose", "反馈导出", "收集客户意见"}, {"我只是聊天", "", "", ""}} {
		got := Parse(x.input)
		if got.Kind != x.kind || got.Title != x.title || got.Body != x.body {
			t.Errorf("%q: %+v", x.input, got)
		}
	}
}
func TestDMChannelGuard(t *testing.T) {
	if !DirectAllowed("1", "test-corp", "test-corp", "user") || DirectAllowed("2", "test-corp", "test-corp", "user") || DirectAllowed("1", "another", "test-corp", "user") || DirectAllowed("1", "test-corp", "test-corp", "") {
		t.Fatal("invalid DM channel boundary")
	}
}
