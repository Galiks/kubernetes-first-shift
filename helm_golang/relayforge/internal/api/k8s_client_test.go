package api

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
)

// --- RetryBudget semantics ---

func TestRetryBudgetCap(t *testing.T) {
	b := NewRetryBudgetWith(60, 12)
	got := 0
	for i := 0; i < 100 && b.TryAcquire(); i++ {
		got++
	}
	if got != 12 {
		t.Fatalf("acquired %d, want 12 (total cap)", got)
	}
}

func runCreate(t *testing.T, h *testHarness, job *batchv1.Job, canonicalHash string, budget *RetryBudget) (*CreateJobResult, error) {
	t.Helper()
	return createJobIdempotent(context.Background(), *h.clients(), "ns", job, budget, canonicalHash)
}

func TestCreatedReturnsOriginal(t *testing.T) {
	h := newHarness(t)
	ctr := reactorCreateErr(h.cs, func(count int, name string) error { return nil })
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Origin != "created" {
		t.Fatalf("origin = %q, want created", res.Origin)
	}
	if ctr.calls() != 1 {
		t.Fatalf("create called %d times, want 1", ctr.calls())
	}
}

func Test409ExistingSameHashReturnsExisting(t *testing.T) {
	h := newHarness(t)
	h.addJob(mkDeliveryJob("j", "h1", "relay-a-x"))
	reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(409, "exists") })
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Origin != "existing" {
		t.Fatalf("origin = %q, want existing", res.Origin)
	}
	if res.Job.Annotations["relayforge/request-hash"] != "h1" {
		t.Fatalf("read job hash = %q", res.Job.Annotations["relayforge/request-hash"])
	}
}

func Test409ConflictingHashRaisesConflict(t *testing.T) {
	h := newHarness(t)
	h.addJob(mkDeliveryJob("j", "h-other", "relay-a-x"))
	reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(409, "exists") })
	_, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	assertApiErr(t, err, 409, "IDEMPOTENCY_CONFLICT")
}

func Test403RaisesWithoutRetry(t *testing.T) {
	h := newHarness(t)
	ctr := reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(403, "forbidden") })
	_, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	assertApiErr(t, err, 500, "K8S_FORBIDDEN")
	if ctr.calls() != 1 {
		t.Fatalf("create called %d times, want 1 (no retry on 403)", ctr.calls())
	}
}

func Test429RetryAfterThenSuccess(t *testing.T) {
	h := newHarness(t)
	ctr := reactorCreateErr(h.cs, func(count int, name string) error {
		if count == 1 {
			return newStatusErr(429, "too many requests").withRetryAfter("0")
		}
		return nil
	})
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudgetWith(60, 5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Origin != "created" {
		t.Fatalf("origin = %q, want created", res.Origin)
	}
	if ctr.calls() != 2 {
		t.Fatalf("create called %d times, want 2", ctr.calls())
	}
}

func Test5xxThenReadFoundReturnsExisting(t *testing.T) {
	h := newHarness(t)
	h.addJob(mkDeliveryJob("j", "h1", "relay-a-x"))
	ctr := reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(500, "boom") })
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Origin != "existing" {
		t.Fatalf("origin = %q, want existing", res.Origin)
	}
	if ctr.calls() != 1 {
		t.Fatalf("create called %d times, want 1 (read matched, no second create)", ctr.calls())
	}
}

func TestTimeoutThen404ThenCreateSuccess(t *testing.T) {
	h := newHarness(t)
	ctr := reactorCreateErr(h.cs, func(count int, name string) error {
		if count == 1 {
			return newStatusErr(0, "client timeout").network()
		}
		return nil
	})
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudgetWith(60, 5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Origin != "created" {
		t.Fatalf("origin = %q, want created", res.Origin)
	}
	if ctr.calls() != 2 {
		t.Fatalf("create called %d times, want 2 (create,read,create)", ctr.calls())
	}
}

func TestRetryBudgetExhaustionRaises503(t *testing.T) {
	h := newHarness(t)
	reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(500, "boom") })
	_, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudgetWith(60, 1))
	assertApiErr(t, err, 503, "RETRY_BUDGET_EXHAUSTED")
}

func TestFaultInjectionTimeoutAfterAcceptedCreate(t *testing.T) {
	// Scenario 02: server accepted the Job but the caller sees 5xx.
	h := newHarness(t)
	ctr := reactorCreateErr(h.cs, func(count int, name string) error { return nil })
	createJobFault.enabled.Store(true)
	defer createJobFault.enabled.Store(false)
	armCreateJobFault()
	_, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	assertApiErr(t, err, 503, "CREATE_AMBIGUOUS")
	if ctr.calls() != 1 {
		t.Fatalf("first request: create called %d times, want 1 (no read)", ctr.calls())
	}

	// Retry of the original request: create -> 409 AlreadyExists (job present)
	// -> read finds it -> origin existing, no second create.
	h.addJob(mkDeliveryJob("j", "h1", "relay-a-x"))
	ctr2 := reactorCreateErr(h.cs, func(count int, name string) error { return newStatusErr(409, "exists") })
	res, err := runCreate(t, h, mkDeliveryJob("j", "h1", "relay-a-x"), "h1", NewRetryBudget())
	if err != nil {
		t.Fatalf("retry error: %v", err)
	}
	if res.Origin != "existing" {
		t.Fatalf("retry origin = %q, want existing", res.Origin)
	}
	if ctr2.calls() != 1 {
		t.Fatalf("retry: create called %d times, want 1", ctr2.calls())
	}
}

func assertApiErr(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(*ApiError)
	if !ok {
		t.Fatalf("got %T %v, want *ApiError", err, err)
	}
	if ae.Status != status || ae.Code != code {
		t.Fatalf("got %d/%q, want %d/%q", ae.Status, ae.Code, status, code)
	}
}