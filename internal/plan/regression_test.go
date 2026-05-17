package plan

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// Regression for review bug #1: plan.Run must honor Project.Config.Targets
// when the explicit targets arg is empty.
func TestRun_HonorsConfigTargets(t *testing.T) {
	a := &recordingPlugin{name: "a", detect: false}
	b := &recordingPlugin{name: "b", detect: false}
	c := &recordingPlugin{name: "c", detect: false}

	reg := plugin.NewRegistry()
	_ = reg.Register(a)
	_ = reg.Register(b)
	_ = reg.Register(c)

	proj := &model.Project{
		Root: "/tmp/fake",
		Config: &model.Config{
			Targets: []string{"a", "c"},
		},
	}

	if _, _, err := Run(proj, reg, nil); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if !a.called || !c.called {
		t.Fatalf("expected a and c to be called, got a=%v c=%v", a.called, c.called)
	}
	if b.called {
		t.Fatalf("b should not have been called (not in Config.Targets)")
	}
}

// Regression for review bug #1 corner case: explicit targets arg still wins
// over Config.Targets.
func TestRun_ExplicitTargetsOverrideConfig(t *testing.T) {
	a := &recordingPlugin{name: "a", detect: true}
	b := &recordingPlugin{name: "b", detect: true}

	reg := plugin.NewRegistry()
	_ = reg.Register(a)
	_ = reg.Register(b)

	proj := &model.Project{
		Root: "/tmp/fake",
		Config: &model.Config{
			Targets: []string{"a"},
		},
	}

	if _, _, err := Run(proj, reg, []string{"b"}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if a.called {
		t.Fatalf("a should not have been called (explicit targets was [b])")
	}
	if !b.called {
		t.Fatalf("b should have been called")
	}
}

type recordingPlugin struct {
	name   string
	detect bool
	called bool
}

func (p *recordingPlugin) Name() string                      { return p.name }
func (p *recordingPlugin) Detect(root string) bool           { return p.detect }
func (p *recordingPlugin) Capabilities() plugin.Capabilities { return plugin.Capabilities{} }
func (p *recordingPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	p.called = true
	return nil, nil
}

var _ plugin.Plugin = (*recordingPlugin)(nil)

func TestRecording_StringerSanity(t *testing.T) {
	p := &recordingPlugin{name: "x"}
	if !strings.Contains(p.Name(), "x") {
		t.Fatalf("Name not preserved: %q", p.Name())
	}
}
