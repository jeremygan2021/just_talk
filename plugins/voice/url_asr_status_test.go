package voice

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// aucTestServer fakes the Doubao auc submit/query endpoints. queryStatus and
// queryBody are served for every /query call; queryCalls counts them.
type aucTestServer struct {
	*httptest.Server
	queryStatus atomic.Value // string
	queryBody   atomic.Value // string
	queryCalls  atomic.Int32
}

func newAUCTestServer(t *testing.T, queryBody string) *aucTestServer {
	t.Helper()
	s := &aucTestServer{}
	s.queryStatus.Store(aucStatusNoSpeech)
	s.queryBody.Store(queryBody)
	mux := http.NewServeMux()
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", aucStatusOK)
		w.Header().Set("X-Api-Message", "OK")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		s.queryCalls.Add(1)
		w.Header().Set("X-Api-Status-Code", s.queryStatus.Load().(string))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.queryBody.Load().(string)))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func newAUCClient(t *testing.T, s *aucTestServer, pollTimeout time.Duration) *URLASRClient {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewURLASRClient(URLASRConfig{
		APIKey:      "test-key",
		ResourceID:  "volc.seedasr.auc",
		SubmitURL:   s.URL + "/submit",
		QueryURL:    s.URL + "/query",
		PollInitial: 10 * time.Millisecond,
		PollMax:     10 * time.Millisecond,
		PollTimeout: pollTimeout,
	}, logger)
}

// TestURLASRNoSpeechPublishesEmptyFinal is the regression test for the
// "silent clip parks the overlay on WAI until ERR" bug. The auc service
// answers a silent recording with HTTP 200 + X-Api-Status-Code 20000003
// ("no valid speech") and an empty result; the client must treat that as a
// terminal empty transcript and stop polling immediately.
func TestURLASRNoSpeechPublishesEmptyFinal(t *testing.T) {
	s := newAUCTestServer(t, `{"audio_info":{"duration":1500},"result":{"text":""}}`)
	c := newAUCClient(t, s, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := c
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := client.SendAudio(ctx, loudPCM(3200), true); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case <-client.Final():
	case <-time.After(2 * time.Second):
		t.Fatal("Final() did not close for a no-speech result")
	}
	if got := client.LastText(); got != "" {
		t.Fatalf("LastText() = %q, want empty for a no-speech result", got)
	}
	if calls := s.queryCalls.Load(); calls != 1 {
		t.Fatalf("query called %d times, want 1 (no-speech must be terminal)", calls)
	}
	_ = client.Close()
}

// TestURLASRFailedStatusPublishesError checks a genuine task failure is
// surfaced (not silently turned into an empty transcript) and stops polling.
func TestURLASRFailedStatusPublishesError(t *testing.T) {
	s := newAUCTestServer(t, `{}`)
	s.queryStatus.Store(aucStatusFailed)
	c := newAUCClient(t, s, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.SendAudio(ctx, loudPCM(3200), true); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case <-c.Final():
	case <-time.After(2 * time.Second):
		t.Fatal("Final() did not close for a failed task")
	}
	select {
	case r := <-c.Results():
		if r.Error == nil {
			t.Fatalf("expected an error result, got text=%q", r.Text)
		}
	default:
		t.Fatal("no error result published for a failed task")
	}
	if calls := s.queryCalls.Load(); calls != 1 {
		t.Fatalf("query called %d times, want 1 (failure must be terminal)", calls)
	}
	_ = c.Close()
}

func TestURLASRProcessingThenFinal(t *testing.T) {
	s := newAUCTestServer(t, `{"result":{"text":""}}`)
	s.queryStatus.Store(aucStatusProcessing)
	c := newAUCClient(t, s, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.SendAudio(ctx, loudPCM(3200), true); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	// Let the first query observe "processing", then flip to a ready result.
	time.Sleep(30 * time.Millisecond)
	s.queryStatus.Store(aucStatusOK)
	s.queryBody.Store(`{"result":{"text":"hello world"}}`)

	select {
	case <-c.Final():
	case <-time.After(2 * time.Second):
		t.Fatal("Final() did not close after the query became ready")
	}
	if got := c.LastText(); got != "hello world" {
		t.Fatalf("LastText() = %q, want %q", got, "hello world")
	}
	_ = c.Close()
}

// TestURLASRPollBudgetExpiresWithEmptyFinal guards against the infinite poll
// loop: a task that never reaches a terminal state must publish an empty
// final once the budget expires, so finishRecordingSession returns to idle
// instead of waiting out its own timeout and flashing ERR.
func TestURLASRPollBudgetExpiresWithEmptyFinal(t *testing.T) {
	s := newAUCTestServer(t, `{"result":{"text":""}}`)
	s.queryStatus.Store(aucStatusProcessing)
	c := newAUCClient(t, s, 150*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.SendAudio(ctx, loudPCM(3200), true); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case <-c.Final():
	case <-time.After(3 * time.Second):
		t.Fatal("Final() did not close after the poll budget expired")
	}
	if got := c.LastText(); got != "" {
		t.Fatalf("LastText() = %q, want empty after the poll budget expired", got)
	}
	_ = c.Close()
}
