package api

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// testHarness gives hermetic Kubernetes access: the official client-go fake
// clientset (satisfying kubernetes.Interface) for BatchV1 jobs, plus a
// controllable pod source so the UI logs endpoint can inject pod log bodies and
// errors.
type testHarness struct {
	cs      *fake.Clientset
	podLogs map[string]string // pod name -> raw log body
}

func newHarness(t *testing.T, initObjs ...runtime.Object) *testHarness {
	t.Helper()
	return &testHarness{cs: fake.NewSimpleClientset(initObjs...), podLogs: map[string]string{}}
}

// clients returns the kubeClients used by the handlers (batch = official fake,
// pods = controllable podReader).
func (h *testHarness) clients() *kubeClients {
	return &kubeClients{
		batch: h.cs,
		pods:  &harnessPods{h: h},
	}
}

// addJob seeds a delivery job into the fake tracker with the test namespace.
func (h *testHarness) addJob(job runtime.Object) {
	if j, ok := job.(*batchv1.Job); ok {
		if j.Namespace == "" {
			j.Namespace = "ns"
		}
		// The tracker keys objects by GVK; the zero TypeMeta of a hand-built
		// object would otherwise be stored under an empty group.
		j.TypeMeta = metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"}
	}
	h.cs.Tracker().Add(job)
}

// addPod seeds a pod into the fake tracker.
func (h *testHarness) addPod(pod *corev1.Pod) {
	if pod.Namespace == "" {
		pod.Namespace = "ns"
	}
	pod.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}
	h.cs.Tracker().Add(pod)
}

// setLog configures the raw log body returned for a pod.
func (h *testHarness) setLog(pod, body string) { h.podLogs[pod] = body }

// harnessPods satisfies the podReader interface with hermetic data.
type harnessPods struct{ h *testHarness }

func (p *harnessPods) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	return p.h.cs.CoreV1().Pods(namespace).List(ctx, opts)
}

func (p *harnessPods) ReadPodLog(ctx context.Context, namespace, name string, opts *corev1.PodLogOptions) ([]byte, error) {
	if body, ok := p.h.podLogs[name]; ok {
		return []byte(body), nil
	}
	return nil, &k8sStatusError{code: 404, message: "not found"}
}

// newStatusErr builds the transport error used by create reactors, carrying an
// HTTP status (mirrors kubernetes_asyncio ApiException.status).
func newStatusErr(code int32, msg string) *k8sStatusError { return &k8sStatusError{code: code, message: msg} }

func (e *k8sStatusError) withRetryAfter(seconds string) *k8sStatusError { e.ra = seconds; return e }
func (e *k8sStatusError) network() *k8sStatusError                      { e.message = "client timeout"; return e }

// reactorCallCounter tracks how many create actions the reactor has seen.
type reactorCallCounter struct{ n int }

func (c *reactorCallCounter) calls() int { return c.n }

// reactorCreateErr installs a PrependReactor on create/jobs that consults fn
// for each create; when fn returns an error the reactor short-circuits. fn is
// invoked with the current create count so scenarios can fail only the first
// call.
func reactorCreateErr(cs *fake.Clientset, fn func(count int, jobName string) error) *reactorCallCounter {
	ctr := &reactorCallCounter{}
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		ctr.n++
		err := fn(ctr.n, action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).Name)
		if err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
	return ctr
}

func mkDeliveryJob(name, requestHash, deliveryID string) *batchv1.Job {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels: map[string]string{
				"app.kubernetes.io/name":      "relayforge",
				"app.kubernetes.io/instance":  "relay-test",
				"app.kubernetes.io/component": "delivery",
			},
			Annotations: map[string]string{},
		},
		Spec: batchv1.JobSpec{},
	}
	if requestHash != "" {
		j.Annotations["relayforge/request-hash"] = requestHash
	}
	if deliveryID != "" {
		j.Annotations["relayforge/delivery-id"] = deliveryID
		// The job builder also publishes it as a label; the fake clientset
		// honors label selectors, so tests must seed it too.
		j.Labels["relayforge/delivery-id"] = deliveryID
	}
	return j
}

// mkDeliveryJobNS builds a delivery job in an explicit namespace.
func mkDeliveryJobNS(namespace, name, requestHash, deliveryID string) *batchv1.Job {
	j := mkDeliveryJob(name, requestHash, deliveryID)
	j.Namespace = namespace
	return j
}

// mkPodNS builds a pod in an explicit namespace with a job-name label.
func mkPodNS(namespace, name, jobName string) *corev1.Pod {
	p := mkPod(name, jobName)
	p.Namespace = namespace
	return p
}

func mkPod(name, jobName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"job-name": jobName},
		},
	}
}