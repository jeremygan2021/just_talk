package config

import "testing"

func TestDefaultMultiTap(t *testing.T) {
	cfg := Default()
	if !cfg.Voice.DoubleTapSend {
		t.Fatal("DoubleTapSend should default to true")
	}
	if !cfg.Voice.TripleTapUndo {
		t.Fatal("TripleTapUndo should default to true")
	}
	if cfg.Voice.MultiTapMs != 500 {
		t.Fatalf("MultiTapMs = %d, want 500", cfg.Voice.MultiTapMs)
	}
	if cfg.Voice.UndoAction != "undo" {
		t.Fatalf("UndoAction = %q, want undo", cfg.Voice.UndoAction)
	}
}

func TestNormalizeUndoAction(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "undo", false},
		{"undo", "undo", false},
		{"clear", "clear", false},
		{"delete", "", true},
		{"UNDO", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeUndoAction(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("NormalizeUndoAction(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeUndoAction(%q): unexpected error %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeUndoAction(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

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
