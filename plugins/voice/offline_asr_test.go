package voice

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestOfflineASREncodeFromWav reads a real wav, feeds its PCM through the
// OfflineASRClient's SendAudio(final) path, and asserts a non-empty transcript.
func TestOfflineASREncodeFromWav(t *testing.T) {
	wavPath := "/home/kali/sherpa-models/sense-voice/test_zh.wav"
	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("test audio not present: %v", err)
	}

	// Read raw wav PCM payload (skip 44-byte header).
	pcm, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	pcm = pcm[44:] // RIFF header

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := OfflineASRConfig{ModelDir: "/home/kali/sherpa-models/sense-voice"}
	c := NewOfflineASRClient(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.SendAudio(ctx, pcm, true); err != nil {
		t.Fatalf("SendAudio(final): %v", err)
	}

	select {
	case r := <-c.Results():
		t.Logf("结果: %q (final=%v err=%v)", r.Text, r.IsFinal, r.Error)
		if r.Text == "" {
			t.Fatalf("recognized empty text")
		}
	case <-ctx.Done():
		t.Fatalf("no result within timeout: %v", ctx.Err())
	}
	c.Close()
}
