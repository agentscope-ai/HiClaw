package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureUser_NewRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_matrix/client/v3/register":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"user_id":      "@alice:test.domain",
				"access_token": "token-abc",
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL:         server.URL,
		Domain:            "test.domain",
		RegistrationToken: "reg-secret",
	}, server.Client())

	creds, err := c.EnsureUser(context.Background(), EnsureUserRequest{
		Username: "alice",
		Password: "pass123",
	})
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if !creds.Created {
		t.Error("expected Created=true for new registration")
	}
	if creds.UserID != "@alice:test.domain" {
		t.Errorf("UserID = %q, want @alice:test.domain", creds.UserID)
	}
	if creds.AccessToken != "token-abc" {
		t.Errorf("AccessToken = %q, want token-abc", creds.AccessToken)
	}
	if creds.Password != "pass123" {
		t.Errorf("Password = %q, want pass123", creds.Password)
	}
}

func TestEnsureUser_ExistingUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_matrix/client/v3/register":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"errcode": "M_USER_IN_USE",
				"error":   "User ID already taken",
			})
		case "/_matrix/client/v3/login":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"access_token": "login-token-xyz",
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL:         server.URL,
		Domain:            "test.domain",
		RegistrationToken: "reg-secret",
	}, server.Client())

	creds, err := c.EnsureUser(context.Background(), EnsureUserRequest{
		Username: "bob",
		Password: "existing-pass",
	})
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if creds.Created {
		t.Error("expected Created=false for existing user")
	}
	if creds.AccessToken != "login-token-xyz" {
		t.Errorf("AccessToken = %q, want login-token-xyz", creds.AccessToken)
	}
}

func TestEnsureUser_GeneratesPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"user_id":      "@gen:test.domain",
			"access_token": "tok",
		})
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL:         server.URL,
		Domain:            "test.domain",
		RegistrationToken: "reg-secret",
	}, server.Client())

	creds, err := c.EnsureUser(context.Background(), EnsureUserRequest{Username: "gen"})
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if len(creds.Password) != 32 { // 16 bytes hex = 32 chars
		t.Errorf("generated password length = %d, want 32", len(creds.Password))
	}
}

func TestCreateRoom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/createRoom" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer creator-token" {
			t.Errorf("Authorization = %q, want Bearer creator-token", auth)
		}

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		if body["preset"] != "trusted_private_chat" {
			t.Errorf("preset = %v, want trusted_private_chat", body["preset"])
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"room_id": "!room123:test.domain",
		})
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL: server.URL,
		Domain:    "test.domain",
	}, server.Client())

	info, err := c.CreateRoom(context.Background(), CreateRoomRequest{
		Name:         "Worker: alice",
		Topic:        "Communication channel",
		Invite:       []string{"@admin:test.domain", "@alice:test.domain"},
		CreatorToken: "creator-token",
		PowerLevels: map[string]int{
			"@admin:test.domain": 100,
			"@alice:test.domain": 0,
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if !info.Created {
		t.Error("expected Created=true")
	}
	if info.RoomID != "!room123:test.domain" {
		t.Errorf("RoomID = %q, want !room123:test.domain", info.RoomID)
	}
}

func TestCreateRoom_ExistingRoomID(t *testing.T) {
	c := NewTuwunelClient(Config{ServerURL: "http://unused", Domain: "d"}, nil)
	info, err := c.CreateRoom(context.Background(), CreateRoomRequest{
		ExistingRoomID: "!existing:domain",
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if info.Created {
		t.Error("expected Created=false for existing room ID")
	}
	if info.RoomID != "!existing:domain" {
		t.Errorf("RoomID = %q, want !existing:domain", info.RoomID)
	}
}

func TestUserID(t *testing.T) {
	c := NewTuwunelClient(Config{Domain: "matrix.example.com:8080"}, nil)
	got := c.UserID("alice")
	want := "@alice:matrix.example.com:8080"
	if got != want {
		t.Errorf("UserID = %q, want %q", got, want)
	}
}

func TestEnsureUser_OrphanRecovery(t *testing.T) {
	var (
		registerCalls int32
		loginCalls    int32
		adminSendHit  int32
		adminLoginHit int32
		dirLookupHit  int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_matrix/client/v3/register":
			atomic.AddInt32(&registerCalls, 1)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"errcode": "M_USER_IN_USE",
				"error":   "User ID already taken",
			})

		case r.URL.Path == "/_matrix/client/v3/login":
			n := atomic.AddInt32(&loginCalls, 1)
			var body struct {
				Identifier struct {
					User string `json:"user"`
				} `json:"identifier"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Identifier.User == "admin" {
				atomic.AddInt32(&adminLoginHit, 1)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"access_token": "admin-token"})
				return
			}
			// First attempt (orphan) fails; retries succeed after the
			// admin reset-password command is "applied".
			if n <= 1 {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"errcode": "M_FORBIDDEN",
					"error":   "Invalid password",
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"access_token": "user-token"})

		case r.URL.Path == "/_matrix/client/v3/directory/room/#admins:test.domain":
			atomic.AddInt32(&dirLookupHit, 1)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"room_id": "!admins:test.domain"})

		case r.Method == http.MethodPut &&
			len(r.URL.Path) > len("/_matrix/client/v3/rooms/") &&
			r.URL.Path[:len("/_matrix/client/v3/rooms/")] == "/_matrix/client/v3/rooms/":
			atomic.AddInt32(&adminSendHit, 1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"event_id":"$evt"}`))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL:         server.URL,
		Domain:            "test.domain",
		RegistrationToken: "reg",
		AdminUser:         "admin",
		AdminPassword:     "adminpw",
	}, server.Client())
	c.orphanRetryBaseDelay = time.Millisecond

	creds, err := c.EnsureUser(context.Background(), EnsureUserRequest{
		Username: "bob",
		Password: "bobpw",
	})
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if creds.Created {
		t.Error("expected Created=false for orphan recovery path")
	}
	if creds.AccessToken != "user-token" {
		t.Errorf("AccessToken = %q, want user-token", creds.AccessToken)
	}
	if atomic.LoadInt32(&adminLoginHit) == 0 {
		t.Error("expected admin login to happen during orphan recovery")
	}
	if atomic.LoadInt32(&dirLookupHit) == 0 {
		t.Error("expected admin room alias to be resolved")
	}
	if atomic.LoadInt32(&adminSendHit) == 0 {
		t.Error("expected admin command to be sent to admin room")
	}
}

func TestAdminCommand(t *testing.T) {
	var (
		sentRoomID string
		sentBody   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_matrix/client/v3/login":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"access_token": "admin-token"})
		case r.URL.Path == "/_matrix/client/v3/directory/room/#admins:test.domain":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"room_id": "!admins:test.domain"})
		case r.Method == http.MethodPut &&
			len(r.URL.Path) > len("/_matrix/client/v3/rooms/") &&
			r.URL.Path[:len("/_matrix/client/v3/rooms/")] == "/_matrix/client/v3/rooms/":
			sentRoomID = r.URL.Path
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			sentBody = body["body"]
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"event_id":"$evt"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{
		ServerURL:     server.URL,
		Domain:        "test.domain",
		AdminUser:     "admin",
		AdminPassword: "adminpw",
	}, server.Client())

	if err := c.AdminCommand(context.Background(), "!admin users force-leave-room @x:test.domain !r:test.domain"); err != nil {
		t.Fatalf("AdminCommand: %v", err)
	}
	if sentRoomID == "" {
		t.Error("expected PUT to rooms/.../send/m.room.message/...")
	}
	if sentBody != "!admin users force-leave-room @x:test.domain !r:test.domain" {
		t.Errorf("sent body = %q", sentBody)
	}
}

func TestListJoinedRooms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/joined_rooms" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer u-tok" {
			t.Errorf("Authorization = %q", auth)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string][]string{
			"joined_rooms": {"!a:d", "!b:d"},
		})
	}))
	defer server.Close()

	c := NewTuwunelClient(Config{ServerURL: server.URL, Domain: "d"}, server.Client())
	rooms, err := c.ListJoinedRooms(context.Background(), "u-tok")
	if err != nil {
		t.Fatalf("ListJoinedRooms: %v", err)
	}
	if len(rooms) != 2 || rooms[0] != "!a:d" || rooms[1] != "!b:d" {
		t.Errorf("rooms = %v", rooms)
	}
}

func TestGeneratePassword(t *testing.T) {
	p1, err := GeneratePassword(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 32 {
		t.Errorf("len = %d, want 32", len(p1))
	}

	p2, _ := GeneratePassword(16)
	if p1 == p2 {
		t.Error("two generated passwords should not be equal")
	}
}
