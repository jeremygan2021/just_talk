package voice

import "context"

// ASRBackend abstracts a speech-to-text engine so the voice pipeline can run
// either the online (Doubao/Volcano streaming WebSocket) engine or a local
// offline engine (sherpa-onnx SenseVoice) without touching the pipeline code.
//
// The existing *ASRClient already satisfies this interface; adding
// *OfflineASRClient lets voice.go drive both backends identically.
type ASRBackend interface {
	// Connect establishes the backend and blocks until it is ready to accept
	// audio. For online this dials the WebSocket; for offline it ensures the
	// local model server is running (and warm) or loads the model.
	Connect(ctx context.Context) error

	// SendAudio streams raw 16k mono S16LE PCM. isLast=true marks the final
	// chunk; for offline backends this triggers the single utterance decode.
	SendAudio(ctx context.Context, pcm []byte, isLast bool) error

	// Results returns a channel of incremental / final recognition results.
	Results() <-chan ASRResult
	// StartReceive launches the backend's background receive loop. For online
	// engines this consumes the WebSocket; offline engines may be a no-op
	// because they resolve results synchronously on the final SendAudio.
	StartReceive(ctx context.Context)
	// Done is closed when the backend is finished and closed.
	Done() <-chan struct{}
	// Final is closed once a definitive final result has been produced.
	Final() <-chan struct{}
	// LastText returns the most recent full recognized text.
	LastText() string
	// Close releases the backend. Best-effort; callers tolerate errors.
	Close() error
}
