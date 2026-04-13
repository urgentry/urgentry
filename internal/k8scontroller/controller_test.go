package k8scontroller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCreatesManagedResources(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	inst := &UrgentryInstallation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "UrgentryInstallation",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
		Spec:       UrgentryInstallationSpec{Image: "ghcr.io/urgentry/urgentry:test"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(inst).WithObjects(inst).Build()
	reconciler := &InstallationReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: objectKey("default", "sample")}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, name := range []string{"urgentry-api", "urgentry-ingest", "urgentry-worker", "urgentry-scheduler"} {
		var deployment appsv1.Deployment
		if err := client.Get(context.Background(), objectKey("default", name), &deployment); err != nil {
			t.Fatalf("Get deployment %s: %v", name, err)
		}
		if got := deployment.Spec.Template.Spec.Containers[0].Image; got != "ghcr.io/urgentry/urgentry:test" {
			t.Fatalf("%s image = %q", name, got)
		}

		var service corev1.Service
		if err := client.Get(context.Background(), objectKey("default", name), &service); err != nil {
			t.Fatalf("Get service %s: %v", name, err)
		}
	}

	var pvc corev1.PersistentVolumeClaim
	if err := client.Get(context.Background(), objectKey("default", "urgentry-data"), &pvc); err != nil {
		t.Fatalf("Get pvc: %v", err)
	}
}

func TestReconcileUpdatesReadyStatus(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	inst := &UrgentryInstallation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "UrgentryInstallation",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
	}
	spec := defaults(inst.Spec)
	apiDeployment := desiredDeployment(inst, spec, managedRoles[0])
	apiDeployment.Status.ReadyReplicas = 1
	ingestDeployment := desiredDeployment(inst, spec, managedRoles[1])
	ingestDeployment.Status.ReadyReplicas = 1
	workerDeployment := desiredDeployment(inst, spec, managedRoles[2])
	workerDeployment.Status.ReadyReplicas = 1
	schedulerDeployment := desiredDeployment(inst, spec, managedRoles[3])
	schedulerDeployment.Status.ReadyReplicas = 1

	statusClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(inst).
		WithObjects(inst, apiDeployment, ingestDeployment, workerDeployment, schedulerDeployment).
		Build()
	reconciler := &InstallationReconciler{Client: statusClient, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: objectKey("default", "sample")}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var refreshed UrgentryInstallation
	if err := statusClient.Get(context.Background(), objectKey("default", "sample"), &refreshed); err != nil {
		t.Fatalf("Get installation: %v", err)
	}
	if refreshed.Status.ReadyRoles != 4 {
		t.Fatalf("ReadyRoles = %d, want 4", refreshed.Status.ReadyRoles)
	}
	if len(refreshed.Status.Conditions) == 0 || refreshed.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("Conditions = %+v, want Ready=True", refreshed.Status.Conditions)
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(client-go): %v", err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(controller): %v", err)
	}
	return scheme
}

func objectKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}
