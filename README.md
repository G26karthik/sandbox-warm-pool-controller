# Sandbox Warm Pool Controller

A standalone Kubernetes controller that mirrors Koordinator's sandbox pre-warming mechanism to reduce gVisor/Kata cold-start latency.

## Why this project

Koordinator's sandbox scheduling proposal calls out pre-warming for hardware-isolated runtimes (gVisor, Kata Containers) to avoid 1-5 second cold starts. This project recreates that exact mechanism in a minimal, standalone controller using the same stack: Go, controller-runtime, CRDs, RuntimeClass, and sandbox pod lifecycle management.

## Architecture

```
SandboxWarmPool CR
        |
        v
Warm Pool Controller -----> Pod Pool (Pending/Idle/Assigned/...) -----> Assignment Server -----> Caller
```

## Quick Start

```bash
make install                          # install CRD into cluster
make deploy                           # deploy controller
kubectl apply -f examples/gvisor-pool.yaml
curl -X POST http://localhost:8081/assign \
  -d '{"namespace":"default","poolName":"gvisor-pool","callerID":"test"}'
```

## State Machine

```
                    ┌─────────────────────────────────────┐
                    │           CREATE POD                │
                    └──────────────┬──────────────────────┘
                                   │
                                   ▼
                               Pending
                              /        \
                 Pod Running+Ready    Pod Failed
                        │                  │
                        ▼                  ▼
                        Idle         Terminating ──► (deleted; scaleUp creates replacement)
                          │
                  /assign HTTP call
                          │
                          ▼
                       Assigned
                          │
                  /unassign HTTP call
                          │
              ┌───────────┴───────────┐
         policy=Delete           policy=Reuse
              │                       │
              ▼                       ▼
        Terminating              Recycling
              │                       │
     (deleted; scaleUp         reset OK?
      creates replacement)    /         \
                            Yes          No
                             │            │
                             ▼            ▼
                            Idle     Terminating
```

## Metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `swpc_pool_pods` | Gauge | `namespace`, `pool_name`, `state` | Pod count per state |
| `swpc_pods_created_total` | Counter | `namespace`, `pool_name` | Total pods created by controller |
| `swpc_pods_expired_total` | Counter | `namespace`, `pool_name` | Pods deleted due to idle timeout |
| `swpc_pods_recycled_total` | Counter | `namespace`, `pool_name`, `recycle_policy` | Pods through recycle pipeline |
| `swpc_assignment_duration_seconds` | Histogram | `namespace`, `pool_name`, `result` | `/assign` latency |
| `swpc_pod_warmup_duration_seconds` | Histogram | `namespace`, `pool_name`, `runtime_class` | Time from Pending to Idle |
