package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

func TestAssign_Success(t *testing.T) {
	pool := newTestPool(uniqueName("pool-assign"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	pod := newTestPod(pool, uniqueName("pod-assign"), stateIdle, time.Now())
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	server := NewAssignmentServer(k8sClient, ":0")
	resp := callAssign(t, server, pool.Namespace, pool.Name, "caller-1")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	updated := getPod(t, pod.Namespace, pod.Name)
	if getPoolState(updated) != stateAssigned {
		t.Fatalf("expected pod state assigned, got %q", getPoolState(updated))
	}
}

func TestAssign_NoIdlePods(t *testing.T) {
	pool := newTestPool(uniqueName("pool-noidle"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	pod := newTestPod(pool, uniqueName("pod-pending"), statePending, time.Time{})
	createPod(t, pod)

	server := NewAssignmentServer(k8sClient, ":0")
	resp := callAssign(t, server, pool.Namespace, pool.Name, "caller-2")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Code)
	}
}

func TestAssign_ConcurrentRequests(t *testing.T) {
	pool := newTestPool(uniqueName("pool-concurrent"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)
	pod := newTestPod(pool, uniqueName("pod-concurrent"), stateIdle, time.Now())
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	server := NewAssignmentServer(k8sClient, ":0")

	var successCount int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			callerID := fmt.Sprintf("caller-%d", i)
			resp := callAssign(t, server, pool.Namespace, pool.Name, callerID)
			if resp.Code == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected 1 successful assignment, got %d", successCount)
	}
}

func TestUnassign_Success(t *testing.T) {
	pool := newTestPool(uniqueName("pool-unassign"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyReuse, 0)
	createPool(t, pool)
	pod := newTestPod(pool, uniqueName("pod-assigned"), stateAssigned, time.Time{})
	createPod(t, pod)
	setPodStatus(t, pod, corev1.PodRunning, true)

	server := NewAssignmentServer(k8sClient, ":0")
	resp := callUnassign(t, server, pod.Namespace, pod.Name)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	updated := getPod(t, pod.Namespace, pod.Name)
	if getPoolState(updated) != stateRecycling {
		t.Fatalf("expected pod state recycling, got %q", getPoolState(updated))
	}
}

func TestStatus_ReturnsCorrectCounts(t *testing.T) {
	pool := newTestPool(uniqueName("pool-status-api"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	createPool(t, pool)

	pool.Status.IdleCount = 2
	pool.Status.AssignedCount = 1
	pool.Status.PendingCount = 0
	pool.Status.RecyclingCount = 0
	pool.Status.TotalCount = 3
	pool.Status.Conditions = []metav1.Condition{{Type: conditionPoolReady, Status: metav1.ConditionTrue}}
	if err := k8sClient.Status().Update(testCtx, pool); err != nil {
		t.Fatalf("update pool status: %v", err)
	}

	server := NewAssignmentServer(k8sClient, ":0")
	req := httptest.NewRequest(http.MethodGet, "/status?namespace="+pool.Namespace+"&poolName="+pool.Name, nil)
	rec := httptest.NewRecorder()
	server.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	server := NewAssignmentServer(k8sClient, ":0")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.handleHealthz(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func callAssign(t *testing.T, server *AssignmentServer, namespace, poolName, callerID string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(assignRequest{Namespace: namespace, PoolName: poolName, CallerID: callerID})
	req := httptest.NewRequest(http.MethodPost, "/assign", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	server.handleAssign(rec, req)
	return rec
}

func callUnassign(t *testing.T, server *AssignmentServer, namespace, podName string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(unassignRequest{PodName: podName, PodNamespace: namespace})
	req := httptest.NewRequest(http.MethodPost, "/unassign", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	server.handleUnassign(rec, req)
	return rec
}
