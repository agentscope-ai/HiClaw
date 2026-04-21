package backend

import "testing"

func TestResolveRuntime(t *testing.T) {
	cases := []struct {
		name           string
		reqRuntime     string
		backendDefault string
		want           string
	}{
		{"explicit_wins", RuntimeCopaw, RuntimeOpenClaw, RuntimeCopaw},
		{"explicit_over_empty_default", RuntimeOpenClaw, "", RuntimeOpenClaw},
		{"empty_falls_back_to_backend_default", "", RuntimeCopaw, RuntimeCopaw},
		{"empty_and_no_default_falls_back_to_openclaw", "", "", RuntimeOpenClaw},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveRuntime(tc.reqRuntime, tc.backendDefault)
			if got != tc.want {
				t.Fatalf("ResolveRuntime(%q, %q) = %q, want %q", tc.reqRuntime, tc.backendDefault, got, tc.want)
			}
		})
	}
}

func TestValidRuntime(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{RuntimeOpenClaw, true},
		{RuntimeCopaw, true},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := ValidRuntime(tc.in); got != tc.want {
			t.Fatalf("ValidRuntime(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
