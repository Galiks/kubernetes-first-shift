package testsink

// SinkState is the global state of the test-sink, populated during Run and used
// by the HTTP handlers (mirrors test_sink/state.py).
type SinkState struct {
	VerificationKey []byte
	ControlToken    []byte
	Modes           *ModeController
	Receipts        *ReceiptStore
}

// State is the shared instance used by routes handlers.
var State = &SinkState{}
