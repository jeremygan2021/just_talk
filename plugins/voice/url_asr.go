package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// URLASRConfig configures the Doubao/Volcano "auc" (audio URL conversion)
// file-based recognizer. Unlike the streaming WebSocket backend, this one
// uploads the whole utterance as a base64-encoded WAV blob after the user
// stops recording, then polls the query endpoint for the final transcript.
//
// Auth uses a single x-api-key header (the same token used for the
// submit/query HTTP endpoints). The corresponding streaming WebSocket
// requires a different AppKey/AccessKey pair and is not interchangeable.
type URLASRConfig struct {
	APIKey      string
	ResourceID  string
	SubmitURL   string
	QueryURL    string
	Language    string
	Hotwords    []string
	HTTPTimeout time.Duration
	PollInitial time.Duration
	PollMax     time.Duration
}

// URLASRClient implements ASRBackend using the auc submit/query HTTP API.
// It buffers the entire recording in PCM, then on SendAudio(isLast=true)
// wraps it in a WAV container, base64-encodes it, and submits. Results are
// fetched by polling query until the API reports a non-empty result.
//
// The backend is fire-and-forget per recording: Connect is a cheap HTTP
// sanity check, SendAudio streams chunks into the buffer (only the final
// one triggers a network round trip), StartReceive is a no-op because the
// result is resolved inside SendAudio, and Final/Done close together once
// the final transcript is published.
type URLASRClient struct {
	cfg       URLASRConfig
	logger    *slog.Logger
	httpClient *http.Client
	resultCh  chan ASRResult
	done      chan struct{}
	final     chan struct{}
	finalOnce sync.Once

	mu        sync.Mutex
	pcm       []byte
	lastText  string
	closed    bool
}

// NewURLASRClient builds a file-based ASR backend. Any zero-value timeouts
// fall back to sensible defaults that match the streaming backend's
// end-to-end latency budget (~30s ceiling).
func NewURLASRClient(cfg URLASRConfig, logger *slog.Logger) *URLASRClient {
	if cfg.ResourceID == "" {
		cfg.ResourceID = "volc.seedasr.auc"
	}
	if cfg.SubmitURL == "" {
		cfg.SubmitURL = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit"
	}
	if cfg.QueryURL == "" {
		cfg.QueryURL = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/query"
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if cfg.PollInitial == 0 {
		cfg.PollInitial = 300 * time.Millisecond
	}
	if cfg.PollMax == 0 {
		cfg.PollMax = 3 * time.Second
	}
	return &URLASRClient{
		cfg:        cfg,
		logger:     logger,
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
		resultCh:   make(chan ASRResult, 4),
		done:       make(chan struct{}),
		final:      make(chan struct{}),
	}
}

// Connect verifies credentials up front. We submit a tiny silent wav and
// immediately query; any auth or quota error surfaces here rather than
// after the user has finished dictating.
func (c *URLASRClient) Connect(ctx context.Context) error {
	if c.cfg.APIKey == "" {
		return fmt.Errorf("doubao url asr: api key is empty")
	}
	silentWav := buildSilentWAV(16000, 1, 1600)
	b64 := base64.StdEncoding.EncodeToString(silentWav)
	reqID := uuid.New().String()
	if err := c.submit(ctx, reqID, b64); err != nil {
		return fmt.Errorf("doubao url asr: submit probe failed: %w", err)
	}
	if _, err := c.query(ctx, reqID); err != nil {
		return fmt.Errorf("doubao url asr: query probe failed: %w", err)
	}
	c.logger.Info("doubao url asr connected", "resource_id", c.cfg.ResourceID)
	return nil
}

// SendAudio accumulates PCM. Only the final chunk (isLast=true) round-trips
// to the ASR service.
func (c *URLASRClient) SendAudio(ctx context.Context, pcm []byte, isLast bool) error {
	if pcm != nil && len(pcm) > 0 {
		c.mu.Lock()
		c.pcm = append(c.pcm, pcm...)
		c.mu.Unlock()
	}
	if !isLast {
		return nil
	}
	c.mu.Lock()
	audio := c.pcm
	c.pcm = nil
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil
	}
	if len(audio) == 0 {
		c.publishFinal("")
		return nil
	}
	wav := buildWAV(audio, 16000, 1)
	b64 := base64.StdEncoding.EncodeToString(wav)
	reqID := uuid.New().String()
	c.logger.Debug("doubao url asr: submitting", "pcm_bytes", len(audio), "req_id", reqID)
	if err := c.submit(ctx, reqID, b64); err != nil {
		c.publishError(err)
		return err
	}
	go c.pollAndPublish(reqID)
	return nil
}

func (c *URLASRClient) pollAndPublish(reqID string) {
	delay := c.cfg.PollInitial
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.HTTPTimeout)
		text, err := c.query(ctx, reqID)
		cancel()
		if err != nil {
			c.logger.Warn("doubao url asr: query failed", "req_id", reqID, "error", err)
		} else if text != "" {
			c.logger.Debug("doubao url asr: final", "req_id", reqID, "text_len", len(text))
			c.publishFinal(text)
			return
		}
		select {
		case <-time.After(delay):
		}
		if delay < c.cfg.PollMax {
			delay *= 2
			if delay > c.cfg.PollMax {
				delay = c.cfg.PollMax
			}
		}
	}
}

func (c *URLASRClient) submit(ctx context.Context, reqID, wavB64 string) error {
	payload := map[string]interface{}{
		"user":    map[string]string{"uid": "just-talk"},
		"audio":   map[string]interface{}{"data": wavB64, "format": "wav", "codec": "raw"},
		"request": c.buildRequest(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SubmitURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	req.Header.Set("X-Api-Request-Id", reqID)
	req.Header.Set("X-Api-Sequence", "-1")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	status := resp.Header.Get("X-Api-Status-Code")
	msg := resp.Header.Get("X-Api-Message")
	if resp.StatusCode != http.StatusOK || (status != "" && status != "20000000") {
		return fmt.Errorf("submit http %d status=%s msg=%s body=%s", resp.StatusCode, status, msg, string(raw))
	}
	return nil
}

func (c *URLASRClient) query(ctx context.Context, reqID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.QueryURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	req.Header.Set("X-Api-Request-Id", reqID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		status := resp.Header.Get("X-Api-Status-Code")
		msg := resp.Header.Get("X-Api-Message")
		return "", fmt.Errorf("query http %d status=%s msg=%s body=%s", resp.StatusCode, status, msg, string(raw))
	}
	var parsed struct {
		AudioInfo struct {
			Duration int `json:"duration"`
		} `json:"audio_info"`
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("query decode: %w body=%s", err, string(raw))
	}
	text := parsed.Result.Text
	if text != "" {
		c.mu.Lock()
		c.lastText = text
		c.mu.Unlock()
	}
	return text, nil
}

func (c *URLASRClient) buildRequest() map[string]interface{} {
	r := map[string]interface{}{
		"model_name":          "bigmodel",
		"enable_itn":          true,
		"enable_punc":         true,
		"enable_ddc":          false,
		"enable_speaker_info": false,
		"show_utterances":     false,
		"vad_segment":         false,
	}
	if len(c.cfg.Hotwords) > 0 {
		if ctx, err := hotwordsContext(c.cfg.Hotwords); err == nil && ctx != "" {
			var parsed map[string]interface{}
			if json.Unmarshal([]byte(ctx), &parsed) == nil {
				r["corpus"] = parsed
			}
		}
	}
	if c.cfg.Language != "" && c.cfg.Language != "zh-CN" {
		r["language"] = c.cfg.Language
	}
	return r
}

func (c *URLASRClient) publishFinal(text string) {
	c.mu.Lock()
	if text != "" {
		c.lastText = text
	}
	c.mu.Unlock()
	c.resultCh <- ASRResult{Text: text, IsFinal: true}
	c.finalOnce.Do(func() { close(c.final) })
}

func (c *URLASRClient) publishError(err error) {
	c.resultCh <- ASRResult{Error: err, IsFinal: true}
	c.finalOnce.Do(func() { close(c.final) })
}

// Results implements ASRBackend.
func (c *URLASRClient) Results() <-chan ASRResult { return c.resultCh }

// StartReceive is a no-op: the file-based backend resolves its single final
// result inside SendAudio(isLast=true).
func (c *URLASRClient) StartReceive(ctx context.Context) {}

func (c *URLASRClient) Done() <-chan struct{}  { return c.done }
func (c *URLASRClient) Final() <-chan struct{} { return c.final }

func (c *URLASRClient) LastText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastText
}

func (c *URLASRClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}

func buildWAV(pcm []byte, sampleRate, channels int) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

func buildSilentWAV(sampleRate, channels int, samples int) []byte {
	pcm := make([]byte, samples*channels*2)
	return buildWAV(pcm, sampleRate, channels)
}
