package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Client abstracts Matrix homeserver operations.
// Implementations: TuwunelClient (current), future SynapseClient.
type Client interface {
	// EnsureUser registers a user or logs in if the account already exists.
	// Returns credentials regardless of whether the user was newly created.
	EnsureUser(ctx context.Context, req EnsureUserRequest) (*UserCredentials, error)

	// CreateRoom creates a new Matrix room with the given configuration.
	// If req.ExistingRoomID is set, returns it without creating a new room.
	CreateRoom(ctx context.Context, req CreateRoomRequest) (*RoomInfo, error)

	// JoinRoom makes the user identified by token join the given room.
	JoinRoom(ctx context.Context, roomID, userToken string) error

	// LeaveRoom makes the user identified by token leave the given room.
	LeaveRoom(ctx context.Context, roomID, userToken string) error

	// SendMessage sends a plain-text message to a room.
	SendMessage(ctx context.Context, roomID, token, body string) error

	// Login obtains an access token for an existing user.
	Login(ctx context.Context, username, password string) (string, error)

	// UserID builds a full Matrix user ID from a localpart.
	UserID(localpart string) string
}

// TuwunelClient implements Client for Tuwunel (conduwuit) homeservers.
type TuwunelClient struct {
	config     Config
	http       *http.Client
	adminToken atomic.Value // cached admin access token (string)
}

// NewTuwunelClient creates a Matrix client for a Tuwunel homeserver.
func NewTuwunelClient(cfg Config, httpClient *http.Client) *TuwunelClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &TuwunelClient{config: cfg, http: httpClient}
}

func (c *TuwunelClient) UserID(localpart string) string {
	return fmt.Sprintf("@%s:%s", localpart, c.config.Domain)
}

// ensureAdminToken obtains and caches an admin access token via Login.
func (c *TuwunelClient) ensureAdminToken(ctx context.Context) (string, error) {
	if t, ok := c.adminToken.Load().(string); ok && t != "" {
		return t, nil
	}
	token, err := c.Login(ctx, c.config.AdminUser, c.config.AdminPassword)
	if err != nil {
		return "", fmt.Errorf("admin login: %w", err)
	}
	c.adminToken.Store(token)
	return token, nil
}

func (c *TuwunelClient) EnsureUser(ctx context.Context, req EnsureUserRequest) (*UserCredentials, error) {
	password := req.Password
	if password == "" {
		var err error
		password, err = GeneratePassword(16)
		if err != nil {
			return nil, fmt.Errorf("generate password: %w", err)
		}
	}

	// Try registration first
	regBody := map[string]interface{}{
		"username": req.Username,
		"password": password,
		"auth": map[string]string{
			"type":  "m.login.registration_token",
			"token": c.config.RegistrationToken,
		},
	}
	var regResp struct {
		UserID      string `json:"user_id"`
		AccessToken string `json:"access_token"`
		ErrCode     string `json:"errcode"`
		Error       string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		"/_matrix/client/v3/register", "", regBody, &regResp)
	if err != nil {
		return nil, fmt.Errorf("register user %s: %w", req.Username, err)
	}

	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return &UserCredentials{
			UserID:      regResp.UserID,
			AccessToken: regResp.AccessToken,
			Password:    password,
			Created:     true,
		}, nil
	}

	// Only fall back to login if the user already exists
	if regResp.ErrCode != "" && regResp.ErrCode != "M_USER_IN_USE" {
		return nil, fmt.Errorf("register user %s: %s (%s)", req.Username, regResp.ErrCode, regResp.Error)
	}

	// Registration failed with M_USER_IN_USE — try login
	token, err := c.Login(ctx, req.Username, password)
	if err != nil {
		return nil, fmt.Errorf("user %s exists but login failed: %w", req.Username, err)
	}

	return &UserCredentials{
		UserID:      c.UserID(req.Username),
		AccessToken: token,
		Password:    password,
		Created:     false,
	}, nil
}

func (c *TuwunelClient) Login(ctx context.Context, username, password string) (string, error) {
	body := map[string]interface{}{
		"type": "m.login.password",
		"identifier": map[string]string{
			"type": "m.id.user",
			"user": username,
		},
		"password": password,
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		"/_matrix/client/v3/login", "", body, &resp)
	if err != nil {
		return "", fmt.Errorf("login %s: %w", username, err)
	}
	if statusCode != http.StatusOK {
		return "", fmt.Errorf("login %s: HTTP %d", username, statusCode)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("login %s: empty access token", username)
	}
	return resp.AccessToken, nil
}

func (c *TuwunelClient) CreateRoom(ctx context.Context, req CreateRoomRequest) (*RoomInfo, error) {
	if req.ExistingRoomID != "" {
		return &RoomInfo{RoomID: req.ExistingRoomID, Created: false}, nil
	}

	token := req.CreatorToken
	if token == "" {
		var err error
		token, err = c.ensureAdminToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("create room %q: %w", req.Name, err)
		}
	}

	body := map[string]interface{}{
		"name":      req.Name,
		"topic":     req.Topic,
		"invite":    req.Invite,
		"preset":    "trusted_private_chat",
		"is_direct": req.IsDirect,
	}

	if len(req.PowerLevels) > 0 {
		body["power_level_content_override"] = map[string]interface{}{
			"users": req.PowerLevels,
		}
	}

	if req.E2EE {
		body["initial_state"] = []map[string]interface{}{
			{
				"type":      "m.room.encryption",
				"state_key": "",
				"content": map[string]string{
					"algorithm": "m.megolm.v1.aes-sha2",
				},
			},
		}
	}

	var resp struct {
		RoomID string `json:"room_id"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		"/_matrix/client/v3/createRoom", token, body, &resp)
	if err != nil {
		return nil, fmt.Errorf("create room %q: %w", req.Name, err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("create room %q: HTTP %d", req.Name, statusCode)
	}
	if resp.RoomID == "" {
		return nil, fmt.Errorf("create room %q: empty room_id in response", req.Name)
	}

	return &RoomInfo{RoomID: resp.RoomID, Created: true}, nil
}

func (c *TuwunelClient) JoinRoom(ctx context.Context, roomID, userToken string) error {
	encodedRoom := encodeRoomID(roomID)
	statusCode, err := c.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/join", encodedRoom),
		userToken, map[string]interface{}{}, nil)
	if err != nil {
		return fmt.Errorf("join room %s: %w", roomID, err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("join room %s: HTTP %d", roomID, statusCode)
	}
	return nil
}

func (c *TuwunelClient) LeaveRoom(ctx context.Context, roomID, userToken string) error {
	token := userToken
	if token == "" {
		var err error
		token, err = c.ensureAdminToken(ctx)
		if err != nil {
			return fmt.Errorf("leave room %s: %w", roomID, err)
		}
	}
	encodedRoom := encodeRoomID(roomID)
	statusCode, err := c.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/leave", encodedRoom),
		token, map[string]interface{}{}, nil)
	if err != nil {
		return fmt.Errorf("leave room %s: %w", roomID, err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("leave room %s: HTTP %d", roomID, statusCode)
	}
	return nil
}

func (c *TuwunelClient) SendMessage(ctx context.Context, roomID, token, body string) error {
	encodedRoom := encodeRoomID(roomID)
	txnID := fmt.Sprintf("hc-%d", txnCounter.Add(1))
	msg := map[string]string{
		"msgtype": "m.text",
		"body":    body,
	}

	statusCode, err := c.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message/%s", encodedRoom, txnID),
		token, msg, nil)
	if err != nil {
		return fmt.Errorf("send message to %s: %w", roomID, err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("send message to %s: HTTP %d", roomID, statusCode)
	}
	return nil
}

// doJSON performs an HTTP request with JSON body/response.
// Returns the HTTP status code and any transport/decode error.
// If respOut is nil, the response body is discarded.
func (c *TuwunelClient) doJSON(ctx context.Context, method, path, token string, reqBody interface{}, respOut interface{}) (int, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return 0, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := strings.TrimRight(c.config.ServerURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Clear cached admin token on auth failure so next call re-authenticates
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.adminToken.Store("")
	}

	respBody, _ := io.ReadAll(resp.Body)

	if respOut != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, respOut); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w (body: %s)", err, truncate(respBody, 200))
		}
	}

	return resp.StatusCode, nil
}

// encodeRoomID percent-encodes the "!" in room IDs for URL paths.
func encodeRoomID(roomID string) string {
	return strings.ReplaceAll(roomID, "!", "%21")
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// txnCounter provides unique transaction IDs for Matrix event sends.
var txnCounter atomic.Int64
