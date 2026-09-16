// Package cleanup ports relayforge/cleanup/run.py job cleanup to Go. It uses
// the in-cluster clientset directly (this binary runs as the same image, so no
// kubectl is available) and holds only the RBAC granted by
// chart/relayforge/templates/role-cleanup.yaml (deployments get,
// deployments/scale patch, jobs list/delete).
package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"relayforge/internal/config"
	"relayforge/internal/logging"
)

// Run is the frozen cleanup entrypoint. Sequence mirrors cleanup/run.py:
//  1. scale the API Deployment to 0,
//  2. wait up to 60s for replicas==0 and available_replicas==0 (else fail),
//  3. delete ALL delivery Jobs of this release,
//  4. succeed.
func Run(cfg *config.Config) error {
	logging.Setup()

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return err
	}

	ctx := context.Background()
	apps := clientset.AppsV1()
	apiName := cfg.Release + "-relayforge-api"

	// 1. Scale the API down so it cannot create new Jobs while we delete.
	slog.Info("scaling api to 0", "deployment", apiName)
	if _, err := apps.Deployments(cfg.Namespace).Patch(
		ctx, apiName, types.MergePatchType, []byte(`{"spec":{"replicas":0}}`),
		metav1.PatchOptions{}, "scale",
	); err != nil {
		return err
	}

	// 2. Wait for the scale-down to take effect (60 x 1s).
	scaledDown := false
	for i := 0; i < 60; i++ {
		dep, err := apps.Deployments(cfg.Namespace).Get(ctx, apiName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if dep.Status.Replicas == 0 && dep.Status.AvailableReplicas == 0 {
			scaledDown = true
			break
		}
		time.Sleep(time.Second)
	}
	if !scaledDown {
		slog.Error("timeout waiting for api scale-down")
		return errors.New("timeout waiting for api scale-down")
	}

	// 3. Delete ALL delivery Jobs of this release (finished ones included).
	selector := "app.kubernetes.io/instance=" + cfg.Release + ",app.kubernetes.io/component=delivery"
	jobs, err := clientset.BatchV1().Jobs(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		slog.Info("deleting job", "job", job.Name)
		if err := clientset.BatchV1().Jobs(cfg.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{
			PropagationPolicy: &backgroundPropagation,
		}); err != nil {
			slog.Error("delete job", "job", job.Name, "error", err.Error())
		}
	}

	slog.Info("cleanup done")
	return nil
}

var backgroundPropagation = metav1.DeletePropagationBackground