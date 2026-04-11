package monitor

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigFilename = ".srtmiddleware.yaml"
	defaultRedirectURL    = "http://127.0.0.1:8099/callback"
	defaultListenAddr     = "127.0.0.1:8099"
)

type FileConfig struct {
	Monitor MonitorSettings `yaml:"monitor"`
}

type MonitorSettings struct {
	GrpcAddr   string            `yaml:"grpc_addr,omitempty"`
	OBS        OBSSettings       `yaml:"obs,omitempty"`
	AutoScenes AutoSceneSettings `yaml:"auto_scenes,omitempty"`
	Twitch     TwitchSettings    `yaml:"twitch,omitempty"`
}

type OBSSettings struct {
	WsAddr string `yaml:"ws_addr,omitempty"`
	WsPass string `yaml:"ws_pass,omitempty"`
}

type AutoSceneSettings struct {
	Live string `yaml:"live,omitempty"`
	LBR  string `yaml:"lbr,omitempty"`
	BRB  string `yaml:"brb,omitempty"`
}

type TwitchSettings struct {
	Enabled       bool              `yaml:"enabled,omitempty"`
	ClientID      string            `yaml:"client_id,omitempty"`
	ClientSecret  string            `yaml:"client_secret,omitempty"`
	RedirectURL   string            `yaml:"redirect_url,omitempty"`
	ListenAddr    string            `yaml:"listen_addr,omitempty"`
	AccessToken   string            `yaml:"access_token,omitempty"`
	RefreshToken  string            `yaml:"refresh_token,omitempty"`
	ExpiresAt     string            `yaml:"expires_at,omitempty"`
	SceneCommands map[string]string `yaml:"scene_commands,omitempty"`
}

type Configuration struct {
	ConfigPath string
	GrpcAddr   string
	OBS        OBSSettings
	AutoScenes AutoSceneSettings
	Twitch     TwitchRuntimeSettings
}

type TwitchRuntimeSettings struct {
	Enabled       bool
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	ListenAddr    string
	AccessToken   string
	RefreshToken  string
	ExpiresAt     time.Time
	SceneCommands map[string]string
}

type TwitchTokenState struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func DefaultFileConfig() FileConfig {
	return FileConfig{
		Monitor: MonitorSettings{
			GrpcAddr: "127.0.0.1:50051",
			OBS: OBSSettings{
				WsAddr: "localhost:4455",
			},
			AutoScenes: AutoSceneSettings{
				Live: "Live",
				LBR:  "LBR",
				BRB:  "BRB",
			},
			Twitch: TwitchSettings{
				RedirectURL:   defaultRedirectURL,
				ListenAddr:    defaultListenAddr,
				SceneCommands: map[string]string{},
			},
		},
	}
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultConfigFilename), nil
}

func ResolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return expandPath(path)
	}
	return DefaultConfigPath()
}

func expandPath(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Abs(path)
}

func LoadFileConfig(path string) (FileConfig, string, error) {
	resolvedPath, err := ResolveConfigPath(path)
	if err != nil {
		return FileConfig{}, "", err
	}
	cfg := DefaultFileConfig()
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.Normalize()
			return cfg, resolvedPath, nil
		}
		return FileConfig{}, "", err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, "", fmt.Errorf("parse monitor config: %w", err)
	}
	cfg.Normalize()
	return cfg, resolvedPath, nil
}

func SaveFileConfig(path string, cfg FileConfig) error {
	resolvedPath, err := ResolveConfigPath(path)
	if err != nil {
		return err
	}
	cfg.Normalize()
	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".srtmiddleware-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceConfigFile(runtime.GOOS, tmpName, resolvedPath, os.Rename, os.Remove, os.Stat)
}

func replaceConfigFile(goos, tmpName, resolvedPath string, rename func(string, string) error, remove func(string) error, stat func(string) (os.FileInfo, error)) error {
	if err := rename(tmpName, resolvedPath); err != nil {
		if goos != "windows" {
			return err
		}
		if !windowsRenameNeedsRemove(err) {
			return err
		}
		if _, statErr := stat(resolvedPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return err
			}
			return statErr
		}
		if err := remove(resolvedPath); err != nil {
			return err
		}
		return rename(tmpName, resolvedPath)
	}
	return nil
}

func windowsRenameNeedsRemove(err error) bool {
	if os.IsExist(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already exists") ||
		strings.Contains(message, "file exists") ||
		strings.Contains(message, "destination exists")
}

func (cfg *FileConfig) Normalize() {
	if strings.TrimSpace(cfg.Monitor.GrpcAddr) == "" {
		cfg.Monitor.GrpcAddr = "127.0.0.1:50051"
	}
	if strings.TrimSpace(cfg.Monitor.OBS.WsAddr) == "" {
		cfg.Monitor.OBS.WsAddr = "localhost:4455"
	}
	if strings.TrimSpace(cfg.Monitor.AutoScenes.Live) == "" {
		cfg.Monitor.AutoScenes.Live = "Live"
	}
	if strings.TrimSpace(cfg.Monitor.AutoScenes.LBR) == "" {
		cfg.Monitor.AutoScenes.LBR = "LBR"
	}
	if strings.TrimSpace(cfg.Monitor.AutoScenes.BRB) == "" {
		cfg.Monitor.AutoScenes.BRB = "BRB"
	}
	if strings.TrimSpace(cfg.Monitor.Twitch.RedirectURL) == "" {
		cfg.Monitor.Twitch.RedirectURL = defaultRedirectURL
	}
	if strings.TrimSpace(cfg.Monitor.Twitch.ListenAddr) == "" {
		cfg.Monitor.Twitch.ListenAddr = defaultListenAddr
	}
	if cfg.Monitor.Twitch.SceneCommands == nil {
		cfg.Monitor.Twitch.SceneCommands = map[string]string{}
	}
	cfg.Monitor.Twitch.SceneCommands = normalizeSceneCommands(cfg.Monitor.Twitch.SceneCommands)
}

func (cfg FileConfig) Resolve(configPath string) (Configuration, error) {
	cfg.Normalize()
	if err := cfg.Monitor.ValidateRun(); err != nil {
		return Configuration{}, err
	}
	runtimeConfig := Configuration{
		ConfigPath: configPath,
		GrpcAddr:   cfg.Monitor.GrpcAddr,
		OBS:        cfg.Monitor.OBS,
		AutoScenes: cfg.Monitor.AutoScenes,
		Twitch: TwitchRuntimeSettings{
			Enabled:       cfg.Monitor.Twitch.Enabled,
			ClientID:      cfg.Monitor.Twitch.ClientID,
			ClientSecret:  cfg.Monitor.Twitch.ClientSecret,
			RedirectURL:   cfg.Monitor.Twitch.RedirectURL,
			ListenAddr:    cfg.Monitor.Twitch.ListenAddr,
			AccessToken:   cfg.Monitor.Twitch.AccessToken,
			RefreshToken:  cfg.Monitor.Twitch.RefreshToken,
			SceneCommands: cloneStringMap(cfg.Monitor.Twitch.SceneCommands),
		},
	}
	if cfg.Monitor.Twitch.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, cfg.Monitor.Twitch.ExpiresAt)
		if err != nil {
			return Configuration{}, fmt.Errorf("parse monitor.twitch.expires_at: %w", err)
		}
		runtimeConfig.Twitch.ExpiresAt = expiresAt
	}
	return runtimeConfig, nil
}

func (s MonitorSettings) ValidateRun() error {
	if strings.TrimSpace(s.GrpcAddr) == "" {
		return errors.New("monitor.grpc_addr must not be empty")
	}
	if strings.TrimSpace(s.OBS.WsAddr) == "" {
		return errors.New("monitor.obs.ws_addr must not be empty")
	}
	if strings.TrimSpace(s.AutoScenes.Live) == "" {
		return errors.New("monitor.auto_scenes.live must not be empty")
	}
	if strings.TrimSpace(s.AutoScenes.LBR) == "" {
		return errors.New("monitor.auto_scenes.lbr must not be empty")
	}
	if strings.TrimSpace(s.AutoScenes.BRB) == "" {
		return errors.New("monitor.auto_scenes.brb must not be empty")
	}
	if !s.Twitch.Enabled {
		return nil
	}
	if strings.TrimSpace(s.Twitch.ClientID) == "" {
		return errors.New("monitor.twitch.client_id must not be empty when twitch is enabled")
	}
	if strings.TrimSpace(s.Twitch.ClientSecret) == "" {
		return errors.New("monitor.twitch.client_secret must not be empty when twitch is enabled")
	}
	if strings.TrimSpace(s.Twitch.AccessToken) == "" {
		return errors.New("monitor.twitch.access_token must not be empty when twitch is enabled; run `srtmiddleware monitor login`")
	}
	if strings.TrimSpace(s.Twitch.RefreshToken) == "" {
		return errors.New("monitor.twitch.refresh_token must not be empty when twitch is enabled; run `srtmiddleware monitor login`")
	}
	return nil
}

func (s MonitorSettings) ValidateLogin() error {
	if strings.TrimSpace(s.Twitch.ClientID) == "" {
		return errors.New("monitor.twitch.client_id must not be empty")
	}
	if strings.TrimSpace(s.Twitch.ClientSecret) == "" {
		return errors.New("monitor.twitch.client_secret must not be empty")
	}
	if strings.TrimSpace(s.Twitch.RedirectURL) == "" {
		return errors.New("monitor.twitch.redirect_url must not be empty")
	}
	redirectURL, err := url.Parse(s.Twitch.RedirectURL)
	if err != nil {
		return fmt.Errorf("monitor.twitch.redirect_url must be a valid URL: %w", err)
	}
	if strings.TrimSpace(redirectURL.Path) == "" {
		return errors.New("monitor.twitch.redirect_url must include a callback path")
	}
	if strings.TrimSpace(s.Twitch.ListenAddr) == "" {
		return errors.New("monitor.twitch.listen_addr must not be empty")
	}
	return nil
}

func UpdateTwitchTokens(path string, tokens TwitchTokenState) error {
	cfg, resolvedPath, err := LoadFileConfig(path)
	if err != nil {
		return err
	}
	cfg.Monitor.Twitch.AccessToken = tokens.AccessToken
	cfg.Monitor.Twitch.RefreshToken = tokens.RefreshToken
	if !tokens.ExpiresAt.IsZero() {
		cfg.Monitor.Twitch.ExpiresAt = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return SaveFileConfig(resolvedPath, cfg)
}

func normalizeSceneCommands(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		scene := strings.TrimSpace(in[key])
		if scene == "" {
			continue
		}
		normalized := normalizeCommandName(key)
		if normalized == "" || normalized == "!auto" {
			continue
		}
		out[normalized] = scene
	}
	return out
}

func normalizeCommandName(command string) string {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return ""
	}
	if !strings.HasPrefix(command, "!") {
		command = "!" + command
	}
	return command
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
