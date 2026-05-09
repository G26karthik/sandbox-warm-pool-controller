package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=swp
// +kubebuilder:printcolumn:name="RuntimeClass",type=string,JSONPath=`.spec.runtimeClassName`
// +kubebuilder:printcolumn:name="MinIdle",type=integer,JSONPath=`.spec.minIdleCount`
// +kubebuilder:printcolumn:name="Idle",type=integer,JSONPath=`.status.idleCount`
// +kubebuilder:printcolumn:name="Assigned",type=integer,JSONPath=`.status.assignedCount`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="PoolReady")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type SandboxWarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxWarmPoolSpec   `json:"spec"`
	Status SandboxWarmPoolStatus `json:"status,omitempty"`
}

type SandboxWarmPoolSpec struct {
	// RuntimeClassName references a Kubernetes RuntimeClass configured for
	// gVisor (handler: runsc) or Kata Containers (handler: kata-qemu / kata-clh).
	// +kubebuilder:validation:Required
	RuntimeClassName string `json:"runtimeClassName"`

	// MinIdleCount is the target number of warm idle pods to maintain.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=2
	MinIdleCount int32 `json:"minIdleCount"`

	// MaxPoolSize is the hard cap on total pods across all states.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=10
	MaxPoolSize int32 `json:"maxPoolSize"`

	// PodTemplate defines the spec for each sandbox pod.
	// RuntimeClassName is injected automatically; do not set it here.
	// +kubebuilder:validation:Required
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate"`

	// RecyclePolicy controls what happens when a pod is un-assigned.
	// Delete: terminate the pod and create a fresh one.
	// Reuse: attempt an in-place reset, return to Idle if successful.
	// +kubebuilder:validation:Enum=Delete;Reuse
	// +kubebuilder:default=Delete
	RecyclePolicy RecyclePolicy `json:"recyclePolicy,omitempty"`

	// IdleTimeoutSeconds: idle pods older than this are garbage-collected
	// when idleCount > minIdleCount. 0 = never expire.
	// +kubebuilder:default=300
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`
}

// RecyclePolicy determines pod fate after un-assignment.
// +kubebuilder:validation:Enum=Delete;Reuse
type RecyclePolicy string

const (
	RecyclePolicyDelete RecyclePolicy = "Delete"
	RecyclePolicyReuse  RecyclePolicy = "Reuse"
)

type SandboxWarmPoolStatus struct {
	// IdleCount is the number of pods in Idle state.
	IdleCount int32 `json:"idleCount"`
	// AssignedCount is the number of pods currently claimed by callers.
	AssignedCount int32 `json:"assignedCount"`
	// RecyclingCount is the number of pods being recycled.
	RecyclingCount int32 `json:"recyclingCount"`
	// PendingCount is the number of pods still initializing.
	PendingCount int32 `json:"pendingCount"`
	// TotalCount is the sum across all states.
	TotalCount int32 `json:"totalCount"`

	// Conditions reflects operational state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastScaleTime is when the controller last created or deleted a pod.
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`
}

// +kubebuilder:object:root=true
type SandboxWarmPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxWarmPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SandboxWarmPool{}, &SandboxWarmPoolList{})
}
