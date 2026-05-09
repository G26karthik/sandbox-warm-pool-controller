package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

type AssignmentServer struct {
	client    client.Client
	addr      string
	poolLocks sync.Map
}

type assignRequest struct {
	Namespace string `json:"namespace"`
	PoolName  string `json:"poolName"`
	CallerID  string `json:"callerID"`
}

type assignResponse struct {
	PodName      string `json:"podName"`
	PodNamespace string `json:"podNamespace"`
	PodIP        string `json:"podIP"`
	NodeName     string `json:"nodeName"`
	AssignedAt   string `json:"assignedAt"`
}

type assignErrorResponse struct {
	Error        string `json:"error"`
	IdleCount    int32  `json:"idleCount"`
	PendingCount int32  `json:"pendingCount"`
}

type unassignRequest struct {
	PodName      string `json:"podName"`
	PodNamespace string `json:"podNamespace"`
}

type unassignResponse struct {
	Status        string `json:"status"`
	RecyclePolicy string `json:"recyclePolicy"`
}

type statusResponse struct {
	IdleCount      int32 `json:"idleCount"`
	AssignedCount  int32 `json:"assignedCount"`
	PendingCount   int32 `json:"pendingCount"`
	RecyclingCount int32 `json:"recyclingCount"`
	TotalCount     int32 `json:"totalCount"`
	PoolReady      bool  `json:"poolReady"`
}

type healthResponse struct {
	Status string `json:"status"`
}

func NewAssignmentServer(k8sClient client.Client, addr string) *AssignmentServer {
	return &AssignmentServer{client: k8sClient, addr: addr}
}

func (s *AssignmentServer) Start() {
	logger := log.Log.WithName("assignment-server")
	mux := http.NewServeMux()
	mux.HandleFunc("/assign", s.handleAssign)
	mux.HandleFunc("/unassign", s.handleUnassign)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/healthz", s.handleHealthz)

	server := &http.Server{Addr: s.addr, Handler: mux}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(err, "assignment server stopped")
	}
}

func (s *AssignmentServer) handleAssign(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, assignErrorResponse{Error: "method not allowed"})
		return
	}
	var req assignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "invalid json"})
		return
	}
	if req.Namespace == "" || req.PoolName == "" || req.CallerID == "" {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "namespace, poolName, and callerID are required"})
		return
	}

	lock := s.getPoolLock(req.Namespace + "/" + req.PoolName)
	lock.Lock()
	defer lock.Unlock()

	ctx := r.Context()
	idlePods, pendingCount, err := s.listPoolPods(ctx, req.Namespace, req.PoolName)
	if err != nil {
		observeAssignmentDuration(req.Namespace, req.PoolName, "error", time.Since(start))
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to list pods"})
		return
	}
	if len(idlePods) == 0 {
		observeAssignmentDuration(req.Namespace, req.PoolName, "no_pods", time.Since(start))
		writeJSON(w, http.StatusServiceUnavailable, assignErrorResponse{Error: "no idle pods available", IdleCount: 0, PendingCount: pendingCount})
		return
	}

	selected := pickOldestIdlePod(idlePods)
	if selected == nil {
		observeAssignmentDuration(req.Namespace, req.PoolName, "no_pods", time.Since(start))
		writeJSON(w, http.StatusServiceUnavailable, assignErrorResponse{Error: "no idle pods available", IdleCount: 0, PendingCount: pendingCount})
		return
	}

	assignedAt := time.Now().UTC().Format(time.RFC3339)
	patch := client.MergeFrom(selected.DeepCopy())
	if selected.Labels == nil {
		selected.Labels = map[string]string{}
	}
	if selected.Annotations == nil {
		selected.Annotations = map[string]string{}
	}
	selected.Labels[poolStateLabel] = stateAssigned
	selected.Annotations[assignedToAnnot] = req.CallerID
	selected.Annotations[assignedAtAnnot] = assignedAt

	if err := s.client.Patch(ctx, selected, patch); err != nil {
		if apierrors.IsConflict(err) {
			observeAssignmentDuration(req.Namespace, req.PoolName, "conflict", time.Since(start))
			writeJSON(w, http.StatusConflict, assignErrorResponse{Error: "assignment conflict"})
			return
		}
		observeAssignmentDuration(req.Namespace, req.PoolName, "error", time.Since(start))
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to assign pod"})
		return
	}

	observeAssignmentDuration(req.Namespace, req.PoolName, "success", time.Since(start))
	writeJSON(w, http.StatusOK, assignResponse{
		PodName:      selected.Name,
		PodNamespace: selected.Namespace,
		PodIP:        selected.Status.PodIP,
		NodeName:     selected.Spec.NodeName,
		AssignedAt:   assignedAt,
	})
}

func (s *AssignmentServer) handleUnassign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, assignErrorResponse{Error: "method not allowed"})
		return
	}
	var req unassignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "invalid json"})
		return
	}
	if req.PodName == "" || req.PodNamespace == "" {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "podName and podNamespace are required"})
		return
	}

	ctx := r.Context()
	var pod corev1.Pod
	if err := s.client.Get(ctx, types.NamespacedName{Name: req.PodName, Namespace: req.PodNamespace}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, assignErrorResponse{Error: "pod not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to fetch pod"})
		return
	}

	if getPoolState(&pod) != stateAssigned {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "pod is not in assigned state"})
		return
	}

	poolName := ""
	if pod.Labels != nil {
		poolName = pod.Labels[poolNameLabel]
	}
	if poolName == "" {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "pod missing pool-name label"})
		return
	}

	var pool sandboxv1alpha1.SandboxWarmPool
	if err := s.client.Get(ctx, types.NamespacedName{Name: poolName, Namespace: req.PodNamespace}, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, assignErrorResponse{Error: "pool not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to fetch pool"})
		return
	}

	patch := client.MergeFrom(pod.DeepCopy())
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	status := "recycling"
	if pool.Spec.RecyclePolicy == sandboxv1alpha1.RecyclePolicyDelete {
		status = "terminating"
		pod.Labels[poolStateLabel] = stateTerminating
	} else {
		pod.Labels[poolStateLabel] = stateRecycling
	}
	pod.Annotations[assignedToAnnot] = ""
	pod.Annotations[assignedAtAnnot] = ""

	if err := s.client.Patch(ctx, &pod, patch); err != nil {
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to update pod"})
		return
	}

	if pool.Spec.RecyclePolicy == sandboxv1alpha1.RecyclePolicyDelete {
		if err := s.client.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to delete pod"})
			return
		}
	}

	recordPodRecycled(&pool, pool.Spec.RecyclePolicy)
	writeJSON(w, http.StatusOK, unassignResponse{Status: status, RecyclePolicy: string(pool.Spec.RecyclePolicy)})
}

func (s *AssignmentServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	poolName := r.URL.Query().Get("poolName")
	if namespace == "" || poolName == "" {
		writeJSON(w, http.StatusBadRequest, assignErrorResponse{Error: "namespace and poolName are required"})
		return
	}

	ctx := r.Context()
	var pool sandboxv1alpha1.SandboxWarmPool
	if err := s.client.Get(ctx, types.NamespacedName{Name: poolName, Namespace: namespace}, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, assignErrorResponse{Error: "pool not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, assignErrorResponse{Error: "failed to fetch pool"})
		return
	}

	poolReady := false
	for _, cond := range pool.Status.Conditions {
		if cond.Type == conditionPoolReady && cond.Status == metav1.ConditionTrue {
			poolReady = true
			break
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{
		IdleCount:      pool.Status.IdleCount,
		AssignedCount:  pool.Status.AssignedCount,
		PendingCount:   pool.Status.PendingCount,
		RecyclingCount: pool.Status.RecyclingCount,
		TotalCount:     pool.Status.TotalCount,
		PoolReady:      poolReady,
	})
}

func (s *AssignmentServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *AssignmentServer) listPoolPods(ctx context.Context, namespace, poolName string) ([]*corev1.Pod, int32, error) {
	var podList corev1.PodList
	if err := s.client.List(ctx, &podList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			poolNameLabel:      poolName,
			poolNamespaceLabel: namespace,
		},
	); err != nil {
		return nil, 0, err
	}

	idlePods := make([]*corev1.Pod, 0)
	var pendingCount int32
	for i := range podList.Items {
		state := getPoolState(&podList.Items[i])
		switch state {
		case stateIdle:
			idlePods = append(idlePods, &podList.Items[i])
		case statePending:
			pendingCount++
		}
	}
	return idlePods, pendingCount, nil
}

func (s *AssignmentServer) getPoolLock(key string) *sync.Mutex {
	lock, _ := s.poolLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func pickOldestIdlePod(pods []*corev1.Pod) *corev1.Pod {
	if len(pods) == 0 {
		return nil
	}
	sort.Slice(pods, func(i, j int) bool {
		return idleSince(pods[i]).Before(idleSince(pods[j]))
	})
	return pods[0]
}

func idleSince(pod *corev1.Pod) time.Time {
	if pod.Annotations == nil {
		return time.Now().UTC()
	}
	value := pod.Annotations[idleSinceAnnot]
	if value == "" {
		return time.Now().UTC()
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().UTC()
	}
	return parsed
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
