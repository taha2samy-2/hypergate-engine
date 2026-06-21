package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// Variable defines how to extract and fall back a single input parameter.
type Variable struct {
	// Source is the source for the variable.
	Source string `json:"source" yaml:"source"`

	// Default value for the variable.
	// +optional
	Default string `json:"default,omitempty" yaml:"default,omitempty"`

	// RegexPattern is the regex pattern to extract value.
	// +optional
	RegexPattern string `json:"regexPattern,omitempty" yaml:"regex_pattern,omitempty"`

	// JSONPath to extract value if source is JSON.
	// +optional
	JSONPath string `json:"jsonPath,omitempty" yaml:"json_path,omitempty"`
}

// +kubebuilder:object:generate=true
// OutputMappingSpec defines where to extract from the returned JSON and which header to inject it into.
type OutputMappingSpec struct {
	// JSONPath to extract from returned JSON.
	// +optional
	JSONPath string `json:"jsonPath,omitempty" yaml:"json_path,omitempty"`

	// TargetHeader to inject the extracted value.
	TargetHeader string `json:"targetHeader" yaml:"target_header"`
}

// +kubebuilder:object:generate=true
// RedisMetadataEnricherFilterSpec defines the desired state of RedisMetadataEnricherFilter
type RedisMetadataEnricherFilterSpec struct {
	// RedisService is the name of the Redis service to use.
	RedisService string `json:"redisService" yaml:"redis_service"`

	// CacheSizeMB is the size of the cache in megabytes.
	// +optional
	CacheSizeMB int `json:"cacheSizeMB,omitempty" yaml:"cache_size_mb,omitempty"`

	// CacheTimeout is the cache timeout duration string.
	// +optional
	CacheTimeout string `json:"cacheTimeout,omitempty" yaml:"cache_timeout,omitempty"`

	// Variables defines how to extract input parameters.
	// +optional
	Variables map[string]Variable `json:"variables,omitempty" yaml:"variables,omitempty"`

	// KeyPattern is the pattern for the Redis key.
	KeyPattern string `json:"keyPattern" yaml:"key_pattern"`

	// OutputMappings defines where to extract and inject the returned JSON.
	// +optional
	OutputMappings []OutputMappingSpec `json:"outputMappings,omitempty" yaml:"output_mappings,omitempty"`
}

// +kubebuilder:object:generate=true
// RedisMetadataEnricherFilterStatus defines the observed state of RedisMetadataEnricherFilter
type RedisMetadataEnricherFilterStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// RedisMetadataEnricherFilter is the Schema for the redismetadataenricherfilters API
type RedisMetadataEnricherFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisMetadataEnricherFilterSpec   `json:"spec,omitempty"`
	Status RedisMetadataEnricherFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisMetadataEnricherFilterList contains a list of RedisMetadataEnricherFilter
type RedisMetadataEnricherFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisMetadataEnricherFilter `json:"items"`
}
