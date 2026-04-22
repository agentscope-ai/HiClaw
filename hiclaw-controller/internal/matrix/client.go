package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Client abstracts Matrix homeserver operations.
// Implementations: TuwunelClient (current), future SynapseClient.
type Client interface {
	// EnsureUser registers a user or logs in if the account already exists.
	// Returns credentials regardless of whether the user was newly created.
	EnsureUser(ctx context.Context, req EnsureUserRequest) (*UserCredentials, error)

	// CreateRoom creates a new Matrix room with the given configuration.
	// When req.RoomAliasName is non-empty the call is idempotent: if a room
	// with that alias already exists on the homeserver, the existing RoomID
	// is resolved and returned with Created=false. Callers SHOULD always
	// populate RoomAliasName for controller-managed rooms to avoid duplicate
	// creation caused by K8s informer cache lag or concurrent reconciles.
	CreateRoom(ctx context.Context, req CreateRoomRequest) (*RoomInfo, error)

	// ResolveRoomAlias looks up the RoomID a Matrix alias currently points
	// to. Returns (roomID, true, nil) on hit, ("", false, nil) when the
	// alias does not exist (M_NOT_FOUND), and ("", false, err) on any
	// other error. The alias argument MUST be the full form
	// "#localpart:server".
	ResolveRoomAlias(ctx context.Context, alias string) (string, bool, error)

	// DeleteRoomAlias removes a Matrix alias so a future CreateRoom with the
	// same localpart starts fresh. Idempotent: a missing alias returns nil.
	// The alias argument MUST be the full form "#localpart:server".
	DeleteRoomAlias(ctx context.Context, alias string) error

	// JoinRoom makes the user identified by token join the given room.
	JoinRoom(ctx context.Context, roomID, userToken string) error

	// LeaveRoom makes the user identified by token leave the given room.
	LeaveRoom(ctx context.Context, roomID, userToken string) error

	// SendMessage sends a plain-text message to a room.
	SendMessage(ctx context.Context, roomID, token, body string) error

	// Login obtains an access token for an existing user.
	Login(ctx context.Context, username, password string) (string, error)

	// AdminCommand sends a `!admin ...` text message to the tuwunel admin
	// bot room (#admins:<domain>). Fire-and-forget: delivery of the
	// message is confirmed but execution of the admin action is not.
	AdminCommand(ctx context.Context, command string) error

	// ListJoinedRooms returns the list of room IDs the user identified
	// by userToken is currently joined to.
	ListJoinedRooms(ctx context.Context, userToken string) ([]string, error)

	// ListRoomMembers returns users currently in the room whose membership
	// is "join" or "invite". leave/ban/knock entries are filtered out.
	// Uses an admin access token internally.
	ListRoomMembers(ctx context.Context, roomID string) ([]RoomMember, error)

	// InviteToRoom invites userID to roomID using an admin access token.
	// Idempotent: returns nil if the user is already joined/invited.
	InviteToRoom(ctx context.Context, roomID, userID string) error

	// KickFromRoom removes userID from roomID using an admin access token.
	// Idempotent: returns nil if the user is not currently in the room.
	KickFromRoom(ctx context.Context, roomID, userID, reason string) error

	// UserID builds a full Matrix user ID from a localpart.
	UserID(localpart string) string
}

// TuwunelClient implements Client for Tuwunel (conduwuit) homeservers.
type TuwunelClient struct {
	config      Config
	http        *http.Client
	adminToken  atomic.Value // cached admin access token (string)
	adminRoomID atomic.Value // cached admin room ID (string), resolved from #admins:<domain>

	// orphanRetryBaseDelay is the base backoff between Login retries
	// after issuing an admin reset-password command. Exposed as a field
	// (not a const) so tests can collapse the delay.
	orphanRetryBaseDelay time.Duration
}

// NewTuwunelClient creates a Matrix client for a Tuwunel homeserver.
func NewTuwunelClient(cfg Config, httpClient *http.Client) *TuwunelClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &TuwunelClient{
		config:               cfg,
		http:                 httpClient,
		orphanRetryBaseDelay: 500 * time.Millisecond,
	}
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
	if err == nil {
		return &UserCredentials{
			UserID:      c.UserID(req.Username),
			AccessToken: token,
			Password:    password,
			Created:     false,
		}, nil
	}

	// Orphan recovery: Matrix still has a userid_password entry for
	// this username (either deactivated by a prior delete flow, or the
	// password was rotated out-of-band), so login with our current
	// password fails. Since Tuwunel cannot hard-delete users, we
	// reactivate via the admin bot's reset-password command and retry
	// login.
	userID := c.UserID(req.Username)
	cmd := fmt.Sprintf("!admin users reset-password %s %s", userID, password)
	if adminErr := c.AdminCommand(ctx, cmd); adminErr != nil {
		return nil, fmt.Errorf("user %s exists but login failed (%v) and orphan recovery failed: %w",
			req.Username, err, adminErr)
	}

	const maxAttempts = 5
	baseDelay := c.orphanRetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	var lastErr = err
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(baseDelay * time.Duration(attempt)):
		}
		token, lastErr = c.Login(ctx, req.Username, password)
		if lastErr == nil {
			return &UserCredentials{
				UserID:      userID,
				AccessToken: token,
				Password:    password,
				Created:     false,
			}, nil
		}
	}
	return nil, fmt.Errorf("user %s exists, orphan recovery issued but login still failing: %w",
		req.Username, lastErr)
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

	if req.RoomAliasName != "" {
		body["room_alias_name"] = req.RoomAliasName
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
		RoomID  string `json:"room_id"`
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		"/_matrix/client/v3/createRoom", token, body, &resp)
	if err != nil {
		return nil, fmt.Errorf("create room %q: %w", req.Name, err)
	}

	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		if resp.RoomID == "" {
			return nil, fmt.Errorf("create room %q: empty room_id in response", req.Name)
		}
		return &RoomInfo{RoomID: resp.RoomID, Created: true}, nil
	}

	// Alias already claimed by a prior reconcile: resolve it and treat as
	// idempotent success. This is the sole path that turns informer-cache
	// lag / concurrent reconciles into a no-op instead of a duplicate room.
	if req.RoomAliasName != "" && resp.ErrCode == "M_ROOM_IN_USE" {
		alias := roomAliasFullFor(c.config.Domain, req.RoomAliasName)
		existingID, found, resolveErr := c.ResolveRoomAlias(ctx, alias)
		if resolveErr != nil {
			return nil, fmt.Errorf("create room %q: alias %s in use, resolve failed: %w",
				req.Name, alias, resolveErr)
		}
		if !found {
			return nil, fmt.Errorf("create room %q: alias %s reported in use but resolve returned not found",
				req.Name, alias)
		}
		return &RoomInfo{RoomID: existingID, Created: false}, nil
	}

	return nil, fmt.Errorf("create room %q: HTTP %d %s %s",
		req.Name, statusCode, resp.ErrCode, resp.Error)
}

// ResolveRoomAlias implements Client.ResolveRoomAlias.
func (c *TuwunelClient) ResolveRoomAlias(ctx context.Context, alias string) (string, bool, error) {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return "", false, fmt.Errorf("resolve alias %s: %w", alias, err)
	}

	var resp struct {
		RoomID  string `json:"room_id"`
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodGet,
		"/_matrix/client/v3/directory/room/"+encodeAlias(alias),
		token, nil, &resp)
	if err != nil {
		return "", false, fmt.Errorf("resolve alias %s: %w", alias, err)
	}
	if statusCode == http.StatusOK {
		if resp.RoomID == "" {
			return "", false, fmt.Errorf("resolve alias %s: empty room_id in response", alias)
		}
		return resp.RoomID, true, nil
	}
	if statusCode == http.StatusNotFound || resp.ErrCode == "M_NOT_FOUND" {
		return "", false, nil
	}
	return "", false, fmt.Errorf("resolve alias %s: HTTP %d %s %s",
		alias, statusCode, resp.ErrCode, resp.Error)
}

// DeleteRoomAlias implements Client.DeleteRoomAlias.
func (c *TuwunelClient) DeleteRoomAlias(ctx context.Context, alias string) error {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("delete alias %s: %w", alias, err)
	}

	var resp struct {
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodDelete,
		"/_matrix/client/v3/directory/room/"+encodeAlias(alias),
		token, nil, &resp)
	if err != nil {
		return fmt.Errorf("delete alias %s: %w", alias, err)
	}
	if statusCode == http.StatusOK {
		return nil
	}
	if statusCode == http.StatusNotFound || resp.ErrCode == "M_NOT_FOUND" {
		return nil
	}
	return fmt.Errorf("delete alias %s: HTTP %d %s %s",
		alias, statusCode, resp.ErrCode, resp.Error)
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

// ensureAdminRoomID resolves the Tuwunel admin room via the well-known
// alias "#admins:<domain>" and caches the result for the lifetime of the
// client. Controller restart re-resolves.
func (c *TuwunelClient) ensureAdminRoomID(ctx context.Context) (string, error) {
	if r, ok := c.adminRoomID.Load().(string); ok && r != "" {
		return r, nil
	}
	alias := fmt.Sprintf("#admins:%s", c.config.Domain)
	path := "/_matrix/client/v3/directory/room/" + url.PathEscape(alias)

	var resp struct {
		RoomID string `json:"room_id"`
	}
	statusCode, err := c.doJSON(ctx, http.MethodGet, path, "", nil, &resp)
	if err != nil {
		return "", fmt.Errorf("resolve admin room alias %s: %w", alias, err)
	}
	if statusCode != http.StatusOK {
		return "", fmt.Errorf("resolve admin room alias %s: HTTP %d", alias, statusCode)
	}
	if resp.RoomID == "" {
		return "", fmt.Errorf("resolve admin room alias %s: empty room_id", alias)
	}
	c.adminRoomID.Store(resp.RoomID)
	return resp.RoomID, nil
}

// AdminCommand sends a command message to the Tuwunel admin bot room as
// the admin user. The bot parses messages starting with "!admin" in the
// admin room. Processing is asynchronous; this call is fire-and-forget.
func (c *TuwunelClient) AdminCommand(ctx context.Context, command string) error {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("admin command: %w", err)
	}
	roomID, err := c.ensureAdminRoomID(ctx)
	if err != nil {
		return fmt.Errorf("admin command: %w", err)
	}
	if err := c.SendMessage(ctx, roomID, token, command); err != nil {
		return fmt.Errorf("admin command: %w", err)
	}
	return nil
}

func (c *TuwunelClient) ListRoomMembers(ctx context.Context, roomID string) ([]RoomMember, error) {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("list members %s: %w", roomID, err)
	}
	encodedRoom := encodeRoomID(roomID)

	var resp struct {
		Chunk []struct {
			StateKey string `json:"state_key"`
			Content  struct {
				Membership string `json:"membership"`
			} `json:"content"`
		} `json:"chunk"`
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodGet,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/members", encodedRoom),
		token, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("list members %s: %w", roomID, err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("list members %s: HTTP %d %s %s",
			roomID, statusCode, resp.ErrCode, resp.Error)
	}

	members := make([]RoomMember, 0, len(resp.Chunk))
	for _, ev := range resp.Chunk {
		if ev.StateKey == "" {
			continue
		}
		if ev.Content.Membership != "join" && ev.Content.Membership != "invite" {
			continue
		}
		members = append(members, RoomMember{
			UserID:     ev.StateKey,
			Membership: ev.Content.Membership,
		})
	}
	return members, nil
}

func (c *TuwunelClient) InviteToRoom(ctx context.Context, roomID, userID string) error {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("invite %s to %s: %w", userID, roomID, err)
	}
	encodedRoom := encodeRoomID(roomID)

	var resp struct {
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/invite", encodedRoom),
		token, map[string]string{"user_id": userID}, &resp)
	if err != nil {
		return fmt.Errorf("invite %s to %s: %w", userID, roomID, err)
	}
	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}
	// Idempotent: user already in the room.
	if statusCode == http.StatusForbidden && resp.ErrCode == "M_FORBIDDEN" {
		lower := strings.ToLower(resp.Error)
		if strings.Contains(lower, "already in") || strings.Contains(lower, "already a member") {
			return nil
		}
	}
	return fmt.Errorf("invite %s to %s: HTTP %d %s %s",
		userID, roomID, statusCode, resp.ErrCode, resp.Error)
}

func (c *TuwunelClient) KickFromRoom(ctx context.Context, roomID, userID, reason string) error {
	token, err := c.ensureAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("kick %s from %s: %w", userID, roomID, err)
	}
	encodedRoom := encodeRoomID(roomID)

	body := map[string]string{"user_id": userID}
	if reason != "" {
		body["reason"] = reason
	}

	var resp struct {
		ErrCode string `json:"errcode"`
		Error   string `json:"error"`
	}

	statusCode, err := c.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/_matrix/client/v3/rooms/%s/kick", encodedRoom),
		token, body, &resp)
	if err != nil {
		return fmt.Errorf("kick %s from %s: %w", userID, roomID, err)
	}
	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}
	// Idempotent: user not in the room (or already left).
	if statusCode == http.StatusNotFound {
		return nil
	}
	if statusCode == http.StatusForbidden && resp.ErrCode == "M_FORBIDDEN" {
		lower := strings.ToLower(resp.Error)
		if strings.Contains(lower, "not in") || strings.Contains(lower, "not a member") ||
			strings.Contains(lower, "cannot kick") {
			return nil
		}
	}
	return fmt.Errorf("kick %s from %s: HTTP %d %s %s",
		userID, roomID, statusCode, resp.ErrCode, resp.Error)
}

// ListJoinedRooms returns the room IDs joined by the user identified by
// the given access token.
func (c *TuwunelClient) ListJoinedRooms(ctx context.Context, userToken string) ([]string, error) {
	var resp struct {
		JoinedRooms []string `json:"joined_rooms"`
	}
	statusCode, err := c.doJSON(ctx, http.MethodGet,
		"/_matrix/client/v3/joined_rooms", userToken, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("list joined rooms: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("list joined rooms: HTTP %d", statusCode)
	}
	return resp.JoinedRooms, nil
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

// roomAliasFullFor builds the full Matrix alias "#localpart:server" from a
// localpart. Exposed at package level so the service layer can synthesize
// the same alias format used by the client when calling ResolveRoomAlias /
// DeleteRoomAlias.
func roomAliasFullFor(domain, localpart string) string {
	return "#" + localpart + ":" + domain
}

// encodeAlias percent-encodes the "#" and ":" characters used by Matrix room
// aliases for safe inclusion in URL paths.
func encodeAlias(alias string) string {
	s := strings.ReplaceAll(alias, "#", "%23")
	s = strings.ReplaceAll(s, ":", "%3A")
	return s
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// txnCounter provides unique transaction IDs for Matrix event sends.
var txnCounter atomic.Int64
