package voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestURLASRSubmitPayloadFormat verifies the submit() POST body
// matches the curl example the user shared: audio carries
// data/format/codec/rate/bits/channel, and request carries all the
// expected fields including enable_channel_split and
// sensitive_words_filter. We intercept the POST with an httptest
// server instead of hitting openspeech.bytedance.com.
func TestURLASRSubmitPayloadFormat(t *testing.T) {
	var calls atomic.Int32
	var captured atomic.Value // map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad json: "+err.Error(), 400)
			return
		}
		captured.Store(payload)

		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing x-api-key", 401)
			return
		}
		if r.Header.Get("X-Api-Resource-Id") != "volc.seedasr.auc" {
			http.Error(w, "wrong resource id", 400)
			return
		}
		if r.Header.Get("X-Api-Sequence") != "-1" {
			http.Error(w, "wrong sequence", 400)
			return
		}

		// Reply with the standard "queued, query later" envelope.
		w.Header().Set("X-Api-Status-Code", "20000000")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewURLASRClient(URLASRConfig{
		APIKey:     "test-key",
		ResourceID: "volc.seedasr.auc",
		SubmitURL:  server.URL,
		QueryURL:   server.URL,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wavBytes := []byte{0x52, 0x49, 0x46, 0x46} // "RIFF" prefix
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client.StartReceive(ctx)
	if err := client.SendAudio(ctx, wavBytes, true); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	// Wait for the submit call to land.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatalf("submit was not called")
	}

	payload, _ := captured.Load().(map[string]interface{})
	if payload == nil {
		t.Fatalf("no payload captured")
	}

	audio, ok := payload["audio"].(map[string]interface{})
	if !ok {
		t.Fatalf("audio field missing or wrong type: %v", payload["audio"])
	}
	if got := audio["format"]; got != "wav" {
		t.Errorf("audio.format = %v, want wav", got)
	}
	if got := audio["codec"]; got != "raw" {
		t.Errorf("audio.codec = %v, want raw", got)
	}
	if got := audio["rate"]; got != float64(16000) {
		t.Errorf("audio.rate = %v, want 16000", got)
	}
	if got := audio["bits"]; got != float64(16) {
		t.Errorf("audio.bits = %v, want 16", got)
	}
	if got := audio["channel"]; got != float64(1) {
		t.Errorf("audio.channel = %v, want 1", got)
	}
	data, ok := audio["data"].(string)
	if !ok || data == "" {
		t.Fatalf("audio.data missing or wrong type")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("audio.data is not valid base64: %v", err)
	}
	// The backend wraps the recorded PCM in a WAV header before
	// base64-encoding it, so the payload size is the PCM bytes plus
	// a 44-byte header. Just verify it's plausibly a WAV and that the
	// original PCM survived round-trip inside it.
	if len(decoded) < 44 {
		t.Fatalf("audio.data base64-decoded payload too short: %d bytes", len(decoded))
	}
	if string(decoded[0:4]) != "RIFF" || string(decoded[8:12]) != "WAVE" {
		t.Fatalf("audio.data base64-decoded payload is not a WAV container: %q", string(decoded[:12]))
	}
	if !contains(string(decoded), string(wavBytes)) {
		t.Fatalf("original PCM bytes not found inside the WAV payload")
	}

	request, ok := payload["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request field missing or wrong type: %v", payload["request"])
	}
	for _, key := range []string{
		"model_name",
		"enable_itn",
		"enable_punc",
		"enable_ddc",
		"enable_speaker_info",
		"enable_channel_split",
		"show_utterances",
		"vad_segment",
		"sensitive_words_filter",
	} {
		if _, ok := request[key]; !ok {
			t.Errorf("request missing key %q", key)
		}
	}
	if request["model_name"] != "bigmodel" {
		t.Errorf("request.model_name = %v, want bigmodel", request["model_name"])
	}
	if request["enable_channel_split"] != false {
		t.Errorf("request.enable_channel_split = %v, want false", request["enable_channel_split"])
	}
	if request["sensitive_words_filter"] != "" {
		t.Errorf("request.sensitive_words_filter = %v, want \"\"", request["sensitive_words_filter"])
	}
}

// TestURLASRSubmitErrorWrapsURL verifies that transport-level
// failures surface with the submit URL embedded in the error
// message, so the user can tell at a glance whether the failure
// happened at DNS, TLS, or the server.
func TestURLASRSubmitErrorWrapsURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewURLASRClient(URLASRConfig{
		APIKey:     "test-key",
		ResourceID: "volc.seedasr.auc",
		SubmitURL:  "http://127.0.0.1:1/never-listens",
		QueryURL:   "http://127.0.0.1:1/never-listens",
		HTTPTimeout: 200 * time.Millisecond,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client.StartReceive(ctx)

	// Trigger submit by sending the final chunk.
	if err := client.SendAudio(ctx, []byte("anything"), true); err == nil {
		t.Fatalf("expected submit error against unreachable host")
	}

	// Wait for the asynchronous error to be published.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case r := <-client.Results():
			if r.Error == nil {
				t.Fatalf("expected an error result, got text=%q", r.Text)
			}
			if !contains(r.Error.Error(), "http://127.0.0.1:1/never-listens") {
				t.Errorf("error should embed submit URL, got %q", r.Error.Error())
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("no result published within deadline")
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}