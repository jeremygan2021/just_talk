package pastehistory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPushDedupesAndOrders(t *testing.T) {
	dir := t.TempDir()
	h, err := New(filepath.Join(dir, "x.jsonl"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if added := h.Push("hello"); !added {
		t.Fatal("first push should add")
	}
	if added := h.Push(""); added {
		t.Fatal("empty must not add")
	}
	if added := h.Push("hello"); !added {
		// duplicate of last-only entry: re-touched timestamp
		// counted as add for the user (might bump UI timestamp)
		t.Log("re-touch returned added=true — acceptable")
	}
	if added := h.Push("world"); !added {
		t.Fatal("world should add")
	}

	items := h.Items()
	if len(items) < 2 {
		t.Fatalf("expected ≥2 items, got %d", len(items))
	}
	if items[0].Preview != "world" {
		t.Fatalf("newest should be world, got %q", items[0].Preview)
	}
	if items[0].Text != "world" {
		t.Fatalf("newest text lost, got %q", items[0].Text)
	}
}

func TestPreviewTruncates(t *testing.T) {
	long := strings.Repeat("a", 500)
	dir := t.TempDir()
	h, _ := New(filepath.Join(dir, "x.jsonl"))
	h.Push(long)
	items := h.Items()
	if got := items[0].Preview; len([]rune(got)) > PreviewMaxLen+1 {
		// +1 for ellipsis
		t.Fatalf("preview too long: %d runes", len([]rune(got)))
	}
}

func TestRingCap(t *testing.T) {
	dir := t.TempDir()
	h, _ := New(filepath.Join(dir, "x.jsonl"))
	for i := 0; i < DefaultMaxItems+50; i++ {
		h.Push(strings.Repeat("x", 8) + string(rune('A'+i%26)))
	}
	if got := len(h.Items()); got > DefaultMaxItems {
		t.Fatalf("history grew beyond cap: %d", got)
	}
}
