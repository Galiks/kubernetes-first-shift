package api

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobName mirrors relayforge/api/job_builder.py job_name: sha256 of the
// idempotency key, prefixed with release-, clamped to 63 chars, no trailing '-'.
func JobName(release, idempotencyKey string) string {
	h := sha256.Sum256([]byte(idempotencyKey))
	sum := hex.EncodeToString(h[:])
	s := release + "-" + sum
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.TrimRight(s, "-")
}

// NewDeliveryID mirrors new_delivery_id: release + "-" + 16 hex chars.
func NewDeliveryID(release string) (string, error) {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate delivery id: %w", err)
	}
	return release + "-" + hex.EncodeToString(b[:]), nil
}

type jobBuildParams struct {
	Release             string
	Namespace           string
	Name                string
	DeliveryID          string
	CanonicalBytes      []byte
	IdemKeyHash         string
	Destination         string
	EventType           string
	PayloadJSON         string
	Image               string
	ImageDigest         string
	SigningSecretName   string
	SigningSecretKey    string
	DestinationsConfig  string
	WorkerSA            string
	BackoffLimit        int
	ActiveDeadline      int
	TTLAfterFinished    int
	PermanentExitCode   int
	SecretRevision      string
}

// BuildJob mirrors relayforge/api/job_builder.py build_job: constructs the
// typed batchv1.Job with EXACT labels/annotations/env/volumes/securityContext
// and the podFailurePolicy rule of the Python reference.
func BuildJob(p jobBuildParams) (*batchv1.Job, error) {
	requestHash := sha256.Sum256(p.CanonicalBytes)
	now := isoFormat(time.Now().UTC())

	image := p.Image
	if p.ImageDigest != "" {
		image = p.Image + "@" + p.ImageDigest
	}

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "relayforge",
				"app.kubernetes.io/instance":  p.Release,
				"app.kubernetes.io/component": "delivery",
				"relayforge/delivery-id":      p.DeliveryID,
			},
			Annotations: map[string]string{
				"relayforge/idempotency-key-hash": p.IdemKeyHash,
				"relayforge/request-hash":         hex.EncodeToString(requestHash[:]),
				"relayforge/delivery-id":          p.DeliveryID,
				"relayforge/created-at":           now,
				"relayforge/format-version":       "1",
				"relayforge/secret-revision":      p.SecretRevision,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(int32(p.BackoffLimit)),
			ActiveDeadlineSeconds:   int64Ptr(int64(p.ActiveDeadline)),
			TTLSecondsAfterFinished: int32Ptr(int32(p.TTLAfterFinished)),
			PodFailurePolicy: &batchv1.PodFailurePolicy{
				Rules: []batchv1.PodFailurePolicyRule{
					{
						Action: batchv1.PodFailurePolicyActionFailJob,
						OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
							ContainerName: stringPtr("worker"),
							Operator:      batchv1.PodFailurePolicyOnExitCodesOpIn,
							Values:        []int32{int32(p.PermanentExitCode)},
						},
					},
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "relayforge",
						"app.kubernetes.io/instance":  p.Release,
						"app.kubernetes.io/component": "delivery",
						"relayforge/delivery-id":      p.DeliveryID,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            p.WorkerSA,
					AutomountServiceAccountToken:  boolPtr(false),
					RestartPolicy:                 corev1.RestartPolicyNever,
					SecurityContext:               &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
						RunAsUser:    int64Ptr(1000),
						FSGroup:      int64Ptr(1000),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "worker",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/relayforge", "worker"},
							Env: []corev1.EnvVar{
								{Name: "RELAYFORGE_DELIVERY_ID", Value: p.DeliveryID},
								{Name: "RELAYFORGE_DESTINATION", Value: p.Destination},
								{Name: "RELAYFORGE_EVENT_TYPE", Value: p.EventType},
								{Name: "RELAYFORGE_PAYLOAD", Value: p.PayloadJSON},
								{Name: "RELAYFORGE_RELEASE", Value: p.Release},
								{Name: "RELAYFORGE_NAMESPACE", Value: p.Namespace},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								ReadOnlyRootFilesystem:   boolPtr(true),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/relayforge/config", ReadOnly: true},
								{Name: "secrets", MountPath: "/etc/relayforge/secrets", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: p.DestinationsConfig},
								},
							},
						},
						{
							Name: "secrets",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: p.SigningSecretName,
									Items: []corev1.KeyToPath{
										{Key: p.SigningSecretKey, Path: "signing-key"},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

// isoFormat renders a UTC time like Python's datetime.now(datetime.UTC).isoformat():
// Z becomes +00:00, fractional seconds are microseconds, omitted when zero.
func isoFormat(t time.Time) string {
	base := "2006-01-02T15:04:05"
	if t.Nanosecond() != 0 {
		base = "2006-01-02T15:04:05.999999"
	}
	return t.Format(base) + "+00:00"
}

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }
func boolPtr(v bool) *bool    { return &v }
func stringPtr(v string) *string { return &v }