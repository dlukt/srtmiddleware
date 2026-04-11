package monitor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultTwitchAPIBase    = "https://api.twitch.tv/helix"
	defaultTwitchAuthBase   = "https://id.twitch.tv/oauth2"
	defaultEventSubWSURL    = "wss://eventsub.wss.twitch.tv/ws"
	tokenRefreshSkew        = 1 * time.Minute
	tokenValidationInterval = 1 * time.Hour
)

var twitchChatScopes = []string{"user:read:chat", "user:write:chat"}

var errUnauthorizedTwitchToken = errors.New("twitch token is unauthorized")

type TwitchIdentity struct {
	UserID string
	Login  string
}

type TwitchClient struct {
	httpClient    *http.Client
	wsDialer      *websocket.Dialer
	logger        *log.Logger
	apiBase       string
	authBase      string
	eventSubWSURL string
	configPath    string

	clientID     string
	clientSecret string
	redirectURL  string
	listenAddr   string

	mu               sync.RWMutex
	accessToken      string
	refreshToken     string
	expiresAt        time.Time
	broadcasterID    string
	broadcasterLogin string
	sceneCommands    map[string]string
	seenMessages     map[string]time.Time
}

type twitchTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type twitchValidateResponse struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	UserID    string   `json:"user_id"`
	Scopes    []string `json:"scopes"`
	ExpiresIn int      `json:"expires_in"`
}

type twitchSendMessageResponse struct {
	Data []struct {
		MessageID  string `json:"message_id"`
		IsSent     bool   `json:"is_sent"`
		DropReason *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"drop_reason"`
	} `json:"data"`
}

type eventSubEnvelope struct {
	Metadata eventSubMetadata `json:"metadata"`
	Payload  eventSubPayload  `json:"payload"`
}

type eventSubMetadata struct {
	MessageID        string `json:"message_id"`
	MessageType      string `json:"message_type"`
	SubscriptionType string `json:"subscription_type"`
}

type eventSubPayload struct {
	Session      eventSubSession      `json:"session"`
	Subscription eventSubSubscription `json:"subscription"`
	Event        chatMessageEvent     `json:"event"`
}

type eventSubSession struct {
	ID           string `json:"id"`
	ReconnectURL string `json:"reconnect_url"`
}

type eventSubSubscription struct {
	Type string `json:"type"`
}

type chatMessageEvent struct {
	BroadcasterUserID string          `json:"broadcaster_user_id"`
	ChatterUserID     string          `json:"chatter_user_id"`
	ChatterUserName   string          `json:"chatter_user_name"`
	MessageID         string          `json:"message_id"`
	Message           chatMessageBody `json:"message"`
	Badges            []chatBadge     `json:"badges"`
}

type chatMessageBody struct {
	Text string `json:"text"`
}

type chatBadge struct {
	SetID string `json:"set_id"`
}

func NewTwitchClient(config Configuration, logger *log.Logger) (*TwitchClient, error) {
	if !config.Twitch.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &TwitchClient{
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		wsDialer:      websocket.DefaultDialer,
		logger:        logger,
		apiBase:       defaultTwitchAPIBase,
		authBase:      defaultTwitchAuthBase,
		eventSubWSURL: defaultEventSubWSURL,
		configPath:    config.ConfigPath,
		clientID:      config.Twitch.ClientID,
		clientSecret:  config.Twitch.ClientSecret,
		redirectURL:   config.Twitch.RedirectURL,
		listenAddr:    config.Twitch.ListenAddr,
		accessToken:   config.Twitch.AccessToken,
		refreshToken:  config.Twitch.RefreshToken,
		expiresAt:     config.Twitch.ExpiresAt,
		sceneCommands: cloneStringMap(config.Twitch.SceneCommands),
		seenMessages:  make(map[string]time.Time),
	}, nil
}

func loginConfiguration(configPath string, cfg FileConfig) (Configuration, error) {
	cfg.Normalize()
	if err := cfg.Monitor.ValidateLogin(); err != nil {
		return Configuration{}, err
	}
	settings, err := cfg.Resolve(configPath)
	if err != nil {
		// Ignore runtime token validation requirements during login.
		return Configuration{
			ConfigPath: configPath,
			Twitch: TwitchRuntimeSettings{
				Enabled:       true,
				ClientID:      cfg.Monitor.Twitch.ClientID,
				ClientSecret:  cfg.Monitor.Twitch.ClientSecret,
				RedirectURL:   cfg.Monitor.Twitch.RedirectURL,
				ListenAddr:    cfg.Monitor.Twitch.ListenAddr,
				SceneCommands: cloneStringMap(cfg.Monitor.Twitch.SceneCommands),
			},
		}, nil
	}
	settings.Twitch.Enabled = true
	return settings, nil
}

func Login(configPath string, cfg FileConfig, logger *log.Logger) (TwitchIdentity, error) {
	settings, err := loginConfiguration(configPath, cfg)
	if err != nil {
		return TwitchIdentity{}, err
	}
	client, err := NewTwitchClient(settings, logger)
	if err != nil {
		return TwitchIdentity{}, err
	}
	tokens, identity, err := client.runLocalOAuth(context.Background())
	if err != nil {
		return TwitchIdentity{}, err
	}
	cfg.Monitor.Twitch.Enabled = true
	cfg.Monitor.Twitch.AccessToken = tokens.AccessToken
	cfg.Monitor.Twitch.RefreshToken = tokens.RefreshToken
	cfg.Monitor.Twitch.ExpiresAt = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	if err := SaveFileConfig(configPath, cfg); err != nil {
		return TwitchIdentity{}, err
	}
	return identity, nil
}

func (t *TwitchClient) Start(ctx context.Context, eventCh chan<- monitorEvent) error {
	if err := t.ensureValidated(ctx); err != nil {
		return err
	}
	startErrCh := make(chan error, 1)
	go t.runValidationLoop(ctx)
	go t.runEventSubLoop(ctx, eventCh, startErrCh)
	return <-startErrCh
}

func (t *TwitchClient) SendReply(ctx context.Context, message, parentMessageID string) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	payload := map[string]any{
		"broadcaster_id": t.broadcasterIDValue(),
		"sender_id":      t.broadcasterIDValue(),
		"message":        message,
	}
	if parentMessageID != "" {
		payload["reply_parent_message_id"] = parentMessageID
	}
	var rsp twitchSendMessageResponse
	if err := t.doHelixJSON(ctx, http.MethodPost, "/chat/messages", payload, &rsp); err != nil {
		return err
	}
	if len(rsp.Data) == 0 {
		return errors.New("twitch chat reply returned no data")
	}
	if rsp.Data[0].IsSent {
		return nil
	}
	if rsp.Data[0].DropReason != nil {
		return fmt.Errorf("twitch chat reply dropped: %s", rsp.Data[0].DropReason.Message)
	}
	return errors.New("twitch chat reply was not sent")
}

func (t *TwitchClient) runLocalOAuth(ctx context.Context) (TwitchTokenState, TwitchIdentity, error) {
	redirectURL, err := url.Parse(t.redirectURL)
	if err != nil {
		return TwitchTokenState{}, TwitchIdentity{}, fmt.Errorf("invalid redirect URL: %w", err)
	}
	state, err := randomHex(16)
	if err != nil {
		return TwitchTokenState{}, TwitchIdentity{}, err
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(redirectURL.Path, func(w http.ResponseWriter, r *http.Request) {
		if gotState := r.URL.Query().Get("state"); gotState != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			select {
			case errCh <- errors.New("oauth state mismatch"):
			default:
			}
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, oauthErr, http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("oauth error: %s", oauthErr):
			default:
			}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case errCh <- errors.New("oauth callback missing code"):
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body>Login successful. You can close this window.</body></html>")
		select {
		case codeCh <- code:
		default:
		}
	})

	server := &http.Server{Addr: t.listenAddr, Handler: mux}
	ln, err := net.Listen("tcp", t.listenAddr)
	if err != nil {
		return TwitchTokenState{}, TwitchIdentity{}, err
	}
	defer ln.Close()
	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case errCh <- serveErr:
			default:
			}
		}
	}()
	defer server.Shutdown(context.Background())

	authURL := buildOAuthURL(t.clientID, t.redirectURL, state)
	fmt.Printf("Open this URL to authorize Twitch chat access:\n%s\n", authURL)
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return TwitchTokenState{}, TwitchIdentity{}, ctx.Err()
	case err := <-errCh:
		return TwitchTokenState{}, TwitchIdentity{}, err
	case code := <-codeCh:
		tokens, err := t.exchangeCode(ctx, code)
		if err != nil {
			return TwitchTokenState{}, TwitchIdentity{}, err
		}
		validateRsp, err := t.validateAccessToken(ctx, tokens.AccessToken)
		if err != nil {
			return TwitchTokenState{}, TwitchIdentity{}, err
		}
		if !hasRequiredScopes(validateRsp.Scopes) {
			return TwitchTokenState{}, TwitchIdentity{}, fmt.Errorf("twitch token is missing required scopes: %v", validateRsp.Scopes)
		}
		t.setTokenState(tokens)
		t.setIdentity(validateRsp.UserID, validateRsp.Login)
		return tokens, TwitchIdentity{UserID: validateRsp.UserID, Login: validateRsp.Login}, nil
	}
}

func (t *TwitchClient) runValidationLoop(ctx context.Context) {
	ticker := time.NewTicker(tokenValidationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.ensureValidated(ctx); err != nil {
				t.logger.Printf("twitch token validation failed: %v", err)
			}
		}
	}
}

func (t *TwitchClient) runEventSubLoop(ctx context.Context, eventCh chan<- monitorEvent, startErrCh chan<- error) {
	currentURL := t.eventSubWSURL
	subscribeOnWelcome := true
	first := true
	backoff := 2 * time.Second
	for {
		conn, welcome, err := t.openEventSubConnection(ctx, currentURL)
		if err != nil {
			if first {
				startErrCh <- err
				return
			}
			t.logger.Printf("twitch eventsub connection failed: %v", err)
			if !sleepWithContext(ctx, backoff) {
				return
			}
			currentURL = t.eventSubWSURL
			subscribeOnWelcome = true
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		if subscribeOnWelcome {
			if err := t.createChatSubscription(ctx, welcome.ID); err != nil {
				conn.Close()
				if first {
					startErrCh <- err
					return
				}
				t.logger.Printf("twitch subscription creation failed: %v", err)
				if !sleepWithContext(ctx, backoff) {
					return
				}
				currentURL = t.eventSubWSURL
				subscribeOnWelcome = true
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
		}
		if ctx.Err() != nil {
			conn.Close()
			return
		}
		if first {
			startErrCh <- nil
			first = false
		}
		backoff = 2 * time.Second
		reconnectURL, err := t.serveEventSubConnection(ctx, conn, eventCh)
		conn.Close()
		if err == nil && reconnectURL != "" {
			currentURL = reconnectURL
			subscribeOnWelcome = false
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			t.logger.Printf("twitch eventsub connection failed: %v", err)
		}
		if !sleepWithContext(ctx, backoff) {
			return
		}
		currentURL = t.eventSubWSURL
		subscribeOnWelcome = true
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (t *TwitchClient) openEventSubConnection(ctx context.Context, wsURL string) (*websocket.Conn, eventSubSession, error) {
	conn, _, err := t.wsDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, eventSubSession{}, err
	}
	welcome, err := readWelcome(conn)
	if err != nil {
		conn.Close()
		return nil, eventSubSession{}, err
	}
	return conn, welcome, nil
}

func (t *TwitchClient) serveEventSubConnection(ctx context.Context, conn *websocket.Conn, eventCh chan<- monitorEvent) (string, error) {
	for {
		var envelope eventSubEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return "", err
		}
		switch envelope.Metadata.MessageType {
		case "session_keepalive":
			continue
		case "session_reconnect":
			return envelope.Payload.Session.ReconnectURL, nil
		case "revocation":
			return "", fmt.Errorf("twitch subscription revoked: %s", envelope.Payload.Subscription.Type)
		case "notification":
			if subscriptionType(envelope) != "channel.chat.message" {
				continue
			}
			command, ok := t.commandFromEvent(envelope.Payload.Event)
			if !ok {
				continue
			}
			select {
			case eventCh <- chatCommandEvent{Command: command}:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
}

func (t *TwitchClient) createChatSubscription(ctx context.Context, sessionID string) error {
	body := map[string]any{
		"type":    "channel.chat.message",
		"version": "1",
		"condition": map[string]string{
			"broadcaster_user_id": t.broadcasterIDValue(),
			"user_id":             t.broadcasterIDValue(),
		},
		"transport": map[string]string{
			"method":     "websocket",
			"session_id": sessionID,
		},
	}
	return t.doHelixJSON(ctx, http.MethodPost, "/eventsub/subscriptions", body, nil)
}

func (t *TwitchClient) commandFromEvent(event chatMessageEvent) (ChatCommand, bool) {
	messageText := strings.TrimSpace(event.Message.Text)
	if messageText == "" {
		return ChatCommand{}, false
	}
	fields := strings.Fields(messageText)
	if len(fields) == 0 {
		return ChatCommand{}, false
	}
	commandName := normalizeCommandName(fields[0])
	if commandName == "" {
		return ChatCommand{}, false
	}
	targetScene := ""
	switch {
	case commandName == autoResumeCommand:
	case !t.hasSceneCommand(commandName):
		return ChatCommand{}, false
	default:
		targetScene = t.sceneForCommand(commandName)
	}
	if t.seenMessage(event.MessageID) {
		return ChatCommand{}, false
	}
	if !isAuthorizedChatUser(event) {
		t.logger.Printf("ignoring unauthorized chat command %s from %s", commandName, event.ChatterUserName)
		return ChatCommand{}, false
	}
	return ChatCommand{
		Command:        commandName,
		TargetScene:    targetScene,
		ReplyParentID:  event.MessageID,
		ChatterDisplay: event.ChatterUserName,
	}, true
}

func (t *TwitchClient) ensureValidated(ctx context.Context) error {
	if err := t.ensureFreshToken(ctx); err != nil {
		return err
	}
	validateRsp, err := t.validateCurrentToken(ctx)
	if err != nil {
		if !errors.Is(err, errUnauthorizedTwitchToken) {
			return err
		}
		if _, refreshErr := t.refreshAccessToken(ctx); refreshErr != nil {
			return refreshErr
		}
		validateRsp, err = t.validateCurrentToken(ctx)
		if err != nil {
			return err
		}
	}
	if !hasRequiredScopes(validateRsp.Scopes) {
		return fmt.Errorf("twitch token is missing required scopes: %v", validateRsp.Scopes)
	}
	t.setIdentity(validateRsp.UserID, validateRsp.Login)
	return nil
}

func (t *TwitchClient) ensureFreshToken(ctx context.Context) error {
	t.mu.RLock()
	expiresAt := t.expiresAt
	t.mu.RUnlock()
	if expiresAt.IsZero() || time.Until(expiresAt) > tokenRefreshSkew {
		return nil
	}
	_, err := t.refreshAccessToken(ctx)
	return err
}

func (t *TwitchClient) refreshAccessToken(ctx context.Context) (TwitchTokenState, error) {
	values := url.Values{}
	values.Set("client_id", t.clientID)
	values.Set("client_secret", t.clientSecret)
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", t.refreshTokenValue())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.authBase+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return TwitchTokenState{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return TwitchTokenState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return TwitchTokenState{}, fmt.Errorf("refresh twitch token: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokenRsp twitchTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenRsp); err != nil {
		return TwitchTokenState{}, err
	}
	tokens := TwitchTokenState{
		AccessToken:  tokenRsp.AccessToken,
		RefreshToken: tokenRsp.RefreshToken,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(tokenRsp.ExpiresIn) * time.Second),
	}
	t.setTokenState(tokens)
	if err := UpdateTwitchTokens(t.configPath, tokens); err != nil {
		t.logger.Printf("persist refreshed twitch token failed: %v", err)
	}
	return tokens, nil
}

func (t *TwitchClient) validateCurrentToken(ctx context.Context) (twitchValidateResponse, error) {
	return t.validateAccessToken(ctx, t.accessTokenValue())
}

func (t *TwitchClient) validateAccessToken(ctx context.Context, accessToken string) (twitchValidateResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.authBase+"/validate", nil)
	if err != nil {
		return twitchValidateResponse{}, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return twitchValidateResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return twitchValidateResponse{}, errUnauthorizedTwitchToken
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return twitchValidateResponse{}, fmt.Errorf("validate twitch token: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var validateRsp twitchValidateResponse
	if err := json.NewDecoder(resp.Body).Decode(&validateRsp); err != nil {
		return twitchValidateResponse{}, err
	}
	return validateRsp, nil
}

func (t *TwitchClient) exchangeCode(ctx context.Context, code string) (TwitchTokenState, error) {
	values := url.Values{}
	values.Set("client_id", t.clientID)
	values.Set("client_secret", t.clientSecret)
	values.Set("code", code)
	values.Set("grant_type", "authorization_code")
	values.Set("redirect_uri", t.redirectURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.authBase+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return TwitchTokenState{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return TwitchTokenState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return TwitchTokenState{}, fmt.Errorf("exchange twitch oauth code: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokenRsp twitchTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenRsp); err != nil {
		return TwitchTokenState{}, err
	}
	return TwitchTokenState{
		AccessToken:  tokenRsp.AccessToken,
		RefreshToken: tokenRsp.RefreshToken,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(tokenRsp.ExpiresIn) * time.Second),
	}, nil
}

func (t *TwitchClient) doHelixJSON(ctx context.Context, method, path string, payload any, out any) error {
	return t.doHelixJSONAttempt(ctx, method, path, payload, out, true)
}

func (t *TwitchClient) doHelixJSONAttempt(ctx context.Context, method, path string, payload any, out any, allowRefresh bool) error {
	if err := t.ensureFreshToken(ctx); err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(t.apiBase, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Client-Id", t.clientID)
	req.Header.Set("Authorization", "Bearer "+t.accessTokenValue())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && allowRefresh {
		if _, refreshErr := t.refreshAccessToken(ctx); refreshErr != nil {
			return refreshErr
		}
		return t.doHelixJSONAttempt(ctx, method, path, payload, out, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("twitch api %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (t *TwitchClient) setTokenState(tokens TwitchTokenState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.accessToken = tokens.AccessToken
	t.refreshToken = tokens.RefreshToken
	t.expiresAt = tokens.ExpiresAt
}

func (t *TwitchClient) setIdentity(userID, login string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.broadcasterID = userID
	t.broadcasterLogin = login
}

func (t *TwitchClient) accessTokenValue() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.accessToken
}

func (t *TwitchClient) refreshTokenValue() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.refreshToken
}

func (t *TwitchClient) broadcasterIDValue() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.broadcasterID
}

func (t *TwitchClient) hasSceneCommand(command string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.sceneCommands[command]
	return ok
}

func (t *TwitchClient) sceneForCommand(command string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sceneCommands[command]
}

func (t *TwitchClient) seenMessage(messageID string) bool {
	if messageID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for key, seenAt := range t.seenMessages {
		if now.Sub(seenAt) > 10*time.Minute {
			delete(t.seenMessages, key)
		}
	}
	if _, ok := t.seenMessages[messageID]; ok {
		return true
	}
	t.seenMessages[messageID] = now
	return false
}

func readWelcome(conn *websocket.Conn) (eventSubSession, error) {
	for {
		var envelope eventSubEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return eventSubSession{}, err
		}
		if envelope.Metadata.MessageType == "session_welcome" {
			return envelope.Payload.Session, nil
		}
	}
}

func subscriptionType(envelope eventSubEnvelope) string {
	if envelope.Metadata.SubscriptionType != "" {
		return envelope.Metadata.SubscriptionType
	}
	return envelope.Payload.Subscription.Type
}

func hasRequiredScopes(scopes []string) bool {
	return slices.Contains(scopes, "user:read:chat") && slices.Contains(scopes, "user:write:chat")
}

func isAuthorizedChatUser(event chatMessageEvent) bool {
	if event.ChatterUserID != "" && event.ChatterUserID == event.BroadcasterUserID {
		return true
	}
	for _, badge := range event.Badges {
		if badge.SetID == "moderator" || badge.SetID == "broadcaster" {
			return true
		}
	}
	return false
}

func buildOAuthURL(clientID, redirectURL, state string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(twitchChatScopes, " "))
	values.Set("state", state)
	return defaultTwitchAuthBase + "/authorize?" + values.Encode()
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
