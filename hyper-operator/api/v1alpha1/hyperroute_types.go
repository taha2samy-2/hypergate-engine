package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
type MatchRule struct {
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`

	// +optional
	PathRegexPattern string `json:"pathRegexPattern,omitempty"`

	// +optional
	Headers map[string]string `json:"headers,omitempty"`
}

// +kubebuilder:object:generate=true
// HyperRouteSpec defines the desired state of HyperRoute
type HyperRouteSpec struct {
	// +kubebuilder:validation:Required
	Priority int `json:"priority"`

	// +kubebuilder:validation:Required
	TargetPolicy string `json:"targetPolicy"`

	// +kubebuilder:validation:Required
	Matches []MatchRule `json:"matches"`
}

// +kubebuilder:object:generate=true
// HyperRouteStatus defines the observed state of HyperRoute
type HyperRouteStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Priority",type="integer",JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="Target Policy",type="string",JSONPath=".spec.targetPolicy"

// HyperRoute is the Schema for the hyperroutes API
type HyperRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HyperRouteSpec   `json:"spec,omitempty"`
	Status HyperRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HyperRouteList contains a list of HyperRoute
type HyperRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperRoute{}, &HyperRouteList{})
}
