package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

func TestScaleUp_CreatesMinIdlePods(t *testing.T) {
	pool := newTestPool(uniqueName("pool-scale"), "default", 3, 10, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	reconcilePool(t, pool)

	pods := listPoolPods(t, pool)
	if len(pods) != 3 {
		t.Fatalf("expected 3 pods, got %d", len(pods))
	}
	for _, pod := range pods {
		if getPoolState(&pod) != statePending {
			t.Fatalf("expected pod state pending, got %q", getPoolState(&pod))
		}
	}
}

func TestPendingToIdle_OnPodReady(t *testing.T) {
	pool := newTestPool(uniqueName("pool-ready"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	pod := newTestPod(pool, uniqueName("pod-ready"), statePending, time.Time{})
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	reconcilePool(t, pool)

	updated := getPod(t, pod.Namespace, pod.Name)
	if getPoolState(updated) != stateIdle {
		t.Fatalf("expected pod state idle, got %q", getPoolState(updated))
	}
	if updated.Annotations[idleSinceAnnot] == "" {
		t.Fatalf("expected idle-since annotation to be set")
	}
}

func TestRespectsMaxPoolSize(t *testing.T) {
	pool := newTestPool(uniqueName("pool-max"), "default", 10, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	reconcilePool(t, pool)

	pods := listPoolPods(t, pool)
	if len(pods) != 5 {
		t.Fatalf("expected 5 pods, got %d", len(pods))
	}
}

func TestFailedPodDeleted(t *testing.T) {
	pool := newTestPool(uniqueName("pool-failed"), "default", 1, 3, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	pod := newTestPod(pool, uniqueName("pod-failed"), statePending, time.Time{})
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodFailed, false)

	reconcilePool(t, pool)

	waitForPodDeletion(t, pod.Namespace, pod.Name)
	waitForPodCount(t, pool, 1)
}

func TestIdleTimeout_ExpiresExcessPods(t *testing.T) {
	pool := newTestPool(uniqueName("pool-expire"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 1)
	createPool(t, pool)

	oldTime := time.Now().Add(-10 * time.Second)
	podA := newTestPod(pool, uniqueName("pod-idle-a"), stateIdle, oldTime)
	podB := newTestPod(pool, uniqueName("pod-idle-b"), stateIdle, oldTime)
	createPod(t, podA)
	createPod(t, podB)
	setPodStatus(t, podA, corev1.PodRunning, true)
	setPodStatus(t, podB, corev1.PodRunning, true)

	reconcilePool(t, pool)

	waitForPodCount(t, pool, 1)
}

func TestFinalizer_AddedOnCreate(t *testing.T) {
	pool := newTestPool(uniqueName("pool-finalizer"), "default", 1, 3, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	reconcilePool(t, pool)

	updated := getPool(t, pool.Namespace, pool.Name)
	if !containsString(updated.Finalizers, finalizerName) {
		t.Fatalf("expected finalizer to be added")
	}
}

func TestDeletion_CleansAllPods(t *testing.T) {
	pool := newTestPool(uniqueName("pool-delete"), "default", 1, 3, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	reconcilePool(t, pool)
	waitForPodCount(t, pool, 1)

	if err := k8sClient.Delete(testCtx, pool); err != nil {
		t.Fatalf("delete pool: %v", err)
	}

	reconcilePool(t, pool)
	waitForPodCount(t, pool, 0)

	if updated, err := tryGetPool(pool.Namespace, pool.Name); err == nil {
		if containsString(updated.Finalizers, finalizerName) {
			t.Fatalf("expected finalizer to be removed")
		}
	}
}

func TestStatusUpdated_Correctly(t *testing.T) {
	pool := newTestPool(uniqueName("pool-status"), "default", 1, 10, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	podIdle := newTestPod(pool, uniqueName("pod-idle"), stateIdle, time.Now())
	podAssigned := newTestPod(pool, uniqueName("pod-assigned"), stateAssigned, time.Time{})
	podPending := newTestPod(pool, uniqueName("pod-pending"), statePending, time.Time{})
	podRecycling := newTestPod(pool, uniqueName("pod-recycling"), stateRecycling, time.Time{})

	createPod(t, podIdle)
	createPod(t, podAssigned)
	createPod(t, podPending)
	createPod(t, podRecycling)
	setPodStatus(t, podIdle, corev1.PodRunning, true)
	setPodStatus(t, podAssigned, corev1.PodRunning, true)
	setPodStatus(t, podRecycling, corev1.PodRunning, true)

	reconcilePool(t, pool)

	updated := getPool(t, pool.Namespace, pool.Name)
	if updated.Status.IdleCount != 2 || updated.Status.AssignedCount != 1 || updated.Status.PendingCount != 1 || updated.Status.RecyclingCount != 0 {
		t.Fatalf("unexpected status counts: %+v", updated.Status)
	}
}

func TestRecyclePolicy_Delete(t *testing.T) {
	pool := newTestPool(uniqueName("pool-recycle-del"), "default", 1, 3, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	pod := newTestPod(pool, uniqueName("pod-assigned-del"), stateAssigned, time.Time{})
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	server := NewAssignmentServer(k8sClient, ":0")
	callUnassign(t, server, pod.Namespace, pod.Name)

	reconcilePool(t, pool)
	waitForPodCount(t, pool, 1)
}

func TestRecyclePolicy_Reuse(t *testing.T) {
	pool := newTestPool(uniqueName("pool-recycle-reuse"), "default", 1, 3, sandboxv1alpha1.RecyclePolicyReuse, 0)
	createPool(t, pool)

	pod := newTestPod(pool, uniqueName("pod-assigned-reuse"), stateAssigned, time.Time{})
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	server := NewAssignmentServer(k8sClient, ":0")
	callUnassign(t, server, pod.Namespace, pod.Name)

	reconcilePool(t, pool)

	updated := getPod(t, pod.Namespace, pod.Name)
	if getPoolState(updated) != stateIdle {
		t.Fatalf("expected pod state idle after reuse, got %q", getPoolState(updated))
	}
}

func newTestPool(name, namespace string, minIdle, maxPool int32, policy sandboxv1alpha1.RecyclePolicy, idleTimeout int64) *sandboxv1alpha1.SandboxWarmPool {
	return &sandboxv1alpha1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: sandboxv1alpha1.SandboxWarmPoolSpec{
			RuntimeClassName:   "gvisor",
			MinIdleCount:       minIdle,
			MaxPoolSize:        maxPool,
			RecyclePolicy:      policy,
			IdleTimeoutSeconds: idleTimeout,
			PodTemplate: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "sandbox",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "infinity"},
					}},
				},
			},
		},
	}
}

func newTestPod(pool *sandboxv1alpha1.SandboxWarmPool, name, state string, idleSince time.Time) *corev1.Pod {
	pod := buildSandboxPod(pool)
	pod.Name = name
	pod.GenerateName = ""
	pod.Namespace = pool.Namespace
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[poolStateLabel] = state
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if state == stateIdle && !idleSince.IsZero() {
		pod.Annotations[idleSinceAnnot] = idleSince.UTC().Format(time.RFC3339)
	}
	return pod
}

func createPool(t *testing.T, pool *sandboxv1alpha1.SandboxWarmPool) {
	t.Helper()
	if err := k8sClient.Create(testCtx, pool); err != nil {
		t.Fatalf("create pool: %v", err)
	}
}

func createPod(t *testing.T, pod *corev1.Pod) {
	t.Helper()
	if err := k8sClient.Create(testCtx, pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}
}

func getPod(t *testing.T, namespace, name string) *corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	if err := k8sClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	return &pod
}

func getPool(t *testing.T, namespace, name string) *sandboxv1alpha1.SandboxWarmPool {
	t.Helper()
	var pool sandboxv1alpha1.SandboxWarmPool
	if err := k8sClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &pool); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	return &pool
}

func tryGetPool(namespace, name string) (*sandboxv1alpha1.SandboxWarmPool, error) {
	var pool sandboxv1alpha1.SandboxWarmPool
	if err := k8sClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

func reconcilePool(t *testing.T, pool *sandboxv1alpha1.SandboxWarmPool) {
	t.Helper()
	_, err := reconciler.Reconcile(testCtx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func listPoolPods(t *testing.T, pool *sandboxv1alpha1.SandboxWarmPool) []corev1.Pod {
	t.Helper()
	var podList corev1.PodList
	if err := k8sClient.List(testCtx, &podList,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{
			poolNameLabel:      pool.Name,
			poolNamespaceLabel: pool.Namespace,
		},
	); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	return podList.Items
}

func setPodStatus(t *testing.T, pod *corev1.Pod, phase corev1.PodPhase, ready bool) {
	t.Helper()
	latest := getPod(t, pod.Namespace, pod.Name)
	latest.Status.Phase = phase
	if ready {
		latest.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	}
	if err := k8sClient.Status().Update(testCtx, latest); err != nil {
		t.Fatalf("update pod status: %v", err)
	}
}

func waitForPodCount(t *testing.T, pool *sandboxv1alpha1.SandboxWarmPool, expected int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testCtx, 5*time.Second)
	defer cancel()
	_ = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		pods := listPoolPods(t, pool)
		return len(pods) == expected, nil
	})
}

func waitForPodDeletion(t *testing.T, namespace, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testCtx, 5*time.Second)
	defer cancel()
	_ = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		var pod corev1.Pod
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &pod)
		return apierrors.IsNotFound(err), nil
	})
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
