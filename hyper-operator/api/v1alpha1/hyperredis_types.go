package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=SINGLE;CLUSTER;SENTINEL
type RedisType string

const (
	RedisTypeSingle   RedisType = "SINGLE"
	RedisTypeCluster  RedisType = "CLUSTER"
	RedisTypeSentinel RedisType = "SENTINEL"
)

// +kubebuilder:validation:Enum=Connected;Error;Pending
type RedisState string

const (
	RedisStateConnected RedisState = "Connected"
	RedisStateError     RedisState = "Error"
	RedisStatePending   RedisState = "Pending"
)

// +kubebuilder:object:generate=true
// HyperRedisSpec defines the desired state of HyperRedis
type HyperRedisSpec struct {
	// +kubebuilder:validation:Required
	Url string `json:"url"`

	// +kubebuilder:validation:Required
	Type RedisType `json:"type"`

	// +kubebuilder:validation:Minimum=1
	// +optional
	PoolSize int `json:"poolSize,omitempty"`

	// +optional
	Timeout string `json:"timeout,omitempty"`

	// +optional
	ActiveConnHealthCheck bool `json:"activeConnHealthCheck,omitempty"`
}

// +kubebuilder:object:generate=true
// HyperRedisStatus defines the observed state of HyperRedis
type HyperRedisStatus struct {
	State      RedisState  `json:"state,omitempty"`
	LastCheck  metav1.Time `json:"lastCheck,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The current state of the Redis connection"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type",description="Redis type"
// +kubebuilder:printcolumn:name="Last Check",type="date",JSONPath=".status.lastCheck",description="Last health check time"

// HyperRedis is the Schema for the hyperredis API
type HyperRedis struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HyperRedisSpec   `json:"spec,omitempty"`
	Status HyperRedisStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HyperRedisList contains a list of HyperRedis
type HyperRedisList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperRedis `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperRedis{}, &HyperRedisList{})
}
