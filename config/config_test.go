package config

import "testing"

func TestNormalizeMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "hold", false},
		{"hold", "hold", false},
		{"toggle", "toggle", false},
		{"press", "", true}, // press mode was removed; legacy config values are rejected
		{"unknown", "", true},
		{"HOLD", "", true}, // case-sensitive on purpose to surface typos
	}
	for _, tc := range cases {
		got, err := NormalizeMode(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("NormalizeMode(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeMode(%q): unexpected error %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
