package automation

import "testing"

func TestWorkbenchPrivateTarget(t *testing.T) {
	c := Config{WorkbenchCorp: "ding-test", WorkbenchMode: true}
	if !c.workbenchMessageAllowed("1", "ding-test", "staff") || c.workbenchMessageAllowed("2", "ding-test", "staff") || c.workbenchMessageAllowed("1", "another", "staff") || c.workbenchMessageAllowed("1", "ding-test", "") {
		t.Fatal("invalid DM allowlist boundary")
	}
	for _, check := range []struct {
		space, kind, staff string
		want               bool
	}{{"", "IM_ROBOT", "staff", true}, {"dtv1.card//IM_ROBOT.staff", "IM_ROBOT", "staff", true}, {"dtv1.card//IM_GROUP.staff", "IM_GROUP", "staff", false}, {"other", "IM_ROBOT", "staff", false}} {
		if ok := directCardSpace(check.space, check.kind, check.staff); ok != check.want {
			t.Errorf("space %q: %v", check.space, ok)
		}
	}
}
