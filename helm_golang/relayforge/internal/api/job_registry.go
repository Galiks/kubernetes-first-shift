package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	watchTimeout   = 120 * time.Second
	pendingTTL     = 30.0 // seconds, mirrors _PENDING_TTL
	pruneTTL       = 1200 * time.Second // mirrors _PRUNE_TTL_MULTIPLIER(2) * 600
	watchRetryWait = 5 * time.Second
)

// jobRecord mirrors the per-job dict of job_registry.py.
type jobRecord struct {
	terminal bool
	created  time.Time
}

// JobRegistry mirrors relayforge/api/job_registry.py: a namespace-scoped watch
// of release delivery jobs (selector instance=<release>,component=delivery)
// tracking terminal/created state plus a local pending map for jobs this pod
// created. Thread-safe.
//
// Implementation choice: MANUAL ListWatch loop (not a client-go informer).
// The Python reference streams watch events and reconnects with a fixed 5s
// backoff on any error; a hand-written loop using client-go's watch.Interface
// and wait.Backoff reproduces that behavior directly (initial List to snapshot,
// watch with 120s timeout, relist after disconnect), avoids informer cache
// indirection (the registry only needs the current terminal/created snapshot)
// and keeps stop semantics trivial. Reconnect/backoff is still delegated to
// client-go's wait utilities.
type JobRegistry struct {
	namespace string
	selector  string
	client    kubernetes.Interface

	mu      sync.Mutex
	jobRecs map[string]jobRecord // name -> record (only delivery jobs of this release)
	pending map[string]float64   // name -> monotonic seconds of reservation
	ready   bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewJobRegistry mirrors the Python constructor.
func NewJobRegistry(client kubernetes.Interface, namespace, release string) *JobRegistry {
	selector := "app.kubernetes.io/instance=" + release + ",app.kubernetes.io/component=delivery"
	return &JobRegistry{
		namespace: namespace,
		selector:  selector,
		client:    client,
		jobRecs:   map[string]jobRecord{},
		pending:   map[string]float64{},
		stopCh:    make(chan struct{}),
	}
}

// Start launches the watch loop, mirroring JobRegistry.start.
func (r *JobRegistry) Start() {
	r.mu.Lock()
	if r.ready {
		r.mu.Unlock()
		return
	}
	r.ready = true
	r.mu.Unlock()
	r.wg.Add(1)
	go r.runWatch()
}

// Stop cancels the watch and waits for goroutines, mirroring JobRegistry.stop.
func (r *JobRegistry) Stop() {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return
	}
	r.ready = false
	r.mu.Unlock()
	close(r.stopCh)
	r.wg.Wait()
}

// runWatch mirrors the Python _run loop: list-watch forever, restarting with a
// 5s sleep on any error until stopped.
func (r *JobRegistry) runWatch() {
	defer r.wg.Done()
	backoff := wait.Backoff{Duration: watchRetryWait, Factor: 1.0, Steps: 1}
	for {
		err := r.watchOnce()
		select {
		case <-r.stopCh:
			return
		default:
		}
		if err != nil {
			slog.Warn("job watch error; restarting", "err", err.Error())
			select {
			case <-r.stopCh:
				return
			case <-time.After(backoff.Duration):
			}
		}
	}
}

// watchOnce does the initial List (snapshot) then streams events with a 120s
// timeout, relisting after the watch times out — same cadence as the Python
// w.stream(timeout_seconds=120).
func (r *JobRegistry) watchOnce() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-r.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	listOpts := metav1.ListOptions{LabelSelector: r.selector}
	list, err := r.client.BatchV1().Jobs(r.namespace).List(ctx, listOpts)
	if err != nil {
		return err
	}
	r.mu.Lock()
	for i := range list.Items {
		job := &list.Items[i]
		r.recordLocked(job)
	}
	r.mu.Unlock()

	watcher, err := r.client.BatchV1().Jobs(r.namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:  r.selector,
		ResourceVersion: list.ResourceVersion,
		TimeoutSeconds:  int64Ptr(int64(watchTimeout.Seconds())),
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	start := time.Now()
	for {
		select {
		case <-r.stopCh:
			return nil
		case ev, ok := <-watcher.ResultChan():
			if !ok {
				// A normal end is the 120s server-side timeout. An early close
				// means the stream failed (network drop, 410 Gone, decode
				// error); client-go closes the channel without necessarily
				// delivering an Error event. Back off as Python's blanket
				// exception handler does, instead of hot-relisting.
				if time.Since(start) < watchTimeout-2*time.Second {
					return fmt.Errorf("watch stream closed early after %s", time.Since(start).Round(time.Second))
				}
				return nil // timeout -> relist immediately, like Python
			}
			if ev.Type == k8swatch.Error {
				return fmt.Errorf("watch error event: %v", ev.Object)
			}
			job, isJob := ev.Object.(*batchv1.Job)
			if !isJob {
				continue
			}
			if ev.Type == "DELETED" {
				r.mu.Lock()
				delete(r.jobRecs, job.Name)
				r.mu.Unlock()
				continue
			}
			r.mu.Lock()
			r.recordLocked(job)
			r.mu.Unlock()
		}
	}
}

// recordLocked stores a job's terminal/created state (caller holds the lock).
func (r *JobRegistry) recordLocked(job *batchv1.Job) {
	if job == nil || job.Name == "" {
		return
	}
	name := job.Name
	// watch confirmed the job — the local pending entry is no longer needed.
	delete(r.pending, name)
	r.jobRecs[name] = jobRecord{terminal: isTerminal(job), created: creationTimestamp(job)}
	r.pruneLocked()
}

// isTerminal mirrors _is_terminal.
func isTerminal(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// creationTimestamp mirrors _creation_timestamp: metadata.creationTimestamp
// first, then the relayforge/created-at annotation, else now.
func creationTimestamp(job *batchv1.Job) time.Time {
	if ts := job.CreationTimestamp; !ts.IsZero() {
		return ts.Time.UTC()
	}
	if created := job.Annotations["relayforge/created-at"]; created != "" {
		if t, err := parseISO(created); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

// parseISO parses Python isoformat timestamps (with/without microseconds,
// +00:00 / Z offsets).
func parseISO(s string) (time.Time, error) {
	s = strings.Replace(s, "Z", "+00:00", 1)
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999-07:00",
		"2006-01-02T15:04:05-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, &time.ParseError{}
}

// pruneLocked mirrors _prune: drop terminal jobs older than ttl*600 seconds
// (the TTL controller should already have removed them).
func (r *JobRegistry) pruneLocked() {
	now := time.Now()
	for name, rec := range r.jobRecs {
		if rec.terminal && now.Sub(rec.created) > pruneTTL {
			delete(r.jobRecs, name)
		}
	}
}

// Release mirrors JobRegistry.release: drops the local pending reservation.
func (r *JobRegistry) Release(name string) {
	r.mu.Lock()
	delete(r.pending, name)
	r.mu.Unlock()
}

// TryReserve mirrors JobRegistry.try_reserve: under the lock, prune stale
// pending; return false when the name is already known (repeat key — limit does
// not block); raise 503 BACKPRESSURE when active >= maxActive; else record a
// pending reservation.
func (r *JobRegistry) TryReserve(maxActive int, exemptName string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for n, ts := range r.pending {
		if now.Sub(time.Unix(0, int64(ts*float64(time.Second)))) > pendingTTL*time.Second {
			delete(r.pending, n)
		}
	}
	if exemptName != "" {
		if _, known := r.jobRecs[exemptName]; known {
			return false, nil
		}
		if _, pending := r.pending[exemptName]; pending {
			return false, nil
		}
	}
	active := 0
	for _, rec := range r.jobRecs {
		if !rec.terminal {
			active++
		}
	}
	for n := range r.pending {
		if _, known := r.jobRecs[n]; !known {
			active++
		}
	}
	if active >= maxActive {
		return false, NewApiError(503, "BACKPRESSURE", "too many active jobs", map[string]string{"Retry-After": "5"})
	}
	if exemptName != "" {
		r.pending[exemptName] = float64(now.UnixNano()) / float64(time.Second)
	}
	return true, nil
}

// Has mirrors JobRegistry.has.
func (r *JobRegistry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobRecs[name]; ok {
		return true
	}
	_, ok := r.pending[name]
	return ok
}

// ActiveCount mirrors JobRegistry.active_count: non-terminal jobs plus pending
// reservations not yet confirmed by the watch.
func (r *JobRegistry) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, rec := range r.jobRecs {
		if !rec.terminal {
			count++
		}
	}
	for n := range r.pending {
		if _, known := r.jobRecs[n]; !known {
			count++
		}
	}
	return count
}

// OldestActiveSeconds mirrors JobRegistry.oldest_active_seconds.
func (r *JobRegistry) OldestActiveSeconds() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var oldest *time.Time
	for _, rec := range r.jobRecs {
		if rec.terminal {
			continue
		}
		c := rec.created
		if oldest == nil || c.Before(*oldest) {
			oldest = &c
		}
	}
	if oldest == nil {
		return 0.0
	}
	d := now.Sub(*oldest).Seconds()
	if d < 0 {
		return 0.0
	}
	return d
}