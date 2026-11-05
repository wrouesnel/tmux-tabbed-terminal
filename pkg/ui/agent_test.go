package ui

import "testing"

func TestUseAgent(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name   string
		config string
		chosen *bool
		want   bool
	}{
		{"auto", agentAuto, nil, noConnectionSharing()},
		{"always", agentAlways, nil, true},
		{"never", agentNever, nil, false},
		{"chosen over never", agentNever, &yes, true},
		{"chosen over always", agentAlways, &no, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			a.cfg.SSH.Agent = tc.config
			a.state.UseAgent = tc.chosen
			if got := a.useAgent(); got != tc.want {
				t.Errorf("useAgent() = %v, want %v", got, tc.want)
			}
		})
	}
}
