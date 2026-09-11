package voice

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// TestURLASRBackendPipeline drives a URLASRClient through a fake recorder:
// push PCM in chunks, wait for the final result via Results()/Final(),
// then assert text is non-empty. Mirrors what connectASR/streamAudio/
// finishRecordingSession would do at runtime.
func TestURLASRBackendPipeline(t *testing.T) {
	wavPath := "/home/kali/sherpa-models/sense-voice/test_zh.wav"
	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("test audio not present: %v", err)
	}
	pcm, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	pcm = pcm[44:]

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewURLASRClient(URLASRConfig{
		APIKey:     "e929586a-7b5f-4584-8392-5fe1bb79ddc7",
		ResourceID: "volc.seedasr.auc",
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client.StartReceive(ctx)

	var (
		wg       sync.WaitGroup
		seen     []ASRResult
		mu       sync.Mutex
		finalCh  = make(chan string, 1)
		errCh    = make(chan error, 1)
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := range client.Results() {
			mu.Lock()
			seen = append(seen, r)
			mu.Unlock()
			if r.Error != nil {
				errCh <- r.Error
				return
			}
			if r.IsFinal && r.Text != "" {
				finalCh <- r.Text
				return
			}
		}
	}()

	chunk := 3200
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		last := false
		if end >= len(pcm) {
			end = len(pcm)
			last = true
		}
		if err := client.SendAudio(ctx, pcm[off:end], last); err != nil {
			t.Fatalf("SendAudio: %v", err)
		}
	}

	select {
	case text := <-finalCh:
		t.Logf("final transcript: %q", text)
		if text == "" {
			t.Fatalf("empty final transcript")
		}
	case err := <-errCh:
		t.Fatalf("ASR error: %v", err)
	case <-client.Final():
		text := client.LastText()
		t.Logf("final (via Final()): %q", text)
		if text == "" {
			t.Fatalf("empty final text")
		}
	case <-ctx.Done():
		t.Fatalf("timeout: %v", ctx.Err())
	}

	if err := client.Close(); err != nil {
		t.Logf("close: %v", err)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatalf("no results published")
	}
	for _, r := range seen {
		if !r.IsFinal {
			t.Errorf("unexpected partial result: %+v", r)
		}
	}
}
