package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileConfigMissingReturnsDefaults(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "monitor.yaml")
	cfg, resolvedPath, err := LoadFileConfig(configPath)
	if err != nil {
		t.Fatalf("LoadFileConfig returned error: %v", err)
	}
	if resolvedPath != configPath {
		t.Fatalf("resolved path = %q, want %q", resolvedPath, configPath)
	}
	if cfg.Monitor.GrpcAddr != "127.0.0.1:50051" {
		t.Fatalf("unexpected default grpc addr: %q", cfg.Monitor.GrpcAddr)
	}
	if cfg.Monitor.OBS.WsAddr != "localhost:4455" {
		t.Fatalf("unexpected default obs addr: %q", cfg.Monitor.OBS.WsAddr)
	}
}

func TestSaveFileConfigWritesSecureFile(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "monitor.yaml")
	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.AccessToken = "access-token"
	cfg.Monitor.Twitch.RefreshToken = "refresh-token"

	if err := SaveFileConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveFileConfig returned error: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReplaceConfigFileWindowsOverwritesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	destinationPath := filepath.Join(dir, "monitor.yaml")
	tmpPath := filepath.Join(dir, "monitor.tmp.yaml")

	if err := os.WriteFile(destinationPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile destination returned error: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile tmp returned error: %v", err)
	}

	renameCalls := 0
	removeCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: errors.New("destination exists")}
		}
		return os.Rename(oldPath, newPath)
	}
	remove := func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	if err := replaceConfigFile("windows", tmpPath, destinationPath, rename, remove, os.Stat); err != nil {
		t.Fatalf("replaceConfigFile returned error: %v", err)
	}
	if renameCalls != 2 {
		t.Fatalf("rename called %d times, want 2", renameCalls)
	}
	if removeCalls != 1 {
		t.Fatalf("remove called %d times, want 1", removeCalls)
	}

	data, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("destination contents = %q, want %q", data, "new")
	}
}

func TestReplaceConfigFileWindowsKeepsDestinationOnNonOverwriteError(t *testing.T) {
	dir := t.TempDir()
	destinationPath := filepath.Join(dir, "monitor.yaml")
	tmpPath := filepath.Join(dir, "monitor.tmp.yaml")

	if err := os.WriteFile(destinationPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile destination returned error: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile tmp returned error: %v", err)
	}

	removeCalled := false
	remove := func(path string) error {
		removeCalled = true
		return os.Remove(path)
	}

	wantErr := errors.New("temporary rename failure")
	err := replaceConfigFile(
		"windows",
		tmpPath,
		destinationPath,
		func(oldPath, newPath string) error { return wantErr },
		remove,
		os.Stat,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("replaceConfigFile error = %v, want %v", err, wantErr)
	}
	if removeCalled {
		t.Fatal("remove must not be called for non-overwrite rename errors")
	}

	data, readErr := os.ReadFile(destinationPath)
	if readErr != nil {
		t.Fatalf("ReadFile returned error: %v", readErr)
	}
	if string(data) != "old" {
		t.Fatalf("destination contents = %q, want %q", data, "old")
	}
}

func TestResolveConfigNormalizesCommandsAndExpiry(t *testing.T) {
	t.Parallel()

	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"
	cfg.Monitor.Twitch.AccessToken = "access-token"
	cfg.Monitor.Twitch.RefreshToken = "refresh-token"
	cfg.Monitor.Twitch.ExpiresAt = "2026-04-11T12:00:00Z"
	cfg.Monitor.Twitch.SceneCommands = map[string]string{
		"Live":  "Main",
		"!auto": "ignored",
		"  brb": "BRB",
	}

	resolved, err := cfg.Resolve("/tmp/monitor.yaml")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if got := resolved.Twitch.SceneCommands["!live"]; got != "Main" {
		t.Fatalf("normalized !live command = %q", got)
	}
	if got := resolved.Twitch.SceneCommands["!brb"]; got != "BRB" {
		t.Fatalf("normalized !brb command = %q", got)
	}
	if _, exists := resolved.Twitch.SceneCommands["!auto"]; exists {
		t.Fatal("!auto must not be configurable")
	}
	if want := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC); !resolved.Twitch.ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", resolved.Twitch.ExpiresAt, want)
	}
}

func TestValidateRunRequiresTokensOnlyWhenEnabled(t *testing.T) {
	t.Parallel()

	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.Enabled = false
	if err := cfg.Monitor.ValidateRun(); err != nil {
		t.Fatalf("ValidateRun returned error with twitch disabled: %v", err)
	}

	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"
	if err := cfg.Monitor.ValidateRun(); err == nil {
		t.Fatal("ValidateRun succeeded without tokens")
	}
}

func TestValidateLoginRejectsRedirectURLWithoutPath(t *testing.T) {
	t.Parallel()

	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"
	cfg.Monitor.Twitch.RedirectURL = "http://127.0.0.1:8099"

	err := cfg.Monitor.ValidateLogin()
	if err == nil {
		t.Fatal("ValidateLogin succeeded without a redirect callback path")
	}
	if !strings.Contains(err.Error(), "callback path") {
		t.Fatalf("ValidateLogin error = %v, want callback path validation", err)
	}
}
