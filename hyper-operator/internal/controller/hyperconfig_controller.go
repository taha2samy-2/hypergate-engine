package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hyperv1alpha1 "github.com/taha/myprog/hyper-operator/api/v1alpha1"
)

// HyperConfigReconciler reconciles a HyperConfig object
type HyperConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *HyperConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var hyperConfig hyperv1alpha1.HyperConfig
	if err := r.Get(ctx, req.NamespacedName, &hyperConfig); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch HyperConfig")
		return ctrl.Result{}, err
	}

	targetNS := hyperConfig.Spec.TargetNamespace
	if targetNS == "" {
		targetNS = "hyper-system"
	}

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: targetNS}, ns); err != nil {
		if errors.IsNotFound(err) {
			ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: targetNS}}
			if err := r.Create(ctx, ns); err != nil {
				logger.Error(err, "failed to create target namespace", "namespace", targetNS)
				return ctrl.Result{}, err
			}
			logger.Info("Created target namespace", "namespace", targetNS)
		} else {
			logger.Error(err, "failed to fetch target namespace", "namespace", targetNS)
			return ctrl.Result{}, err
		}
	}

	engineImage := hyperConfig.Spec.EngineImage
	if engineImage == "" {
		engineImage = "taha/myprog-engine:latest"
	}

	namespace := targetNS

	saName := "hyper-engine-sa"
	roleName := "hyper-engine-config-reader"
	dsName := "hyper-engine"
	svcName := "hyper-engine-svc"

	// 1. ServiceAccount
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return ctrl.SetControllerReference(&hyperConfig, sa, r.Scheme)
	}); err != nil {
		logger.Error(err, "failed to reconcile ServiceAccount")
		return ctrl.Result{}, err
	}

	// 2. Role
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{"hyper-engine-config"},
				Verbs:         []string{"get", "watch"},
			},
		}
		return ctrl.SetControllerReference(&hyperConfig, role, r.Scheme)
	}); err != nil {
		logger.Error(err, "failed to reconcile Role")
		return ctrl.Result{}, err
	}

	// 3. RoleBinding
	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleName + "-binding", Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, roleBinding, func() error {
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		}
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: namespace,
			},
		}
		return ctrl.SetControllerReference(&hyperConfig, roleBinding, r.Scheme)
	}); err != nil {
		logger.Error(err, "failed to reconcile RoleBinding")
		return ctrl.Result{}, err
	}

	// 4. DaemonSet
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: dsName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, ds, func() error {
		labels := map[string]string{"app": "hyper-engine"}
		if ds.Spec.Selector == nil {
			ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		ds.Spec.Template.ObjectMeta.Labels = labels
		ds.Spec.Template.Spec.ServiceAccountName = saName
		
		container := corev1.Container{
			Name:  "engine",
			Image: engineImage,
			Ports: []corev1.ContainerPort{
				{
					ContainerPort: 9001,
					Name:          "grpc",
				},
			},
			Env: []corev1.EnvVar{
				{
					Name:  "CONFIG_MODE",
					Value: "k8s",
				},
				{
					Name:  "CONFIG_MAP_NAME",
					Value: "hyper-engine-config",
				},
			},
		}
		
		ds.Spec.Template.Spec.Containers = []corev1.Container{container}
		return ctrl.SetControllerReference(&hyperConfig, ds, r.Scheme)
	}); err != nil {
		logger.Error(err, "failed to reconcile DaemonSet")
		return ctrl.Result{}, err
	}

	// 5. Service
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Selector = map[string]string{"app": "hyper-engine"}
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "grpc",
				Port:       9001,
				TargetPort: intstr.FromInt(9001),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		return ctrl.SetControllerReference(&hyperConfig, svc, r.Scheme)
	}); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HyperConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1alpha1.HyperConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}
