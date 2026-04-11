package cmd

import (
	"testing"

	"github.com/dlukt/srtmiddleware/monitor"
	"github.com/spf13/cobra"
)

func TestApplyMonitorFlagOverridesMergesSceneCommands(t *testing.T) {
	originalSceneCommands := twitchSceneCommands
	t.Cleanup(func() {
		twitchSceneCommands = originalSceneCommands
	})

	twitchSceneCommands = nil
	cmd := &cobra.Command{Use: "monitor"}
	cmd.Flags().StringToStringVar(&twitchSceneCommands, "scene-command", nil, "")
	if err := cmd.Flags().Parse([]string{
		"--scene-command", "!intro=Starting Soon",
		"--scene-command", "!brb=Be Right Back",
	}); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	cfg := monitor.DefaultFileConfig()
	cfg.Monitor.Twitch.SceneCommands = map[string]string{
		"!live": "Live",
		"!brb":  "BRB",
	}

	applyMonitorFlagOverrides(cmd, &cfg)

	if got := cfg.Monitor.Twitch.SceneCommands["!live"]; got != "Live" {
		t.Fatalf("scene command !live = %q, want Live", got)
	}
	if got := cfg.Monitor.Twitch.SceneCommands["!brb"]; got != "Be Right Back" {
		t.Fatalf("scene command !brb = %q, want Be Right Back", got)
	}
	if got := cfg.Monitor.Twitch.SceneCommands["!intro"]; got != "Starting Soon" {
		t.Fatalf("scene command !intro = %q, want Starting Soon", got)
	}
}
