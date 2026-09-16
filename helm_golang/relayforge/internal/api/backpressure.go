package api

// Backpressure mirrors relayforge/api/backpressure.py: a per-pod semaphore
// (max concurrent creates) wrapping the registry's atomic check-and-reserve.
type Backpressure struct {
	maxActive           int
	registry            *JobRegistry
	createSemaphore     chan struct{}
}

// NewBackpressure mirrors the Python constructor: create_semaphore is an
// asyncio.Semaphore(max_concurrent_create) — modeled here as an unbuffered-ish
// counting gate of that size.
func NewBackpressure(maxActive, maxConcurrentCreate int, registry *JobRegistry) *Backpressure {
	return &Backpressure{
		maxActive:       maxActive,
		registry:        registry,
		createSemaphore: make(chan struct{}, maxConcurrentCreate),
	}
}

// acquire takes one slot of the concurrent-create semaphore.
func (b *Backpressure) acquire()    { b.createSemaphore <- struct{}{} }

// release returns one slot of the concurrent-create semaphore.
func (b *Backpressure) release() { <-b.createSemaphore }

// CheckAndReserve mirrors Backpressure.check_and_reserve: returns true when a
// slot was reserved; a known job (repeat key) returns false without reserving.
func (b *Backpressure) CheckAndReserve(exemptName string) (bool, error) {
	if b.maxActive <= 0 {
		return false, nil
	}
	return b.registry.TryReserve(b.maxActive, exemptName)
}