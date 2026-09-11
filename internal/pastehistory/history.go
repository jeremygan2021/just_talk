// Package pastehistory persists the recent clipboard history used by the
// paste-history companion binary. Entries are stored as JSONL on disk
// (atomic append + truncate) plus an in-memory ring buffer for the GUI.
package pastehistory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one clipboard snapshot.
type Entry struct {
	AtUnixMilli int64  `json:"t"`
	Preview     string `json:"p"`
	Text        string `json:"-"`
}

// PreviewMaxLen is the cap on the human-visible snippet per row.
const PreviewMaxLen = 64

const (
	// DefaultMaxItems caps how many entries we keep in memory and on disk.
	DefaultMaxItems = 200
	// DefaultMaxBytes caps the on-disk file size.
	DefaultMaxBytes = 4 * 1024 * 1024 // 4 MiB
)

// History is an in-memory ring buffer shadowed to a JSONL file. Safe for
// concurrent use.
type History struct {
	mu       sync.Mutex
	items    []Entry
	lastText string
	byKey    map[uint64]int
	maxItems int
	maxBytes int
	filePath string
}

type jsonEntry struct {
	T int64
	P string `json:"p"`
}

// New returns a History backed by `path`. The parent directory is created
// when missing.
func New(path string) (*History, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	h := &History{
		items:    make([]Entry, 0, DefaultMaxItems),
		byKey:    make(map[uint64]int, DefaultMaxItems),
		maxItems: DefaultMaxItems,
		maxBytes: DefaultMaxBytes,
		filePath: path,
	}
	if err := h.load(); err != nil {
		return nil, err
	}
	return h, nil
}

// DefaultPath is the per-user history file location.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "just-talk", "paste-history.jsonl"), nil
}

// Open opens the default history file. Equivalent to New(DefaultPath()).
func Open() (*History, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return New(p)
}

// Push inserts `text` if it differs from the previous tail or any other
// recent entry. Returns whether the entry was added.
func (h *History) Push(text string) (added bool) {
	if text == "" {
		return false
	}
	if len(text) > 256*1024 {
		text = text[:256*1024]
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.lastText == text {
		return false
	}
	k := keyOf(text)
	if idx, ok := h.byKey[k]; ok && idx < len(h.items) && h.items[idx].Text == text {
		h.items[idx].AtUnixMilli = time.Now().UnixMilli()
		h.moveToFront(idx)
		h.lastText = text
		h.fsyncLocked()
		return true
	}

	entry := Entry{
		AtUnixMilli: time.Now().UnixMilli(),
		Preview:     previewOf(text),
		Text:        text,
	}
	h.items = append([]Entry{entry}, h.items...)
	h.reindexLocked()
	h.lastText = text
	h.enforceCapsLocked()
	h.fsyncLocked()
	return true
}

// Items returns a snapshot copy of the current history, newest-first.
func (h *History) Items() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Entry, len(h.items))
	copy(out, h.items)
	return out
}

// FilePath is the on-disk JSONL location.
func (h *History) FilePath() string { return h.filePath }

func (h *History) load() error {
	data, err := os.ReadFile(h.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEntry
		if err := json.Unmarshal(line, &je); err != nil {
			continue
		}
		h.items = append(h.items, Entry{
			AtUnixMilli: je.T,
			Preview:     je.P,
			Text:        "", // text is only held in-memory for the lifetime of this run
		})
	}
	// File lines are written newest-first; the slice already matches.
	if h.maxItems > 0 && len(h.items) > h.maxItems {
		h.items = h.items[:h.maxItems]
	}
	h.reindexLocked()
	return nil
}

func (h *History) fsyncLocked() {
	if h.filePath == "" {
		return
	}
	tmp := h.filePath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, e := range h.items {
		_ = enc.Encode(jsonEntry{T: e.AtUnixMilli, P: e.Preview})
	}
	_ = f.Sync()
	_ = f.Close()
	_ = os.Rename(tmp, h.filePath)
	if h.maxBytes > 0 {
		if info, err := os.Stat(h.filePath); err == nil && info.Size() > int64(h.maxBytes) {
			data, _ := os.ReadFile(h.filePath)
			cut := int64(h.maxBytes) / 2
			if cut > 0 && cut < int64(len(data)) {
				kept := append(data[len(data)-int(cut):], '\n')
				_ = os.WriteFile(h.filePath, kept, 0o644)
			}
		}
	}
}

func (h *History) enforceCapsLocked() {
	if h.maxItems > 0 && len(h.items) > h.maxItems {
		h.items = h.items[:h.maxItems]
		h.reindexLocked()
	}
}

func (h *History) reindexLocked() {
	for i := range h.items {
		h.byKey[keyOf(h.items[i].Text)] = i
	}
}

func (h *History) moveToFront(idx int) {
	if idx <= 0 || idx >= len(h.items) {
		return
	}
	entry := h.items[idx]
	copy(h.items[1:idx+1], h.items[:idx])
	h.items[0] = entry
	h.reindexLocked()
}

func previewOf(text string) string {
	runes := []rune(text)
	if len(runes) <= PreviewMaxLen {
		return text
	}
	return string(runes[:PreviewMaxLen]) + "…"
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// FNV-1a 64-bit, mixed with length so collisions across different payloads
// are vanishingly unlikely for our in-process dedup table.
func fnvSum(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func keyOf(text string) uint64 { return fnvSum(text) ^ uint64(len(text)) }
