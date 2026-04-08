package auth

import "testing"

func TestParseSAUsername_Admin(t *testing.T) {
	id, err := ParseSAUsername("system:serviceaccount:hiclaw:hiclaw-admin")
	if err != nil {
		t.Fatal(err)
	}
	if id.Role != RoleAdmin || id.Username != "admin" {
		t.Errorf("expected admin, got %+v", id)
	}
}

func TestParseSAUsername_Manager(t *testing.T) {
	id, err := ParseSAUsername("system:serviceaccount:hiclaw:hiclaw-manager")
	if err != nil {
		t.Fatal(err)
	}
	if id.Role != RoleManager || id.Username != "manager" {
		t.Errorf("expected manager, got %+v", id)
	}
}

func TestParseSAUsername_Worker(t *testing.T) {
	id, err := ParseSAUsername("system:serviceaccount:hiclaw:hiclaw-worker-alice")
	if err != nil {
		t.Fatal(err)
	}
	if id.Role != RoleWorker || id.Username != "alice" || id.WorkerName != "alice" {
		t.Errorf("expected worker alice, got %+v", id)
	}
}

func TestParseSAUsername_WorkerHyphenatedName(t *testing.T) {
	id, err := ParseSAUsername("system:serviceaccount:default:hiclaw-worker-alpha-dev")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "alpha-dev" {
		t.Errorf("expected alpha-dev, got %q", id.Username)
	}
}

func TestParseSAUsername_InvalidFormat(t *testing.T) {
	for _, input := range []string{
		"",
		"admin",
		"system:serviceaccount:hiclaw",
		"system:serviceaccount:hiclaw:unknown-sa",
	} {
		if _, err := ParseSAUsername(input); err == nil {
			t.Errorf("expected error for %q", input)
		}
	}
}

func TestSAName(t *testing.T) {
	tests := []struct {
		role, name, expected string
	}{
		{RoleAdmin, "admin", SAAdminName},
		{RoleManager, "manager", SAManagerName},
		{RoleWorker, "alice", "hiclaw-worker-alice"},
		{RoleTeamLeader, "alpha-lead", "hiclaw-worker-alpha-lead"},
	}
	for _, tc := range tests {
		got := SAName(tc.role, tc.name)
		if got != tc.expected {
			t.Errorf("SAName(%q, %q) = %q, want %q", tc.role, tc.name, got, tc.expected)
		}
	}
}
