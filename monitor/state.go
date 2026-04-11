package monitor

import "fmt"

const autoResumeCommand = "!auto"

type SceneSwitchResult int

const (
	SceneSwitchChanged SceneSwitchResult = iota
	SceneSwitchNoop
)

type SceneSwitcher interface {
	SwitchScene(scene string) (SceneSwitchResult, error)
}

type ChatCommand struct {
	Command        string
	TargetScene    string
	ReplyParentID  string
	ChatterDisplay string
}

type commandOutcome struct {
	reply string
}

type controlState struct {
	manualMode      bool
	lastAutoScene   string
	lastManualScene string
}

func (s *controlState) applyAutoScene(scene string, switcher SceneSwitcher) error {
	s.lastAutoScene = scene
	if s.manualMode {
		return nil
	}
	_, err := switcher.SwitchScene(scene)
	return err
}

func (s *controlState) handleChatCommand(command ChatCommand, switcher SceneSwitcher) (commandOutcome, error) {
	if command.Command == autoResumeCommand {
		if s.lastAutoScene == "" {
			s.manualMode = false
			s.lastManualScene = ""
			return commandOutcome{reply: "Auto mode resumed. Waiting for stream state."}, nil
		}
		result, err := switcher.SwitchScene(s.lastAutoScene)
		if err != nil {
			return commandOutcome{reply: fmt.Sprintf("Failed to resume auto mode on %q.", s.lastAutoScene)}, err
		}
		s.manualMode = false
		s.lastManualScene = ""
		if result == SceneSwitchNoop {
			return commandOutcome{reply: fmt.Sprintf("Auto mode resumed. Scene is already %q.", s.lastAutoScene)}, nil
		}
		return commandOutcome{reply: fmt.Sprintf("Auto mode resumed. Switched scene to %q.", s.lastAutoScene)}, nil
	}

	result, err := switcher.SwitchScene(command.TargetScene)
	if err != nil {
		return commandOutcome{reply: fmt.Sprintf("Failed to switch scene to %q.", command.TargetScene)}, err
	}
	s.manualMode = true
	s.lastManualScene = command.TargetScene
	if result == SceneSwitchNoop {
		return commandOutcome{reply: fmt.Sprintf("Scene is already %q. Manual mode is active; use !auto to resume auto switching.", command.TargetScene)}, nil
	}
	return commandOutcome{reply: fmt.Sprintf("Switched scene to %q. Manual mode is active; use !auto to resume auto switching.", command.TargetScene)}, nil
}
