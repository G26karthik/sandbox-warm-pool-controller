package controller

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

var (
	poolPodsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "swpc_pool_pods",
			Help: "Pod count per state",
		},
		[]string{"namespace", "pool_name", "state"},
	)
	podsCreatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swpc_pods_created_total",
			Help: "Total pods created by controller",
		},
		[]string{"namespace", "pool_name"},
	)
	podsExpiredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swpc_pods_expired_total",
			Help: "Pods deleted due to idle timeout",
		},
		[]string{"namespace", "pool_name"},
	)
	podsRecycledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "swpc_pods_recycled_total",
			Help: "Pods through recycle pipeline",
		},
		[]string{"namespace", "pool_name", "recycle_policy"},
	)
	assignmentDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "swpc_assignment_duration_seconds",
			Help:    "Assign latency by result",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "pool_name", "result"},
	)
	warmupDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "swpc_pod_warmup_duration_seconds",
			Help:    "Time from pending to idle",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "pool_name", "runtime_class"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		poolPodsGauge,
		podsCreatedTotal,
		podsExpiredTotal,
		podsRecycledTotal,
		assignmentDuration,
		warmupDuration,
	)
}

func recordPoolCounts(pool *sandboxv1alpha1.SandboxWarmPool, counts *poolCounts) {
	ns := pool.Namespace
	name := pool.Name
	poolPodsGauge.WithLabelValues(ns, name, statePending).Set(float64(counts.pending))
	poolPodsGauge.WithLabelValues(ns, name, stateIdle).Set(float64(counts.idle))
	poolPodsGauge.WithLabelValues(ns, name, stateAssigned).Set(float64(counts.assigned))
	poolPodsGauge.WithLabelValues(ns, name, stateRecycling).Set(float64(counts.recycling))
	poolPodsGauge.WithLabelValues(ns, name, stateTerminating).Set(float64(counts.terminating))
}

func recordPodCreated(pool *sandboxv1alpha1.SandboxWarmPool) {
	podsCreatedTotal.WithLabelValues(pool.Namespace, pool.Name).Inc()
}

func recordPodExpired(pool *sandboxv1alpha1.SandboxWarmPool) {
	podsExpiredTotal.WithLabelValues(pool.Namespace, pool.Name).Inc()
}

func recordPodRecycled(pool *sandboxv1alpha1.SandboxWarmPool, policy sandboxv1alpha1.RecyclePolicy) {
	podsRecycledTotal.WithLabelValues(pool.Namespace, pool.Name, string(policy)).Inc()
}

func observeAssignmentDuration(namespace, poolName, result string, duration time.Duration) {
	assignmentDuration.WithLabelValues(namespace, poolName, result).Observe(duration.Seconds())
}

func observeWarmupDuration(namespace, poolName, runtimeClass string, duration time.Duration) {
	labelClass := runtimeClass
	if labelClass == "" {
		labelClass = "unknown"
	}
	warmupDuration.WithLabelValues(namespace, poolName, labelClass).Observe(duration.Seconds())
}
