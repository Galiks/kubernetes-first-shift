package testsink

import (
	"sync"
	"time"
)

// Receipt records what was received for a delivery id. It is the in-memory
// payload/signature/timestamp map required by the port.
type Receipt struct {
	DeliveryID string
	Payload    []byte
	Signature  string
	Timestamp  string
	Applied    bool
	AppliedAt  string
}

// ReceiptStore is an in-memory store of received deliveries plus per-id attempt
// counters (mirrors test_sink/receipts.py, kept in memory for the Go port).
type ReceiptStore struct {
	mu       sync.Mutex
	receipts map[string]*Receipt
	attempts map[string]int
}

func NewReceiptStore() *ReceiptStore {
	return &ReceiptStore{
		receipts: map[string]*Receipt{},
		attempts: map[string]int{},
	}
}

// RecordAttempt increments the HTTP attempt counter for a delivery id and
// returns the new value.
func (s *ReceiptStore) RecordAttempt(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[id]++
	return s.attempts[id]
}

// Apply marks a delivery as applied exactly once, storing the receipt details.
func (s *ReceiptStore) Apply(id string, payload []byte, signature, timestamp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.receipts[id]
	if !ok {
		rec = &Receipt{DeliveryID: id}
		s.receipts[id] = rec
	}
	rec.Payload = payload
	rec.Signature = signature
	rec.Timestamp = timestamp
	rec.Applied = true
	rec.AppliedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

// IsApplied reports whether a delivery has already been applied.
func (s *ReceiptStore) IsApplied(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.receipts[id]
	return ok && rec.Applied
}

// Attempts returns the recorded HTTP attempt count for a delivery id.
func (s *ReceiptStore) Attempts(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts[id]
}

// Count returns the number of receipts stored.
func (s *ReceiptStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.receipts)
}

// Clear wipes all receipts and attempt counters.
func (s *ReceiptStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts = map[string]*Receipt{}
	s.attempts = map[string]int{}
}
