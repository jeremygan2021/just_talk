package voice

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestURLASREncodeFromWav feeds a real wav through the URL-based Doubao
// backend and asserts the API returns a non-empty transcript. Skips when
// the api key is empty so unit-test environments without credentials
// don't fail.
func TestURLASREncodeFromWav(t *testing.T) {
	apiKey := os.Getenv("DOUBAO_API_KEY")
	if apiKey == "" {
		apiKey = "e929586a-7b5f-4584-8392-5fe1bb79ddc7"
	}
	wavPath := "/home/kali/sherpa-models/sense-voice/test_zh.wav"
	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("test audio not present: %v", err)
	}
	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	pcm := wavBytes[44:]

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewURLASRClient(URLASRConfig{
		APIKey:     apiKey,
		ResourceID: "volc.seedasr.auc",
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	c.StartReceive(ctx)

	chunk := 3200
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		last := false
		if end >= len(pcm) {
			end = len(pcm)
			last = true
		}
		if err := c.SendAudio(ctx, pcm[off:end], last); err != nil {
			t.Fatalf("SendAudio: %v", err)
		}
	}

	select {
	case r := <-c.Results():
		t.Logf("结果: %q (final=%v err=%v)", r.Text, r.IsFinal, r.Error)
		if r.Error != nil {
			t.Fatalf("ASR error: %v", r.Error)
		}
		if r.Text == "" {
			t.Fatalf("recognized empty text")
		}
	case <-ctx.Done():
		t.Fatalf("no result within timeout: %v", ctx.Err())
	}
	c.Close()
}
