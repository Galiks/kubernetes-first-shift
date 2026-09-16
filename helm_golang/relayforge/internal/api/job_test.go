package api

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// --- job_name (ported from job-name-test.py) ---

func TestJobNameDeterministic(t *testing.T) {
	n1 := JobName("relay-a", "order-service:invoice-1842:v1")
	n2 := JobName("relay-a", "order-service:invoice-1842:v1")
	if n1 != n2 {
		t.Fatalf("deterministic job name expected, got %q vs %q", n1, n2)
	}
}

func TestJobNameMaxLength(t *testing.T) {
	k := ""
	for i := 0; i < 128; i++ {
		k += "x"
	}
	n := JobName("relay-a", k)
	if len(n) > 63 {
		t.Fatalf("job name length %d > 63", len(n))
	}
	// long release + long key must still clamp to 63
	long := ""
	for i := 0; i < 63; i++ {
		long += "r"
	}
	n2 := JobName(long, k)
	if len(n2) > 63 {
		t.Fatalf("job name length %d > 63", len(n2))
	}
}

func TestJobNameDifferentReleases(t *testing.T) {
	n1 := JobName("relay-a", "key")
	n2 := JobName("relay-b", "key")
	if n1 == n2 {
		t.Fatal("different releases must produce different job names")
	}
}

// --- build_job golden assertion ---

func TestBuildJobGolden(t *testing.T) {
	job, err := BuildJob(jobBuildParams{
		Release:            "relay-test",
		Namespace:          "default",
		Name:               "relay-test-abc",
		DeliveryID:         "relay-test-deadbeef",
		CanonicalBytes:     []byte(`{"destination":"test","event_type":"a.b","payload":{"x":1}}`),
		IdemKeyHash:        "idemhash",
		Destination:        "test",
		EventType:          "a.b",
		PayloadJSON:        `{"x":1}`,
		Image:              "localhost:5050/relayforge",
		ImageDigest:        "sha256:abc",
		SigningSecretName:  "relay-test-signing",
		SigningSecretKey:   "signing-key",
		DestinationsConfig: "relay-test-destinations",
		WorkerSA:           "relay-test-worker",
		BackoffLimit:        3,
		ActiveDeadline:      300,
		TTLAfterFinished:    600,
		PermanentExitCode:   12,
		SecretRevision:      "1",
	})
	if err != nil {
		t.Fatalf("build job: %v", err)
	}

	// TypeMeta
	if job.TypeMeta.APIVersion != "batch/v1" || job.TypeMeta.Kind != "Job" {
		t.Fatalf("typemeta = %s/%s", job.TypeMeta.APIVersion, job.TypeMeta.Kind)
	}
	if job.ObjectMeta.Name != "relay-test-abc" || job.ObjectMeta.Namespace != "default" {
		t.Fatalf("metadata name/ns = %s/%s", job.ObjectMeta.Name, job.ObjectMeta.Namespace)
	}

	wantLabels := map[string]string{
		"app.kubernetes.io/name":      "relayforge",
		"app.kubernetes.io/instance":  "relay-test",
		"app.kubernetes.io/component": "delivery",
		"relayforge/delivery-id":      "relay-test-deadbeef",
	}
	for k, v := range wantLabels {
		if job.ObjectMeta.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, job.ObjectMeta.Labels[k], v)
		}
	}

	if job.Annotations["relayforge/idempotency-key-hash"] != "idemhash" {
		t.Errorf("idempotency-key-hash = %q", job.Annotations["relayforge/idempotency-key-hash"])
	}
	if job.Annotations["relayforge/format-version"] != "1" {
		t.Errorf("format-version = %q", job.Annotations["relayforge/format-version"])
	}
	if job.Annotations["relayforge/secret-revision"] != "1" {
		t.Errorf("secret-revision = %q", job.Annotations["relayforge/secret-revision"])
	}
	if job.Annotations["relayforge/created-at"] == "" {
		t.Error("created-at annotation empty")
	}

	// Spec knobs
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 3 {
		t.Errorf("backoffLimit = %v", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 300 {
		t.Errorf("activeDeadlineSeconds = %v", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 600 {
		t.Errorf("ttlSecondsAfterFinished = %v", job.Spec.TTLSecondsAfterFinished)
	}

	// PodFailurePolicy
	pfp := job.Spec.PodFailurePolicy
	if pfp == nil || len(pfp.Rules) != 1 {
		t.Fatalf("podFailurePolicy = %+v", pfp)
	}
	rule := pfp.Rules[0]
	if rule.Action != batchv1.PodFailurePolicyActionFailJob {
		t.Errorf("pfp action = %q, want FailJob", rule.Action)
	}
	if rule.OnExitCodes == nil || rule.OnExitCodes.ContainerName == nil || *rule.OnExitCodes.ContainerName != "worker" {
		t.Errorf("pfp onExitCodes = %+v", rule.OnExitCodes)
	}
	if rule.OnExitCodes.Operator != batchv1.PodFailurePolicyOnExitCodesOpIn {
		t.Errorf("pfp operator = %q", rule.OnExitCodes.Operator)
	}
	if len(rule.OnExitCodes.Values) != 1 || rule.OnExitCodes.Values[0] != 12 {
		t.Errorf("pfp values = %v", rule.OnExitCodes.Values)
	}

	spec := job.Spec.Template.Spec
	// Command
	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %d", len(spec.Containers))
	}
	c := spec.Containers[0]
	if c.Name != "worker" {
		t.Errorf("container name = %q", c.Name)
	}
	wantCmd := []string{"/relayforge", "worker"}
	if len(c.Command) != len(wantCmd) {
		t.Errorf("command = %v, want %v", c.Command, wantCmd)
	}
	for i, x := range wantCmd {
		if c.Command[i] != x {
			t.Errorf("command[%d] = %q, want %q", i, c.Command[i], x)
		}
	}
	wantImage := "localhost:5050/relayforge@sha256:abc"
	if c.Image != wantImage {
		t.Errorf("image = %q, want %q", c.Image, wantImage)
	}
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("imagePullPolicy = %q", c.ImagePullPolicy)
	}

	// Env
	envMap := map[string]string{}
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["RELAYFORGE_DELIVERY_ID"] != "relay-test-deadbeef" ||
		envMap["RELAYFORGE_DESTINATION"] != "test" ||
		envMap["RELAYFORGE_EVENT_TYPE"] != "a.b" ||
		envMap["RELAYFORGE_PAYLOAD"] != `{"x":1}` ||
		envMap["RELAYFORGE_RELEASE"] != "relay-test" ||
		envMap["RELAYFORGE_NAMESPACE"] != "default" {
		t.Errorf("env = %v", envMap)
	}

	// Resources
	if c.Resources.Requests[corev1.ResourceCPU] != resource.MustParse("50m") {
		t.Errorf("requests cpu = %v", c.Resources.Requests[corev1.ResourceCPU])
	}
	if c.Resources.Requests[corev1.ResourceMemory] != resource.MustParse("64Mi") {
		t.Errorf("requests memory = %v", c.Resources.Requests[corev1.ResourceMemory])
	}
	if c.Resources.Limits[corev1.ResourceCPU] != resource.MustParse("200m") {
		t.Errorf("limits cpu = %v", c.Resources.Limits[corev1.ResourceCPU])
	}
	if c.Resources.Limits[corev1.ResourceMemory] != resource.MustParse("128Mi") {
		t.Errorf("limits memory = %v", c.Resources.Limits[corev1.ResourceMemory])
	}

	// Container security context
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil ||
		*c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("container security context = %+v", c.SecurityContext)
	}
	if c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 ||
		c.SecurityContext.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Errorf("capabilities drop = %v", c.SecurityContext.Capabilities)
	}

	// Pod security context
	psc := spec.SecurityContext
	if psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Errorf("pod security runAsNonRoot = %+v", psc)
	}
	if psc == nil || psc.RunAsUser == nil || *psc.RunAsUser != 1000 {
		t.Errorf("pod security runAsUser = %+v", psc)
	}
	if psc == nil || psc.FSGroup == nil || *psc.FSGroup != 1000 {
		t.Errorf("pod security fsGroup = %+v", psc)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod seccomp = %+v", psc)
	}

	// Pod spec basics
	if spec.ServiceAccountName != "relay-test-worker" {
		t.Errorf("serviceAccountName = %q", spec.ServiceAccountName)
	}
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken = %v", spec.AutomountServiceAccountToken)
	}
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q", spec.RestartPolicy)
	}

	// Volumes
	volByName := map[string]corev1.Volume{}
	for _, v := range spec.Volumes {
		volByName[v.Name] = v
	}
	cfgVol, ok := volByName["config"]
	if !ok || cfgVol.ConfigMap == nil || cfgVol.ConfigMap.Name != "relay-test-destinations" {
		t.Errorf("config volume = %+v", cfgVol)
	}
	secVol, ok := volByName["secrets"]
	if !ok || secVol.Secret == nil || secVol.Secret.SecretName != "relay-test-signing" {
		t.Errorf("secrets volume = %+v", secVol)
	} else if len(secVol.Secret.Items) != 1 || secVol.Secret.Items[0].Key != "signing-key" || secVol.Secret.Items[0].Path != "signing-key" {
		t.Errorf("secret items = %+v", secVol.Secret.Items)
	}

	// Volume mounts
	mountByName := map[string]corev1.VolumeMount{}
	for _, m := range c.VolumeMounts {
		mountByName[m.Name] = m
	}
	if m := mountByName["config"]; m.MountPath != "/etc/relayforge/config" || !m.ReadOnly {
		t.Errorf("config mount = %+v", m)
	}
	if m := mountByName["secrets"]; m.MountPath != "/etc/relayforge/secrets" || !m.ReadOnly {
		t.Errorf("secrets mount = %+v", m)
	}
}

// request-hash is sha256 of canonical bytes.
func TestBuildJobRequestHash(t *testing.T) {
	job, _ := BuildJob(jobBuildParams{
		Release: "relay-test", Namespace: "default", Name: "n", DeliveryID: "d",
		CanonicalBytes: []byte(`{"a":1}`), IdemKeyHash: "ih", Image: "i", SecretRevision: "1",
	})
	if job.Annotations["relayforge/request-hash"] == "" {
		t.Fatal("request-hash empty")
	}
}