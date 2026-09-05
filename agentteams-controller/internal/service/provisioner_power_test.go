package service

import (
	"context"
	"errors"
	"testing"
)

func TestEnsureRoomPowerLevel_LegacyRoomGrantsLevel(t *testing.T) {
	fake := newFakeTeamMatrix() // no powerStates: legacy room, state never set
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 100); err != nil {
		t.Fatalf("EnsureRoomPowerLevel: %v", err)
	}
	calls := fake.roomStates
	if len(calls) != 1 || calls[0].eventType != "m.room.power_levels" || calls[0].roomID != "!r:hs" {
		t.Fatalf("room state calls=%+v, want single m.room.power_levels write", calls)
	}
	users, ok := calls[0].content["users"].(map[string]interface{})
	if !ok {
		t.Fatalf("users not map[string]interface{}: %#v", calls[0].content["users"])
	}
	if users["@alice:hs"] != 100.0 {
		t.Errorf("alice level=%v, want 100", users["@alice:hs"])
	}
}

func TestEnsureRoomPowerLevel_MergesExistingUsers(t *testing.T) {
	existing := map[string]interface{}{
		"users": map[string]interface{}{
			"@manager:hs": 100.0,
			"@alice:hs":   0.0, // human currently at 0 — the bug
			"@worker:hs":  0.0,
		},
		"users_default": 0.0,
		"state_default": 50.0,
		// Extension fields the write path must survive untouched.
		"events":        map[string]interface{}{"m.room.name": 50.0},
		"invite":        50.0,
		"notifications": map[string]interface{}{"room": 50.0},
	}
	fake := newFakeTeamMatrix()
	fake.powerStates = map[string]map[string]interface{}{"!r:hs": existing}
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 50); err != nil {
		t.Fatalf("EnsureRoomPowerLevel: %v", err)
	}
	users, ok := fake.powerStates["!r:hs"]["users"].(map[string]interface{})
	if !ok {
		t.Fatalf("stored users not a map: %#v", fake.powerStates["!r:hs"])
	}
	if users["@alice:hs"] != 50.0 {
		t.Errorf("alice=%v, want 50", users["@alice:hs"])
	}
	if users["@manager:hs"] != 100.0 || users["@worker:hs"] != 0.0 {
		t.Errorf("existing users disturbed: %v", users)
	}
	// Non-user power-level settings preserved.
	if _, ok := fake.powerStates["!r:hs"]["users_default"]; !ok {
		t.Errorf("users_default dropped")
	}
	if _, ok := fake.powerStates["!r:hs"]["state_default"]; !ok {
		t.Errorf("state_default dropped")
	}
	// Extension fields survive the write — only the target users entry
	// may be mutated, never a rebuilt struct.
	if ev, ok := fake.powerStates["!r:hs"]["events"].(map[string]interface{}); !ok || ev["m.room.name"] != 50.0 {
		t.Errorf("events field dropped or altered: %#v", fake.powerStates["!r:hs"]["events"])
	}
	if inv, ok := fake.powerStates["!r:hs"]["invite"].(float64); !ok || inv != 50.0 {
		t.Errorf("invite field dropped or altered: %#v", fake.powerStates["!r:hs"]["invite"])
	}
	if n, ok := fake.powerStates["!r:hs"]["notifications"].(map[string]interface{}); !ok || n["room"] != 50.0 {
		t.Errorf("notifications field dropped or altered: %#v", fake.powerStates["!r:hs"]["notifications"])
	}
}

// A demoted human must actually be lowered: a user sitting at 100 whose
// permissionLevel drops from 1 to 2 is written at 50, not kept at 100.
func TestEnsureRoomPowerLevel_DemotionRevokesLevel(t *testing.T) {
	existing := map[string]interface{}{
		"users": map[string]interface{}{
			"@manager:hs": 100.0,
			"@alice:hs":   100.0, // was L1, now being demoted to L2
			"@worker:hs":  0.0,
		},
		"ban":  50.0,
		"kick": 50.0,
	}
	fake := newFakeTeamMatrix()
	fake.powerStates = map[string]map[string]interface{}{"!r:hs": existing}
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 50); err != nil {
		t.Fatalf("EnsureRoomPowerLevel: %v", err)
	}
	if len(fake.roomStates) != 1 {
		t.Fatalf("demotion must write, got %d writes", len(fake.roomStates))
	}
	users, ok := fake.powerStates["!r:hs"]["users"].(map[string]interface{})
	if !ok {
		t.Fatalf("stored users not a map: %#v", fake.powerStates["!r:hs"])
	}
	if users["@alice:hs"] != 50.0 {
		t.Errorf("alice=%v, want 50 (demoted from 100)", users["@alice:hs"])
	}
	if users["@manager:hs"] != 100.0 || users["@worker:hs"] != 0.0 {
		t.Errorf("other users disturbed: %v", users)
	}
}

func TestEnsureRoomPowerLevel_ExactMatchNoWrite(t *testing.T) {
	existing := map[string]interface{}{
		"users": map[string]interface{}{"@alice:hs": 50.0, "@manager:hs": 100.0},
	}
	fake := newFakeTeamMatrix()
	fake.powerStates = map[string]map[string]interface{}{"!r:hs": existing}
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 50); err != nil {
		t.Fatalf("EnsureRoomPowerLevel: %v", err)
	}
	if len(fake.roomStates) != 0 {
		t.Errorf("expected no write when already at exactly the desired level, got %d writes", len(fake.roomStates))
	}
}

func TestEnsureRoomPowerLevel_ReadErrorPropagates(t *testing.T) {
	fake := newFakeTeamMatrix()
	fake.powerStateErr = errors.New("matrix down")
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 50)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(fake.roomStates) != 0 {
		t.Errorf("no write allowed after read failure, got %d", len(fake.roomStates))
	}
}

// A second human granted later must not clobber the first human's level —
// the write path must merge against what the previous write stored (via the
// homeserver's JSON round-trip in production, mirrored in the fake).
func TestEnsureRoomPowerLevel_SecondGrantPreservesFirst(t *testing.T) {
	fake := newFakeTeamMatrix()
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 100); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@bob:hs", 50); err != nil {
		t.Fatalf("second grant: %v", err)
	}
	stored := fake.powerStates["!r:hs"]
	users, ok := stored["users"].(map[string]interface{})
	if !ok {
		t.Fatalf("users=%#v, want map after second grant", stored["users"])
	}
	if users["@alice:hs"] != 100.0 || users["@bob:hs"] != 50.0 {
		t.Errorf("users=%v, want alice=100 bob=50 (no clobber)", users)
	}
}

// EnsureRoomPowerLevel must also handle a power_levels state that exists
// without a users map (defensive: treat as empty users).
func TestEnsureRoomPowerLevel_StateWithoutUsersMap(t *testing.T) {
	fake := newFakeTeamMatrix()
	fake.powerStates = map[string]map[string]interface{}{
		"!r:hs": {"users_default": 0.0},
	}
	p := NewProvisioner(ProvisionerConfig{
		Matrix:   fake,
		Creds:    fakeCredentialStore{},
		OSSAdmin: &fakeStorageAdmin{},
	})

	if err := p.EnsureRoomPowerLevel(context.Background(), "!r:hs", "@alice:hs", 50); err != nil {
		t.Fatalf("EnsureRoomPowerLevel: %v", err)
	}
	stored := fake.powerStates["!r:hs"]
	users, ok := stored["users"].(map[string]interface{})
	if !ok || users["@alice:hs"] != 50.0 {
		t.Errorf("users=%#v, want alice=50", stored["users"])
	}
}
