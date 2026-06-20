package controller

import (
	"context"
	"encoding/json"
	"sort"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperv1alpha1 "github.com/taha/myprog/hyper-operator/api/v1alpha1"
	"github.com/taha/myprog/internal/config"
	mylogger "github.com/taha/myprog/internal/logger"
)

// HyperRouteReconciler reconciles a HyperRoute object
type HyperRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=infra.hyper.io,resources=hyperredis,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *HyperRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx)

	// Fetch all instances of HyperConfig
	var configList hyperv1alpha1.HyperConfigList
	if err := r.List(ctx, &configList); err != nil {
		reqLogger.Error(err, "unable to list HyperConfigs")
		return ctrl.Result{}, err
	}

	// Abort check: need at least one config
	if len(configList.Items) == 0 {
		reqLogger.Info("No HyperConfig exists, skipping compilation")
		return ctrl.Result{}, nil
	}
	
	activeConfig := configList.Items[0]

	// Fetch HyperRedis list
	var redisList hyperv1alpha1.HyperRedisList
	if err := r.List(ctx, &redisList); err != nil {
		reqLogger.Error(err, "unable to list HyperRedis")
		return ctrl.Result{}, err
	}

	// Fetch HyperPolicy list
	var policyList hyperv1alpha1.HyperPolicyList
	if err := r.List(ctx, &policyList); err != nil {
		reqLogger.Error(err, "unable to list HyperPolicies")
		return ctrl.Result{}, err
	}

	// Fetch HyperRoute list
	var routeList hyperv1alpha1.HyperRouteList
	if err := r.List(ctx, &routeList); err != nil {
		reqLogger.Error(err, "unable to list HyperRoutes")
		return ctrl.Result{}, err
	}

	// Route Sorting by Priority descending
	routes := routeList.Items
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Spec.Priority > routes[j].Spec.Priority
	})

	// Mapping to Engine Structs
	engineConfig := config.Config{
		Server: config.ServerConfig{
			Address:              activeConfig.Spec.ServerAddress,
			MaxConcurrentStreams: activeConfig.Spec.MaxConcurrentStreams,
		},
		Telemetry: config.TelemetryConfig{
			Logging: mylogger.LoggingConfig{
				Level: string(activeConfig.Spec.LogLevel),
			},
		},
		Redis:  make(map[string]config.RedisServiceConfig),
		Chains: make(map[string]config.Chain),
		Router: config.RouterConfig{
			Routes: []config.RouteConfig{},
		},
	}

	// Map HyperRedis list
	for _, hr := range redisList.Items {
		engineConfig.Redis[hr.Name] = config.RedisServiceConfig{
			URL:      hr.Spec.Url,
			Type:     string(hr.Spec.Type),
			PoolSize: hr.Spec.PoolSize,
			Timeout:  hr.Spec.Timeout,
		}
	}

	// Map HyperPolicy list
	for _, hp := range policyList.Items {
		var chain config.Chain
		for _, f := range hp.Spec.Filters {
			var opts map[string]interface{}
			if f.Options != nil && f.Options.Raw != nil {
				_ = json.Unmarshal(f.Options.Raw, &opts)
			}
			chain = append(chain, config.FilterConfig{
				Type:    f.Type,
				Options: opts,
			})
		}
		engineConfig.Chains[hp.Name] = chain
	}

	// Map HyperRoute list
	for _, hr := range routes {
		var matchConfigs []config.MatchConfig
		for _, m := range hr.Spec.Matches {
			var hmConfigs map[string]config.HeaderMatchConfig
			if m.Headers != nil {
				hmConfigs = make(map[string]config.HeaderMatchConfig)
				for k, v := range m.Headers {
					hmConfigs[k] = config.HeaderMatchConfig{Exact: v}
				}
			}
			
			matchConfigs = append(matchConfigs, config.MatchConfig{
				PathPrefix:       m.PathPrefix,
				PathRegexPattern: m.PathRegexPattern,
				Headers:          hmConfigs,
			})
		}
		engineConfig.Router.Routes = append(engineConfig.Router.Routes, config.RouteConfig{
			TargetChain: hr.Spec.TargetPolicy,
			Matches:     matchConfigs,
		})
	}

	// YAML Generation
	yamlBytes, err := yaml.Marshal(&engineConfig)
	if err != nil {
		reqLogger.Error(err, "unable to marshal config to YAML")
		return ctrl.Result{}, err
	}

	targetNS := activeConfig.Spec.TargetNamespace
	if targetNS == "" {
		targetNS = "hyper-system"
	}

	// ConfigMap Write
	cm := &corev1.ConfigMap{}
	cmName := types.NamespacedName{
		Name:      "hyper-engine-config",
		Namespace: targetNS,
	}
	
	err = r.Get(ctx, cmName, cm)
	if err != nil && errors.IsNotFound(err) {
		// Create ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName.Name,
				Namespace: cmName.Namespace,
			},
			Data: map[string]string{
				"config.yaml": string(yamlBytes),
			},
		}
		if err := r.Create(ctx, cm); err != nil {
			reqLogger.Error(err, "unable to create ConfigMap")
			return ctrl.Result{}, err
		}
		reqLogger.Info("Created ConfigMap hyper-engine-config")
	} else if err == nil {
		// Update ConfigMap
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["config.yaml"] = string(yamlBytes)
		if err := r.Update(ctx, cm); err != nil {
			reqLogger.Error(err, "unable to update ConfigMap")
			return ctrl.Result{}, err
		}
		reqLogger.Info("Updated ConfigMap hyper-engine-config")
	} else {
		reqLogger.Error(err, "unable to get ConfigMap")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HyperRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	triggerFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "global", Namespace: "default"}}}
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1alpha1.HyperRoute{}).
		Watches(&hyperv1alpha1.HyperPolicy{}, triggerFunc).
		Watches(&hyperv1alpha1.HyperConfig{}, triggerFunc).
		Watches(&hyperv1alpha1.HyperRedis{}, triggerFunc).
		Complete(r)
}
