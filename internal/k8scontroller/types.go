package k8scontroller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "selfhosted.urgentry.dev", Version: "v1alpha1"}

type UrgentryInstallationSpec struct {
	Image         string               `json:"image"`
	ConfigMapName string               `json:"configMapName,omitempty"`
	SecretName    string               `json:"secretName,omitempty"`
	DataPVCName   string               `json:"dataPVCName,omitempty"`
	DataPVCSize   string               `json:"dataPVCSize,omitempty"`
	Replicas      UrgentryRoleReplicas `json:"replicas,omitempty"`
}

type UrgentryRoleReplicas struct {
	API       int32 `json:"api,omitempty"`
	Ingest    int32 `json:"ingest,omitempty"`
	Worker    int32 `json:"worker,omitempty"`
	Scheduler int32 `json:"scheduler,omitempty"`
}

type UrgentryInstallationStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	ReadyRoles         int32              `json:"readyRoles,omitempty"`
	ManagedResources   []string           `json:"managedResources,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type UrgentryInstallation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UrgentryInstallationSpec   `json:"spec,omitempty"`
	Status UrgentryInstallationStatus `json:"status,omitempty"`
}

type UrgentryInstallationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UrgentryInstallation `json:"items"`
}

func (in *UrgentryInstallation) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := *in
	out.ObjectMeta = *in.DeepCopy()
	out.Status.ManagedResources = append([]string(nil), in.Status.ManagedResources...)
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	return &out
}

func (in *UrgentryInstallationList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Items) > 0 {
		out.Items = make([]UrgentryInstallation, len(in.Items))
		for idx := range in.Items {
			itemCopy := in.Items[idx]
			itemCopy.ObjectMeta = *in.Items[idx].DeepCopy()
			itemCopy.Status.ManagedResources = append([]string(nil), in.Items[idx].Status.ManagedResources...)
			itemCopy.Status.Conditions = append([]metav1.Condition(nil), in.Items[idx].Status.Conditions...)
			out.Items[idx] = itemCopy
		}
	}
	return &out
}

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &UrgentryInstallation{}, &UrgentryInstallationList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
