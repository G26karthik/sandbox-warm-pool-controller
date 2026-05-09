package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// transitionState patches the pool-state label and relevant annotations on a pod.
// Uses MergeFrom patch with optimistic concurrency; retries up to 3x on Conflict.
func (r *SandboxWarmPoolReconciler) transitionState(ctx context.Context, pod *corev1.Pod, newState string) error {
	for attempt := 0; attempt < 3; attempt++ {
		patch := client.MergeFrom(pod.DeepCopy())
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Labels[poolStateLabel] = newState
		switch newState {
		case stateIdle:
			pod.Annotations[idleSinceAnnot] = metav1.Now().Format(time.RFC3339)
			pod.Annotations[assignedToAnnot] = ""
			pod.Annotations[assignedAtAnnot] = ""
		}
		err := r.Patch(ctx, pod, patch)
		if err == nil {
			return nil
		}
		if !errors.IsConflict(err) {
			return err
		}
		_ = r.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	}
	return fmt.Errorf("transitionState: exceeded retry limit on conflict")
}
