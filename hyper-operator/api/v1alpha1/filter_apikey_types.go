/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// ApiKeyStatusCheck defines the status check config for the API Key validator CRD.
type ApiKeyStatusCheck struct {
	// Enabled status checks
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// FieldName is the status field name in Redis
	// +optional
	FieldName string `json:"fieldName,omitempty" yaml:"field_name,omitempty"`

	// ExpectedValue is the expected value of status to accept the API Key
	// +optional
	ExpectedValue string `json:"expectedValue,omitempty" yaml:"expected_value,omitempty"`
}

// +kubebuilder:object:generate=true
// ApiKeyOutputMapping defines the mapping of resolved metadata to TargetHeader.
type ApiKeyOutputMapping struct {
	// TargetHeader to inject the value
	// +kubebuilder:validation:Required
	TargetHeader string `json:"targetHeader" yaml:"target_header"`

	// RedisField to extract from hash format
	// +optional
	RedisField string `json:"redisField,omitempty" yaml:"redis_field,omitempty"`

	// JsonPath to extract from JSON format
	// +optional
	JsonPath string `json:"jsonPath,omitempty" yaml:"json_path,omitempty"`
}

// ApiKeyFilterSpec defines the desired state of ApiKeyFilter
type ApiKeyFilterSpec struct {
	// KeyNames is the list of keys to look up in query params or headers
	// +optional
	// +kubebuilder:default={"x-api-key"}
	KeyNames []string `json:"keyNames,omitempty" yaml:"key_names,omitempty"`

	// KeyInHeader enables searching for the API key in the request headers
	// +optional
	// +kubebuilder:default=true
	KeyInHeader bool `json:"keyInHeader,omitempty" yaml:"key_in_header,omitempty"`

	// KeyInQuery enables searching for the API key in the query parameters
	// +optional
	// +kubebuilder:default=true
	KeyInQuery bool `json:"keyInQuery,omitempty" yaml:"key_in_query,omitempty"`

	// HideCredentials strips the API key from headers and query parameters upstream
	// +optional
	// +kubebuilder:default=true
	HideCredentials bool `json:"hideCredentials,omitempty" yaml:"hide_credentials,omitempty"`

	// RedisService is the name of the Redis service to use
	// +kubebuilder:validation:Required
	RedisService string `json:"redisService" yaml:"redis_service"`

	// RedisKeyPrefix is the prefix used when looking up keys in Redis
	// +optional
	// +kubebuilder:default="apikey:"
	RedisKeyPrefix string `json:"redisKeyPrefix,omitempty" yaml:"redis_key_prefix,omitempty"`

	// HashAlgorithm is the algorithm used to hash the API key ("sha256", "md5", "none")
	// +kubebuilder:validation:Enum=sha256;md5;none
	// +kubebuilder:default="sha256"
	// +optional
	HashAlgorithm string `json:"hashAlgorithm,omitempty" yaml:"hash_algorithm,omitempty"`

	// ValueFormat defines how values are stored in Redis ("plain", "hash", "json")
	// +kubebuilder:validation:Enum=plain;hash;json
	// +kubebuilder:default="hash"
	// +optional
	ValueFormat string `json:"valueFormat,omitempty" yaml:"value_format,omitempty"`

	// Delimiter is used to split plain format values
	// +optional
	// +kubebuilder:default="|"
	Delimiter string `json:"delimiter,omitempty" yaml:"delimiter,omitempty"`

	// StatusCheck configuration
	// +optional
	StatusCheck ApiKeyStatusCheck `json:"statusCheck,omitempty" yaml:"status_check,omitempty"`

	// OutputMappings defines the mappings from Redis output to upstream headers
	// +optional
	OutputMappings []ApiKeyOutputMapping `json:"outputMappings,omitempty" yaml:"output_mappings,omitempty"`
}

// ApiKeyFilterStatus defines the observed state of ApiKeyFilter
type ApiKeyFilterStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=apikf
// +kubebuilder:printcolumn:name="Redis Service",type="string",JSONPath=".spec.redisService"
// +kubebuilder:printcolumn:name="Value Format",type="string",JSONPath=".spec.valueFormat"
// +kubebuilder:printcolumn:name="Hash Algorithm",type="string",JSONPath=".spec.hashAlgorithm"

// ApiKeyFilter is the Schema for the apikeyfilters API
type ApiKeyFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApiKeyFilterSpec   `json:"spec,omitempty"`
	Status ApiKeyFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ApiKeyFilterList contains a list of ApiKeyFilter
type ApiKeyFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApiKeyFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApiKeyFilter{}, &ApiKeyFilterList{})
}
