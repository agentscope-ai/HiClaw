package matrix

import "crypto/rand"

// Config holds connection parameters for a Matrix homeserver.
type Config struct {
	ServerURL         string // internal Matrix CS API URL, e.g. http://tuwunel:6167
	Domain            string // Matrix domain for user IDs, e.g. matrix-local.hiclaw.io:8080
	RegistrationToken string // shared registration secret (m.login.registration_token)
	AdminUser         string // global admin username
	AdminPassword     string // global admin password
	E2EEEnabled       bool   // whether to enable E2EE on new rooms
}

// EnsureUserRequest describes a user to register or log in.
type EnsureUserRequest struct {
	Username string // localpart only, e.g. "alice"
	Password string // if empty, a secure random password is generated
}

// UserCredentials holds the result of a successful EnsureUser call.
type UserCredentials struct {
	UserID      string // full Matrix user ID, e.g. @alice:domain
	AccessToken string
	Password    string // the password used (generated or caller-provided)
	Created     bool   // true if newly registered, false if existing user logged in
}

// CreateRoomRequest describes a new Matrix room.
type CreateRoomRequest struct {
	Name         string         // human-readable room name
	Topic        string         // room topic
	Invite       []string       // user IDs to invite
	PowerLevels  map[string]int // userID → power level override
	CreatorToken string         // access token of the room creator
	E2EE         bool           // add m.room.encryption to initial_state

	// IsDirect marks the room as a direct message (1:1) room.
	IsDirect bool

	// ExistingRoomID skips creation and returns this room ID directly.
	// Used for idempotent retries when a room was already created.
	ExistingRoomID string
}

// RoomInfo holds the result of a CreateRoom call.
type RoomInfo struct {
	RoomID  string
	Created bool // true if newly created, false if ExistingRoomID was used
}

// GeneratePassword produces a cryptographically secure random password
// of the given byte length, hex-encoded (output length = 2*n).
func GeneratePassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out), nil
}
