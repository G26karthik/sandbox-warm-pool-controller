package controller

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

func buildSandboxPod(pool *sandboxv1alpha1.SandboxWarmPool) *corev1.Pod {
	template := pool.Spec.PodTemplate.DeepCopy()
	pod := &corev1.Pod{
		ObjectMeta: template.ObjectMeta,
		Spec:       template.Spec,
	}

	pod.GenerateName = "swp-" + pool.Name + "-"
	pod.Name = ""
	pod.Namespace = pool.Namespace

	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[poolNameLabel] = pool.Name
	pod.Labels[poolNamespaceLabel] = pool.Namespace
	pod.Labels[poolStateLabel] = statePending

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[createdAtAnnot] = time.Now().UTC().Format(time.RFC3339)
	pod.Annotations[idleSinceAnnot] = ""
	pod.Annotations[assignedToAnnot] = ""
	pod.Annotations[assignedAtAnnot] = ""

	pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(
		pool,
		sandboxv1alpha1.GroupVersion.WithKind("SandboxWarmPool"),
	)}

	pod.Spec.RuntimeClassName = &pool.Spec.RuntimeClassName
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever

	if len(pod.Spec.Containers) > 0 && pod.Spec.Containers[0].ReadinessProbe == nil {
		pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{Command: []string{"true"}},
			},
			InitialDelaySeconds: 2,
			PeriodSeconds:       2,
		}
	}

	return pod
}
