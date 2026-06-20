package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"unsafe"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hyperv1alpha1 "github.com/taha/myprog/hyper-operator/api/v1alpha1"
	"github.com/taha/myprog/internal/config"
	"github.com/taha/myprog/internal/filters"
	"github.com/taha/myprog/internal/redis"
)

var policylog = logf.Log.WithName("hyperpolicy-resource")

// +kubebuilder:webhook:path=/validate-policy-hyper-io-v1alpha1-hyperpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=hyper.io,resources=hyperpolicies,verbs=create;update,versions=v1alpha1,name=vhyperpolicy.kb.io,admissionReviewVersions=v1

// SetupWebhookWithManager registers the webhook for HyperPolicy in the manager.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &hyperv1alpha1.HyperPolicy{}).
		WithValidator(&HyperPolicyCustomValidator{}).
		Complete()
}

// HyperPolicyCustomValidator implements admission.Validator (Strongly-Typed)
type HyperPolicyCustomValidator struct{}

// Confirming compilation-time contract with the modern generic Validator interface
var _ admission.Validator[*hyperv1alpha1.HyperPolicy] = &HyperPolicyCustomValidator{}

type DummyRedisClient struct{}

func (d *DummyRedisClient) DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error {
	return nil
}
func (d *DummyRedisClient) PipeAppend(pipeline redis.Pipeline, rcv interface{}, cmd, key string, args ...interface{}) redis.Pipeline {
	return pipeline
}
func (d *DummyRedisClient) PipeDo(ctx context.Context, pipeline redis.Pipeline) error { return nil }
func (d *DummyRedisClient) Close() error                                              { return nil }
func (d *DummyRedisClient) NumActiveConns() int                                       { return 0 }

func init() {
	if redis.GlobalManager == nil {
		mgr, _ := redis.NewManager(&config.Config{})
		redis.GlobalManager = mgr
	}
	val := reflect.ValueOf(redis.GlobalManager).Elem()
	clientsField := val.FieldByName("clients")
	reflect.NewAt(clientsField.Type(), unsafe.Pointer(clientsField.UnsafeAddr())).Elem().Set(
		reflect.ValueOf(map[string]redis.Client{
			"myredis":      &DummyRedisClient{},
			"shared-redis": &DummyRedisClient{},
		}),
	)
}

// ValidateCreate implements admission.Validator (Strongly-Typed)
func (v *HyperPolicyCustomValidator) ValidateCreate(ctx context.Context, obj *hyperv1alpha1.HyperPolicy) (admission.Warnings, error) {
	policylog.Info("validate create", "name", obj.Name)
	return nil, v.validateFilters(obj)
}

// ValidateUpdate implements admission.Validator (Strongly-Typed)
func (v *HyperPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *hyperv1alpha1.HyperPolicy) (admission.Warnings, error) {
	policylog.Info("validate update", "name", newObj.Name)
	return nil, v.validateFilters(newObj)
}

// ValidateDelete implements admission.Validator (Strongly-Typed)
func (v *HyperPolicyCustomValidator) ValidateDelete(ctx context.Context, obj *hyperv1alpha1.HyperPolicy) (admission.Warnings, error) {
	policylog.Info("validate delete", "name", obj.Name)
	return nil, nil
}

func (v *HyperPolicyCustomValidator) validateFilters(policy *hyperv1alpha1.HyperPolicy) error {
	for _, filter := range policy.Spec.Filters {
		var rawOptions map[string]interface{}
		if filter.Options != nil && filter.Options.Raw != nil {
			_ = json.Unmarshal(filter.Options.Raw, &rawOptions)
		}

		if _, err := filters.CreateFilter(filter.Type, rawOptions); err != nil {
			return fmt.Errorf("failed to validate filter '%s': %w", filter.Type, err)
		}
	}
	return nil
}
