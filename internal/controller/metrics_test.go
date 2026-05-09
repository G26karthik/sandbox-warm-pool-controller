package controller

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

func TestMetrics_PoolPodsGauge(t *testing.T) {
	pool := newTestPool(uniqueName("pool-metrics"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	counts := &poolCounts{idle: 2, pending: 1, assigned: 1, recycling: 0, terminating: 0}

	recordPoolCounts(pool, counts)

	idle := testutil.ToFloat64(poolPodsGauge.WithLabelValues(pool.Namespace, pool.Name, stateIdle))
	pending := testutil.ToFloat64(poolPodsGauge.WithLabelValues(pool.Namespace, pool.Name, statePending))
	assigned := testutil.ToFloat64(poolPodsGauge.WithLabelValues(pool.Namespace, pool.Name, stateAssigned))
	if idle != 2 || pending != 1 || assigned != 1 {
		t.Fatalf("unexpected pool pod gauge values: idle=%v pending=%v assigned=%v", idle, pending, assigned)
	}
}

func TestMetrics_PodsCreatedCounter(t *testing.T) {
	pool := newTestPool(uniqueName("pool-created"), "default", 1, 5, sandboxv1alpha1.RecyclePolicyDelete, 0)
	recordPodCreated(pool)
	recordPodCreated(pool)

	value := testutil.ToFloat64(podsCreatedTotal.WithLabelValues(pool.Namespace, pool.Name))
	if value != 2 {
		t.Fatalf("expected podsCreatedTotal to be 2, got %v", value)
	}
}

func TestMetrics_AssignmentDurationObserved(t *testing.T) {
	namespace := uniqueName("ns-metrics")
	poolName := uniqueName("pool-metrics")
	observeAssignmentDuration(namespace, poolName, "success", 50*time.Millisecond)

	count := getHistogramCount(t, "swpc_assignment_duration_seconds", map[string]string{
		"namespace": namespace,
		"pool_name": poolName,
		"result":    "success",
	})
	if count < 1 {
		t.Fatalf("expected histogram sample count >= 1, got %d", count)
	}
}

func getHistogramCount(t *testing.T, metricName string, labels map[string]string) uint64 {
	t.Helper()
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != metricName {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if labelsMatch(metric.GetLabel(), labels) {
				if metric.GetHistogram() == nil {
					return 0
				}
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func labelsMatch(actual []*dto.LabelPair, expected map[string]string) bool {
	for key, value := range expected {
		matched := false
		for _, label := range actual {
			if label.GetName() == key && label.GetValue() == value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
