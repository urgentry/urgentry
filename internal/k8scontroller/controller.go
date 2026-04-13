package k8scontroller

import (
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type InstallationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type roleSpec struct {
	Name string
	Port int32
	Addr string
}

var managedRoles = []roleSpec{
	{Name: "api", Port: 8080, Addr: ":8080"},
	{Name: "ingest", Port: 8081, Addr: ":8081"},
	{Name: "worker", Port: 8082, Addr: ":8082"},
	{Name: "scheduler", Port: 8083, Addr: ":8083"},
}

func (r *InstallationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inst UrgentryInstallation
	if err := r.Get(ctx, req.NamespacedName, &inst); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	spec := defaults(inst.Spec)
	managed := make([]string, 0, 1+len(managedRoles)*2)

	pvc := desiredPVC(&inst, spec)
	if err := r.applyOwned(ctx, &inst, pvc); err != nil {
		return ctrl.Result{}, err
	}
	managed = append(managed, resourceName("PersistentVolumeClaim", pvc.Name))

	readyRoles := int32(0)
	for _, role := range managedRoles {
		service := desiredService(&inst, role)
		if err := r.applyOwned(ctx, &inst, service); err != nil {
			return ctrl.Result{}, err
		}
		managed = append(managed, resourceName("Service", service.Name))

		deployment := desiredDeployment(&inst, spec, role)
		if err := r.applyOwned(ctx, &inst, deployment); err != nil {
			return ctrl.Result{}, err
		}
		managed = append(managed, resourceName("Deployment", deployment.Name))

		var current appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, &current); err == nil && deploymentReady(&current) {
			readyRoles++
		}
	}

	sort.Strings(managed)
	current := inst.DeepCopyObject().(*UrgentryInstallation)
	current.Status.ObservedGeneration = inst.Generation
	current.Status.ReadyRoles = readyRoles
	current.Status.ManagedResources = managed

	condition := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: inst.Generation,
		Status:             metav1.ConditionFalse,
		Reason:             "Reconciling",
		Message:            fmt.Sprintf("%d/%d managed roles ready", readyRoles, len(managedRoles)),
	}
	if readyRoles == int32(len(managedRoles)) {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Ready"
	}
	setStatusCondition(&current.Status.Conditions, condition)
	if err := r.Status().Update(ctx, current); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *InstallationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&UrgentryInstallation{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func (r *InstallationReconciler) applyOwned(ctx context.Context, owner *UrgentryInstallation, obj client.Object) error {
	if err := controllerutil.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return err
	}
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	current := obj.DeepCopyObject().(client.Object)
	if err := r.Get(ctx, key, current); err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, obj)
		}
		return err
	}
	obj.SetResourceVersion(current.GetResourceVersion())
	return r.Update(ctx, obj)
}

func defaults(spec UrgentryInstallationSpec) UrgentryInstallationSpec {
	if spec.Image == "" {
		spec.Image = "urgentry:latest"
	}
	if spec.ConfigMapName == "" {
		spec.ConfigMapName = "urgentry-config"
	}
	if spec.SecretName == "" {
		spec.SecretName = "urgentry-secret"
	}
	if spec.DataPVCName == "" {
		spec.DataPVCName = "urgentry-data"
	}
	if spec.DataPVCSize == "" {
		spec.DataPVCSize = "5Gi"
	}
	if spec.Replicas.API <= 0 {
		spec.Replicas.API = 1
	}
	if spec.Replicas.Ingest <= 0 {
		spec.Replicas.Ingest = 1
	}
	if spec.Replicas.Worker <= 0 {
		spec.Replicas.Worker = 1
	}
	if spec.Replicas.Scheduler <= 0 {
		spec.Replicas.Scheduler = 1
	}
	return spec
}

func desiredPVC(owner *UrgentryInstallation, spec UrgentryInstallationSpec) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.DataPVCName,
			Namespace: owner.Namespace,
			Labels:    baseLabels("storage"),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(spec.DataPVCSize),
				},
			},
		},
	}
}

func desiredService(owner *UrgentryInstallation, role roleSpec) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "urgentry-" + role.Name,
			Namespace: owner.Namespace,
			Labels:    baseLabels(role.Name),
		},
		Spec: corev1.ServiceSpec{
			Selector: baseLabels(role.Name),
			Ports: []corev1.ServicePort{{
				Name:       role.Name,
				Port:       role.Port,
				TargetPort: intstrFromString(role.Name),
			}},
		},
	}
}

func desiredDeployment(owner *UrgentryInstallation, spec UrgentryInstallationSpec, role roleSpec) *appsv1.Deployment {
	replicas := spec.Replicas.API
	switch role.Name {
	case "ingest":
		replicas = spec.Replicas.Ingest
	case "worker":
		replicas = spec.Replicas.Worker
	case "scheduler":
		replicas = spec.Replicas.Scheduler
	}
	labels := baseLabels(role.Name)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "urgentry-" + role.Name,
			Namespace: owner.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            role.Name,
						Image:           spec.Image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Args:            []string{"serve", "--role=" + role.Name, "--addr=" + role.Addr},
						Ports: []corev1.ContainerPort{{
							ContainerPort: role.Port,
							Name:          role.Name,
						}},
						EnvFrom: []corev1.EnvFromSource{
							{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: spec.ConfigMapName}}},
							{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: spec.SecretName}}},
						},
						Env: []corev1.EnvVar{{
							Name:  "URGENTRY_DATA_DIR",
							Value: "/data",
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "urgentry-data",
							MountPath: "/data",
						}},
						ReadinessProbe: probe(role.Name, "/readyz", 5, 5),
						LivenessProbe:  probe(role.Name, "/healthz", 15, 10),
					}},
					Volumes: []corev1.Volume{{
						Name: "urgentry-data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: spec.DataPVCName},
						},
					}},
				},
			},
		},
	}
}

func deploymentReady(item *appsv1.Deployment) bool {
	if item.Spec.Replicas == nil {
		return false
	}
	return item.Status.ReadyReplicas >= *item.Spec.Replicas
}

func baseLabels(role string) map[string]string {
	return map[string]string{
		"app":  "urgentry",
		"role": role,
	}
}

func resourceName(kind, name string) string {
	return kind + "/" + name
}
