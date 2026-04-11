package monitor

import (
	"errors"
	"testing"
)

type fakeSceneSwitcher struct {
	current string
	err     error
	calls   []string
}

func (f *fakeSceneSwitcher) SwitchScene(scene string) (SceneSwitchResult, error) {
	f.calls = append(f.calls, scene)
	if f.err != nil {
		return SceneSwitchChanged, f.err
	}
	if f.current == scene {
		return SceneSwitchNoop, nil
	}
	f.current = scene
	return SceneSwitchChanged, nil
}

func TestControlStateManualModeSuppressesAutoSwitch(t *testing.T) {
	t.Parallel()

	state := controlState{}
	switcher := &fakeSceneSwitcher{current: "Live"}
	if _, err := state.handleChatCommand(ChatCommand{Command: "!brb", TargetScene: "BRB"}, switcher); err != nil {
		t.Fatalf("handleChatCommand returned error: %v", err)
	}
	if err := state.applyAutoScene("Live", switcher); err != nil {
		t.Fatalf("applyAutoScene returned error: %v", err)
	}
	if len(switcher.calls) != 1 {
		t.Fatalf("SwitchScene call count = %d, want 1", len(switcher.calls))
	}
	if got := switcher.current; got != "BRB" {
		t.Fatalf("current scene = %q, want BRB", got)
	}
}

func TestControlStateAutoResumeUsesLastAutoScene(t *testing.T) {
	t.Parallel()

	state := controlState{manualMode: true, lastAutoScene: "Live", lastManualScene: "BRB"}
	switcher := &fakeSceneSwitcher{current: "BRB"}
	outcome, err := state.handleChatCommand(ChatCommand{Command: autoResumeCommand}, switcher)
	if err != nil {
		t.Fatalf("handleChatCommand returned error: %v", err)
	}
	if state.manualMode {
		t.Fatal("manual mode still enabled after !auto")
	}
	if got := switcher.current; got != "Live" {
		t.Fatalf("current scene = %q, want Live", got)
	}
	if want := `Auto mode resumed. Switched scene to "Live".`; outcome.reply != want {
		t.Fatalf("reply = %q, want %q", outcome.reply, want)
	}
}

func TestControlStateFailedManualSwitchPreservesPriorMode(t *testing.T) {
	t.Parallel()

	state := controlState{manualMode: false, lastAutoScene: "Live"}
	switcher := &fakeSceneSwitcher{current: "Live", err: errors.New("obs failed")}
	outcome, err := state.handleChatCommand(ChatCommand{Command: "!brb", TargetScene: "BRB"}, switcher)
	if err == nil {
		t.Fatal("expected manual switch error")
	}
	if state.manualMode {
		t.Fatal("manual mode changed on failed switch")
	}
	if got := switcher.current; got != "Live" {
		t.Fatalf("current scene = %q, want Live", got)
	}
	if want := `Failed to switch scene to "BRB".`; outcome.reply != want {
		t.Fatalf("reply = %q, want %q", outcome.reply, want)
	}
}
