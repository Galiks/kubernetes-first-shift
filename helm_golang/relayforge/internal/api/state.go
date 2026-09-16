package api

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"relayforge/internal/config"
)

// kubeClients exposes the two client surfaces the API uses. batch is the typed
// clientset (satisfies kubernetes.Interface); pods abstracts the CoreV1 pod
// list/log surface so tests can inject hermetic pod data.
type kubeClients struct {
	batch kubernetes.Interface // BatchV1 operations via .BatchV1()
	pods  podReader           // CoreV1 pod list/log surface
}

type podReader interface {
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
	ReadPodLog(ctx context.Context, namespace, name string, opts *corev1.PodLogOptions) ([]byte, error)
}

// State mirrors relayforge/api/state.py — one per API pod.
//
// kubeClients stores the raw clientset twice (batch/core are the same
// interface value); typed accessors pick exactly one of the two to avoid
// ambiguous interface method sets. When k8s is nil the API is uninitialized.
type State struct {
	mu sync.RWMutex

	ready        bool
	destinations map[string]config.Destination
	k8s          *kubeClients
	jobRegistry  *JobRegistry
	backpressure *Backpressure
	clientToken  []byte

	cfg *config.Config
}

func newState(cfg *config.Config) *State {
	return &State{
		destinations: map[string]config.Destination{},
		cfg:          cfg,
	}
}

func (s *State) setReady(v bool) {
	s.mu.Lock()
	s.ready = v
	s.mu.Unlock()
}

func (s *State) isReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *State) setToken(t []byte) {
	s.mu.Lock()
	s.clientToken = t
	s.mu.Unlock()
}

func (s *State) token() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clientToken
}

func (s *State) setDestinations(d map[string]config.Destination) {
	s.mu.Lock()
	s.destinations = d
	s.mu.Unlock()
}

func (s *State) destinationsCopy() map[string]config.Destination {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]config.Destination, len(s.destinations))
	for k, v := range s.destinations {
		out[k] = v
	}
	return out
}

func (s *State) hasDestinations() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.destinations) > 0
}

func (s *State) setK8s(k *kubeClients) {
	s.mu.Lock()
	s.k8s = k
	s.mu.Unlock()
}

func (s *State) k8sClients() *kubeClients {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k8s
}

func (s *State) setBackpressure(b *Backpressure) {
	s.mu.Lock()
	s.backpressure = b
	s.mu.Unlock()
}

func (s *State) backpressureRef() *Backpressure {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backpressure
}

func (s *State) setJobRegistry(r *JobRegistry) {
	s.mu.Lock()
	s.jobRegistry = r
	s.mu.Unlock()
}

func (s *State) jobRegistryRef() *JobRegistry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobRegistry
}

func (s *State) configRef() *config.Config {
	return s.cfg
}