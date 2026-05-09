package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

// +kubebuilder:rbac:groups=sandbox.koordinator.sh,resources=sandboxwarmpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sandbox.koordinator.sh,resources=sandboxwarmpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sandbox.koordinator.sh,resources=sandboxwarmpools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type SandboxWarmPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type poolCounts struct {
	idle        int32
	assigned    int32
	recycling   int32
	pending     int32
	terminating int32
	total       int32
	degraded    bool
}

func (r *SandboxWarmPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pool sandboxv1alpha1.SandboxWarmPool
	if err := r.Get(ctx, req.NamespacedName, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !pool.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&pool, finalizerName) {
			if err := r.deleteAllPoolPods(ctx, &pool); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&pool, finalizerName)
			if err := r.Update(ctx, &pool); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&pool, finalizerName) {
		controllerutil.AddFinalizer(&pool, finalizerName)
		if err := r.Update(ctx, &pool); err != nil {
			return ctrl.Result{}, err
		}
	}

	pods, err := r.listPoolPods(ctx, &pool)
	if err != nil {
		return ctrl.Result{}, err
	}

	counts, err := r.syncPodStates(ctx, &pool, pods)
	if err != nil {
		return ctrl.Result{}, err
	}

	scaled, err := r.scaleUp(ctx, &pool, counts)
	if err != nil {
		return ctrl.Result{}, err
	}

	expired, err := r.expireIdlePods(ctx, &pool, pods, counts)
	if err != nil {
		return ctrl.Result{}, err
	}

	if scaled || expired {
		now := metav1.Now()
		pool.Status.LastScaleTime = &now
	}

	if err := r.updateStatus(ctx, &pool, counts); err != nil {
		return ctrl.Result{}, err
	}

	logger.V(1).Info("reconciled sandbox warm pool", "idle", counts.idle, "assigned", counts.assigned, "pending", counts.pending, "recycling", counts.recycling, "terminating", counts.terminating)

	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *SandboxWarmPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.SandboxWarmPool{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *SandboxWarmPoolReconciler) listPoolPods(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool) ([]corev1.Pod, error) {
	var podList corev1.PodList
	if err := r.List(ctx, &podList,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{
			poolNameLabel:      pool.Name,
			poolNamespaceLabel: pool.Namespace,
		},
	); err != nil {
		return nil, err
	}
	return podList.Items, nil
}

func (r *SandboxWarmPoolReconciler) deleteAllPoolPods(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool) error {
	pods, err := r.listPoolPods(ctx, pool)
	if err != nil {
		return err
	}
	for i := range pods {
		if err := r.Delete(ctx, &pods[i]); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

func (r *SandboxWarmPoolReconciler) syncPodStates(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool, pods []corev1.Pod) (*poolCounts, error) {
	counts := &poolCounts{}

	for i := range pods {
		pod := &pods[i]
		state := getPoolState(pod)
		if state == "" {
			if err := r.transitionState(ctx, pod, statePending); err != nil {
				return nil, err
			}
			state = statePending
		}

		if isPodDegraded(pod) {
			counts.degraded = true
		}

		if pod.DeletionTimestamp != nil {
			if state != stateTerminating {
				if err := r.transitionState(ctx, pod, stateTerminating); err != nil {
					return nil, err
				}
				state = stateTerminating
			}
			counts.terminating++
			counts.total++
			continue
		}

		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			if state != stateTerminating {
				if err := r.transitionState(ctx, pod, stateTerminating); err != nil {
					return nil, err
				}
				state = stateTerminating
			}
			if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
				return nil, err
			}
			counts.terminating++
			counts.total++
			continue
		}

		switch state {
		case statePending:
			if isPodReady(pod) {
				if err := r.transitionState(ctx, pod, stateIdle); err != nil {
					return nil, err
				}
				state = stateIdle
			}
		case stateRecycling:
			if isPodReady(pod) {
				if err := r.transitionState(ctx, pod, stateIdle); err != nil {
					return nil, err
				}
				state = stateIdle
			} else {
				if err := r.transitionState(ctx, pod, stateTerminating); err != nil {
					return nil, err
				}
				if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
					return nil, err
				}
				state = stateTerminating
			}
		case stateTerminating:
			if pod.DeletionTimestamp == nil {
				if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
					return nil, err
				}
			}
		}

		switch state {
		case stateIdle:
			counts.idle++
		case stateAssigned:
			counts.assigned++
		case stateRecycling:
			counts.recycling++
		case statePending:
			counts.pending++
		case stateTerminating:
			counts.terminating++
		}
		counts.total++
	}

	return counts, nil
}

func (r *SandboxWarmPoolReconciler) scaleUp(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool, counts *poolCounts) (bool, error) {
	need := pool.Spec.MinIdleCount - (counts.idle + counts.pending)
	if need < 0 {
		need = 0
	}
	room := pool.Spec.MaxPoolSize - counts.total
	if room < 0 {
		room = 0
	}
	toCreate := need
	if room < toCreate {
		toCreate = room
	}

	if toCreate == 0 {
		return false, nil
	}

	for i := int32(0); i < toCreate; i++ {
		pod := buildSandboxPod(pool)
		if err := r.Create(ctx, pod); err != nil {
			return i > 0, err
		}
		counts.pending++
		counts.total++
	}

	return true, nil
}

func (r *SandboxWarmPoolReconciler) expireIdlePods(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool, pods []corev1.Pod, counts *poolCounts) (bool, error) {
	if pool.Spec.IdleTimeoutSeconds == 0 {
		return false, nil
	}

	idleRemaining := counts.idle
	timeout := time.Duration(pool.Spec.IdleTimeoutSeconds) * time.Second
	expired := false

	for i := range pods {
		if idleRemaining <= pool.Spec.MinIdleCount {
			break
		}
		pod := &pods[i]
		if getPoolState(pod) != stateIdle {
			continue
		}
		idleSinceRaw := pod.Annotations[idleSinceAnnot]
		if idleSinceRaw == "" {
			continue
		}
		idleSince, err := time.Parse(time.RFC3339, idleSinceRaw)
		if err != nil {
			continue
		}
		if time.Since(idleSince) <= timeout {
			continue
		}
		if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
			return expired, err
		}
		expired = true
		idleRemaining--
		counts.idle--
		counts.total--
	}

	return expired, nil
}

func (r *SandboxWarmPoolReconciler) updateStatus(ctx context.Context, pool *sandboxv1alpha1.SandboxWarmPool, counts *poolCounts) error {
	base := pool.DeepCopy()

	pool.Status.IdleCount = counts.idle
	pool.Status.AssignedCount = counts.assigned
	pool.Status.RecyclingCount = counts.recycling
	pool.Status.PendingCount = counts.pending
	pool.Status.TotalCount = counts.total

	ready := counts.idle >= pool.Spec.MinIdleCount
	readyStatus := metav1.ConditionFalse
	readyReason := "MinIdleNotMet"
	readyMsg := "idle pods below minIdleCount"
	if ready {
		readyStatus = metav1.ConditionTrue
		readyReason = "MinIdleSatisfied"
		readyMsg = "idle pods at or above minIdleCount"
	}
	metav1.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:    conditionPoolReady,
		Status:  readyStatus,
		Reason:  readyReason,
		Message: readyMsg,
	})

	degradedStatus := metav1.ConditionFalse
	degradedReason := "NoDegradedPods"
	degradedMsg := "no degraded pods detected"
	if counts.degraded {
		degradedStatus = metav1.ConditionTrue
		degradedReason = "PodDegraded"
		degradedMsg = "one or more pods failing to become ready"
	}
	metav1.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:    conditionPoolDegraded,
		Status:  degradedStatus,
		Reason:  degradedReason,
		Message: degradedMsg,
	})

	atCapacity := counts.total >= pool.Spec.MaxPoolSize
	capacityStatus := metav1.ConditionFalse
	capacityReason := "BelowCapacity"
	capacityMsg := "pool below maxPoolSize"
	if atCapacity {
		capacityStatus = metav1.ConditionTrue
		capacityReason = "AtCapacity"
		capacityMsg = "pool at or above maxPoolSize"
	}
	metav1.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:    conditionPoolAtCapacity,
		Status:  capacityStatus,
		Reason:  capacityReason,
		Message: capacityMsg,
	})

	return r.Status().Patch(ctx, pool, client.MergeFrom(base))
}

func getPoolState(pod *corev1.Pod) string {
	if pod.Labels == nil {
		return ""
	}
	return pod.Labels[poolStateLabel]
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isPodDegraded(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting == nil {
			continue
		}
		reason := status.State.Waiting.Reason
		if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" {
			return true
		}
	}
	return false
}
