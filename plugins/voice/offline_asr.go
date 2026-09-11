package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OfflineASRConfig carries the settings needed to run the local sherpa-onnx
// SenseVoice recognizer (the asr_text.py helper from the keli project).
type OfflineASRConfig struct {
	// ModelDir points at the directory holding model.int8.onnx + tokens.txt.
	// Empty uses asr_text.py's default (/root/sherpa-models/sense-voice).
	ModelDir string
	// Script is the path to asr_text.py. Empty auto-discovers common locations.
	Script string
	// WorkerDir is a scratch dir for intermediate wav files. Default os.TempDir().
	WorkerDir string
	// SampleRate of incoming PCM, always 16000 for Just-Talk.
	SampleRate int
}

// OfflineASRClient implements ASRBackend by accumulating the whole utterance's
// PCM, writing it to a wav on the final chunk, then decoding it with a resident
// sherpa-onnx SenseVoice server (asr_text.py --server).
//
// Unlike the online streaming client it cannot return partial hypotheses, so
// the only result published is the final recognized text after SendAudio(last).
type OfflineASRClient struct {
	cfg       OfflineASRConfig
	logger    *slog.Logger
	resultCh  chan ASRResult
	done      chan struct{}
	final     chan struct{}
	finalOnce sync.Once

	mu       sync.Mutex
	pcm      []byte
	lastText string
	started  bool
	closed   bool

	// async process handles
	serverMu    sync.Mutex
	server      *exec.Cmd
	serverIn    *bufio.Writer
	serverOut   *bufio.Scanner
	serverReady chan struct{}
	serverErr   error
}

// NewOfflineASRClient builds a client for the local SenseVoice engine.
func NewOfflineASRClient(cfg OfflineASRConfig, logger *slog.Logger) *OfflineASRClient {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.WorkerDir == "" {
		cfg.WorkerDir = os.TempDir()
	}
	if cfg.Script == "" {
		cfg.Script = findASRScript()
	}
	return &OfflineASRClient{
		cfg:         cfg,
		logger:      logger,
		resultCh:    make(chan ASRResult, 4),
		done:        make(chan struct{}),
		final:       make(chan struct{}),
		serverReady: make(chan struct{}),
	}
}

func findASRScript() string {
	cands := []string{
		"/usr/local/bin/asr_text.py",
		"/home/kali/dev/keliv2.0_view/scripts/asr_text.py",
		"/root/h618-voice-boost/scripts/asr_text.py",
		"/opt/keli/scripts/asr_text.py",
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// Connect ensures the resident SenseVoice server is running and warm. For the
// first utterance this blocks until the model finishes loading (a few seconds);
// later calls return immediately once the server is up.
func (c *OfflineASRClient) Connect(ctx context.Context) error {
	if c.cfg.Script == "" {
		return fmt.Errorf("asr_text.py not found; set OfflineASR.Script or install the keli ASR helper")
	}
	return c.ensureServer(ctx)
}

func (c *OfflineASRClient) ensureServer(ctx context.Context) error {
	// If a server is (being) started, wait on it.
	c.serverMu.Lock()
	running := c.server != nil
	c.serverMu.Unlock()
	if running {
		select {
		case <-c.serverReady:
			c.serverMu.Lock()
			defer c.serverMu.Unlock()
			return c.serverErr
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(60 * time.Second):
			return fmt.Errorf("offline ASR server did not become ready")
		}
	}

	// Atomically start the server.
	c.serverMu.Lock()
	if c.server != nil {
		ready := c.serverReady
		err := c.serverErr
		c.serverMu.Unlock()
		<-ready
		return err
	}
	cmd := exec.Command("python3", c.cfg.Script, "--server")
	if c.cfg.ModelDir != "" {
		cmd.Env = append(os.Environ(), "SENSE_MODEL_DIR="+c.cfg.ModelDir)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		c.serverMu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		c.serverMu.Unlock()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		c.serverMu.Unlock()
		return fmt.Errorf("start asr server: %w", err)
	}
	c.server = cmd
	c.serverOut = bufio.NewScanner(stdoutPipe)
	c.serverIn = bufio.NewWriter(stdinPipe)
	c.serverErr = nil
	c.serverReady = make(chan struct{})
	ready := c.serverReady
	go c.watchServer(cmd)
	c.serverMu.Unlock()

	// Wait for the READY handshake, consuming one scanner line.
	readyCh := make(chan struct{})
	go func() {
		if c.serverOut.Scan() {
			line := c.serverOut.Text()
			if strings.HasPrefix(line, "READY") {
				close(readyCh)
			}
		}
	}()
	select {
	case <-readyCh:
		c.serverMu.Lock()
		c.serverErr = nil
		c.serverMu.Unlock()
		close(ready)
		return nil
	case <-ctx.Done():
		c.killServer()
		return ctx.Err()
	case <-time.After(30 * time.Second):
		c.killServer()
		c.serverMu.Lock()
		c.serverErr = fmt.Errorf("asr server did not print READY")
		c.serverMu.Unlock()
		close(ready)
		return c.serverErr
	}
}

func (c *OfflineASRClient) watchServer(cmd *exec.Cmd) {
	_ = cmd.Wait()
	c.serverMu.Lock()
	if c.server == cmd {
		c.server = nil
	}
	c.serverMu.Unlock()
}

func (c *OfflineASRClient) killServer() {
	c.serverMu.Lock()
	defer c.serverMu.Unlock()
	if c.server != nil && c.server.Process != nil {
		_ = c.server.Process.Kill()
		_, _ = c.server.Process.Wait()
		c.server = nil
	}
}

// SendAudio buffers PCM and, on isLast, writes a wav and decodes it.
func (c *OfflineASRClient) SendAudio(ctx context.Context, pcm []byte, isLast bool) error {
	c.mu.Lock()
	if pcm != nil && len(pcm) > 0 {
		c.pcm = append(c.pcm, pcm...)
	}
	totalLen := len(c.pcm)
	c.mu.Unlock()

	if !isLast {
		return nil // keep buffering
	}

	// Final chunk: decode the buffered audio.
	if totalLen == 0 {
		// Empty buffer (silent recording, mic muted, hotkey released before
		// any audio landed). Publish an empty final result so the voice
		// pipeline does not wait the 15s ASR timeout, and clear lastText so a
		// previous successful transcript is not re-dispatched as this
		// session's result.
		c.mu.Lock()
		c.lastText = ""
		c.mu.Unlock()
		c.resultCh <- ASRResult{Text: "", IsFinal: true}
		c.finalOnce.Do(func() { close(c.final) })
		return nil
	}
	return c.decodeBuffered(ctx, totalLen)
}

func (c *OfflineASRClient) decodeBuffered(ctx context.Context, n int) error {
	c.mu.Lock()
	audio := make([]byte, n)
	copy(audio, c.pcm[:n])
	c.pcm = c.pcm[n:]
	c.mu.Unlock()

	wavPath := filepath.Join(c.cfg.WorkerDir, fmt.Sprintf("justtalk-%d.wav", time.Now().UnixNano()))
	if err := writeWav(wavPath, audio, c.cfg.SampleRate); err != nil {
		return fmt.Errorf("write wav: %w", err)
	}
	defer os.Remove(wavPath)

	text, err := c.decodeFile(ctx, wavPath)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.lastText = text
	c.mu.Unlock()
	c.resultCh <- ASRResult{Text: text, IsFinal: true}
	c.finalOnce.Do(func() { close(c.final) })
	return nil
}

// decodeFile writes the wav path into the resident server's stdin and waits for
// one line of output.
func (c *OfflineASRClient) decodeFile(ctx context.Context, wavPath string) (string, error) {
	c.serverIn.Flush()
	if _, err := c.serverIn.WriteString(wavPath + "\n"); err != nil {
		return "", fmt.Errorf("write to asr server: %w", err)
	}
	if err := c.serverIn.Flush(); err != nil {
		return "", fmt.Errorf("flush asr server: %w", err)
	}

	res := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		if c.serverOut.Scan() {
			res <- c.serverOut.Text()
		} else {
			errCh <- fmt.Errorf("asr server closed (model or script error)")
		}
	}()
	select {
	case text := <-res:
		if strings.HasPrefix(text, "ERR:") {
			return "", fmt.Errorf("asr server: %s", text)
		}
		return strings.TrimSpace(text), nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("asr decode timeout")
	}
}

// Results implements ASRBackend.
func (c *OfflineASRClient) Results() <-chan ASRResult { return c.resultCh }

// StartReceive is a no-op: offline results are resolved synchronously on the
// final SendAudio, so there is no background receive loop to launch.
func (c *OfflineASRClient) StartReceive(ctx context.Context) {}

func (c *OfflineASRClient) Done() <-chan struct{}     { return c.done }
func (c *OfflineASRClient) Final() <-chan struct{}    { return c.final }

func (c *OfflineASRClient) LastText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastText
}

func (c *OfflineASRClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	c.killServer()
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}

// writeWav prepends a 44-byte RIFF/WAVE header to raw PCM. 16-bit mono.
func writeWav(path string, pcm []byte, sr int) error {
	var b bytes.Buffer
	// RIFF chunk
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVE")
	// fmt chunk
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))  // PCM
	binary.Write(&b, binary.LittleEndian, uint16(1))  // mono
	binary.Write(&b, binary.LittleEndian, uint32(sr))
	binary.Write(&b, binary.LittleEndian, uint32(sr*2)) // byte rate
	binary.Write(&b, binary.LittleEndian, uint16(2))    // block align
	binary.Write(&b, binary.LittleEndian, uint16(16))   // bits per sample
	// data chunk
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)

	return os.WriteFile(path, b.Bytes(), 0o644)
}
