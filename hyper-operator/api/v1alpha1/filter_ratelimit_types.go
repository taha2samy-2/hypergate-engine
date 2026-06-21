package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// ResponseHeaders defines configuration for injecting rate limit headers downstream.
type ResponseHeaders struct {
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// +kubebuilder:default="RateLimit-Limit"
	// +optional
	LimitHeader string `json:"limitHeader,omitempty" yaml:"limit_header,omitempty"`

	// +kubebuilder:default="RateLimit-Remaining"
	// +optional
	RemainingHeader string `json:"remainingHeader,omitempty" yaml:"remaining_header,omitempty"`

	// +kubebuilder:default="RateLimit-Reset"
	// +optional
	ResetHeader string `json:"resetHeader,omitempty" yaml:"reset_header,omitempty"`
}

// +kubebuilder:object:generate=true
// DynamicCost defines configuration for extracting dynamic cost from headers.
type DynamicCost struct {
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// +optional
	SourceHeader string `json:"sourceHeader,omitempty" yaml:"source_header,omitempty"`

	// +optional
	DefaultFallbackCost int64 `json:"defaultFallbackCost,omitempty" yaml:"default_fallback_cost,omitempty"`

	// +optional
	MaxAllowedCost int64 `json:"maxAllowedCost,omitempty" yaml:"max_allowed_cost,omitempty"`
}

// +kubebuilder:object:generate=true
// DescriptorEntryDef defines a descriptor key/value criteria.
type DescriptorEntryDef struct {
	// +kubebuilder:validation:Required
	Key string `json:"key" yaml:"key"`

	// +optional
	Value string `json:"value,omitempty" yaml:"value,omitempty"`
}

// +kubebuilder:object:generate=true
// Descriptor defines a single descriptor rule for rate limiting.
type Descriptor struct {
	// +kubebuilder:validation:Required
	Entries []DescriptorEntryDef `json:"entries" yaml:"entries"`

	// +optional
	Limit uint32 `json:"limit,omitempty" yaml:"limit,omitempty"`

	// +optional
	Unit string `json:"unit,omitempty" yaml:"unit,omitempty"`

	// +optional
	MaxTokens float64 `json:"maxTokens,omitempty" yaml:"max_tokens,omitempty"`

	// +optional
	FillRate float64 `json:"fillRate,omitempty" yaml:"fill_rate,omitempty"`

	// +optional
	BucketCapacity uint32 `json:"bucketCapacity,omitempty" yaml:"bucket_capacity,omitempty"`

	// +optional
	LeakRate float64 `json:"leakRate,omitempty" yaml:"leak_rate,omitempty"`

	// +kubebuilder:default=false
	// +optional
	ShadowMode bool `json:"shadowMode,omitempty" yaml:"shadow_mode,omitempty"`

	// +kubebuilder:default=false
	// +optional
	FailOpen bool `json:"failOpen,omitempty" yaml:"fail_open,omitempty"`
}

// +kubebuilder:object:generate=true
// RateLimitFilterSpec defines the desired state of RateLimitFilter
type RateLimitFilterSpec struct {
	// +kubebuilder:validation:Required
	Domain string `json:"domain" yaml:"domain"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=fixed_window;sliding_window_counter;token_bucket;leaky_bucket;sliding_window_log
	Algorithm string `json:"algorithm" yaml:"algorithm"`

	// +kubebuilder:validation:Required
	RedisService string `json:"redisService" yaml:"redis_service"`

	// +optional
	ResponseHeaders ResponseHeaders `json:"responseHeaders,omitempty" yaml:"response_headers,omitempty"`

	// +optional
	DynamicCost DynamicCost `json:"dynamicCost,omitempty" yaml:"dynamic_cost,omitempty"`

	// +optional
	HeaderMappings map[string]string `json:"headerMappings,omitempty" yaml:"header_mappings,omitempty"`

	// +optional
	Descriptors []Descriptor `json:"descriptors,omitempty" yaml:"descriptors,omitempty"`
}

// +kubebuilder:object:generate=true
// RateLimitFilterStatus defines the observed state of RateLimitFilter
type RateLimitFilterStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// RateLimitFilter is the Schema for the ratelimitfilters API
type RateLimitFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RateLimitFilterSpec   `json:"spec,omitempty"`
	Status RateLimitFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RateLimitFilterList contains a list of RateLimitFilter
type RateLimitFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RateLimitFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RateLimitFilter{}, &RateLimitFilterList{})
}
