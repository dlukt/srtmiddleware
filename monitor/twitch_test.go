package monitor

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommandFromEventRequiresAuthorization(t *testing.T) {
	t.Parallel()

	client := &TwitchClient{
		logger:        log.New(io.Discard, "", 0),
		sceneCommands: map[string]string{"!brb": "BRB"},
		seenMessages:  map[string]time.Time{},
	}

	_, ok := client.commandFromEvent(chatMessageEvent{
		BroadcasterUserID: "1",
		ChatterUserID:     "2",
		ChatterUserName:   "viewer",
		MessageID:         "m1",
		Message:           chatMessageBody{Text: "!brb"},
	})
	if ok {
		t.Fatal("unauthorized command was accepted")
	}

	command, ok := client.commandFromEvent(chatMessageEvent{
		BroadcasterUserID: "1",
		ChatterUserID:     "3",
		ChatterUserName:   "mod",
		MessageID:         "m2",
		Message:           chatMessageBody{Text: "!brb now"},
		Badges:            []chatBadge{{SetID: "moderator"}},
	})
	if !ok {
		t.Fatal("moderator command was rejected")
	}
	if command.TargetScene != "BRB" {
		t.Fatalf("target scene = %q, want BRB", command.TargetScene)
	}
}

func TestLoginConfigurationEnablesTwitchForOAuth(t *testing.T) {
	t.Parallel()

	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"

	settings, err := loginConfiguration(filepath.Join(t.TempDir(), "monitor.yaml"), cfg)
	if err != nil {
		t.Fatalf("loginConfiguration returned error: %v", err)
	}
	if !settings.Twitch.Enabled {
		t.Fatal("loginConfiguration left twitch disabled")
	}
	if settings.Twitch.ClientID != "client-id" {
		t.Fatalf("client ID = %q, want client-id", settings.Twitch.ClientID)
	}
	if settings.Twitch.ClientSecret != "client-secret" {
		t.Fatalf("client secret = %q, want client-secret", settings.Twitch.ClientSecret)
	}
	if settings.Twitch.RedirectURL != defaultRedirectURL {
		t.Fatalf("redirect URL = %q, want %q", settings.Twitch.RedirectURL, defaultRedirectURL)
	}
	if settings.Twitch.ListenAddr != defaultListenAddr {
		t.Fatalf("listen addr = %q, want %q", settings.Twitch.ListenAddr, defaultListenAddr)
	}
}

func TestCommandFromEventDeduplicatesMessageID(t *testing.T) {
	t.Parallel()

	client := &TwitchClient{
		logger:        log.New(io.Discard, "", 0),
		sceneCommands: map[string]string{"!live": "Live"},
		seenMessages:  map[string]time.Time{},
	}
	event := chatMessageEvent{
		BroadcasterUserID: "1",
		ChatterUserID:     "1",
		ChatterUserName:   "owner",
		MessageID:         "dup",
		Message:           chatMessageBody{Text: "!live"},
	}

	if _, ok := client.commandFromEvent(event); !ok {
		t.Fatal("first command was rejected")
	}
	if _, ok := client.commandFromEvent(event); ok {
		t.Fatal("duplicate command was accepted")
	}
}

func TestDoHelixJSONRefreshesOnUnauthorized(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "monitor.yaml")
	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"
	cfg.Monitor.Twitch.AccessToken = "old-token"
	cfg.Monitor.Twitch.RefreshToken = "old-refresh"
	cfg.Monitor.Twitch.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if err := SaveFileConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveFileConfig returned error: %v", err)
	}

	var apiCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth2/token":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(twitchTokenResponse{
				AccessToken:  "new-token",
				RefreshToken: "new-refresh",
				ExpiresIn:    3600,
			})
		case r.URL.Path == "/helix/test":
			call := atomic.AddInt32(&apiCalls, 1)
			gotToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if call == 1 {
				if gotToken != "old-token" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if gotToken != "new-token" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &TwitchClient{
		httpClient:    server.Client(),
		logger:        log.New(io.Discard, "", 0),
		apiBase:       server.URL + "/helix",
		authBase:      server.URL + "/oauth2",
		configPath:    configPath,
		clientID:      "client-id",
		clientSecret:  "client-secret",
		accessToken:   "old-token",
		refreshToken:  "old-refresh",
		expiresAt:     time.Now().Add(time.Hour),
		sceneCommands: map[string]string{},
		seenMessages:  map[string]time.Time{},
	}

	var rsp map[string]string
	if err := client.doHelixJSON(context.Background(), http.MethodPost, "/test", map[string]string{"k": "v"}, &rsp); err != nil {
		t.Fatalf("doHelixJSON returned error: %v", err)
	}
	if rsp["status"] != "ok" {
		t.Fatalf("unexpected response: %+v", rsp)
	}
	saved, _, err := LoadFileConfig(configPath)
	if err != nil {
		t.Fatalf("LoadFileConfig returned error: %v", err)
	}
	if saved.Monitor.Twitch.AccessToken != "new-token" {
		t.Fatalf("saved access token = %q, want new-token", saved.Monitor.Twitch.AccessToken)
	}
}

func TestEnsureValidatedRefreshesUnauthorizedToken(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "monitor.yaml")
	cfg := DefaultFileConfig()
	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.ClientID = "client-id"
	cfg.Monitor.Twitch.ClientSecret = "client-secret"
	cfg.Monitor.Twitch.AccessToken = "old-token"
	cfg.Monitor.Twitch.RefreshToken = "old-refresh"
	if err := SaveFileConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveFileConfig returned error: %v", err)
	}

	var refreshCalls int32
	var validateCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			atomic.AddInt32(&refreshCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(twitchTokenResponse{
				AccessToken:  "new-token",
				RefreshToken: "new-refresh",
				ExpiresIn:    3600,
			})
		case "/oauth2/validate":
			atomic.AddInt32(&validateCalls, 1)
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "OAuth ")
			if token == "old-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if token != "new-token" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(twitchValidateResponse{
				UserID: "broadcaster-id",
				Login:  "streamer",
				Scopes: twitchChatScopes,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &TwitchClient{
		httpClient:    server.Client(),
		logger:        log.New(io.Discard, "", 0),
		authBase:      server.URL + "/oauth2",
		configPath:    configPath,
		clientID:      "client-id",
		clientSecret:  "client-secret",
		accessToken:   "old-token",
		refreshToken:  "old-refresh",
		sceneCommands: map[string]string{},
		seenMessages:  map[string]time.Time{},
	}

	if err := client.ensureValidated(context.Background()); err != nil {
		t.Fatalf("ensureValidated returned error: %v", err)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&validateCalls); got != 2 {
		t.Fatalf("validate calls = %d, want 2", got)
	}
	if client.accessTokenValue() != "new-token" {
		t.Fatalf("client access token = %q, want new-token", client.accessTokenValue())
	}
	if client.refreshTokenValue() != "new-refresh" {
		t.Fatalf("client refresh token = %q, want new-refresh", client.refreshTokenValue())
	}
	if client.broadcasterID != "broadcaster-id" {
		t.Fatalf("broadcaster ID = %q, want broadcaster-id", client.broadcasterID)
	}
	if client.broadcasterLogin != "streamer" {
		t.Fatalf("broadcaster login = %q, want streamer", client.broadcasterLogin)
	}

	saved, _, err := LoadFileConfig(configPath)
	if err != nil {
		t.Fatalf("LoadFileConfig returned error: %v", err)
	}
	if saved.Monitor.Twitch.AccessToken != "new-token" {
		t.Fatalf("saved access token = %q, want new-token", saved.Monitor.Twitch.AccessToken)
	}
	if saved.Monitor.Twitch.RefreshToken != "new-refresh" {
		t.Fatalf("saved refresh token = %q, want new-refresh", saved.Monitor.Twitch.RefreshToken)
	}
	if strings.TrimSpace(saved.Monitor.Twitch.ExpiresAt) == "" {
		t.Fatal("saved expires_at must be set after refresh")
	}
}
