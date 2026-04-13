package k8scontroller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func probe(portName, path string, initialDelay, period int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromString(portName),
			},
		},
		InitialDelaySeconds: initialDelay,
		PeriodSeconds:       period,
	}
}

func mustParseQuantity(value string) resource.Quantity {
	return resource.MustParse(value)
}

func intstrFromString(value string) intstr.IntOrString {
	return intstr.FromString(value)
}

func setStatusCondition(conditions *[]metav1.Condition, next metav1.Condition) {
	for idx := range *conditions {
		if (*conditions)[idx].Type == next.Type {
			(*conditions)[idx] = next
			return
		}
	}
	*conditions = append(*conditions, next)
}
