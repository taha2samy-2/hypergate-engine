package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// HeaderOptions defines custom header modifications.
type HeaderOptions struct {
	// +optional
	Add map[string]string `json:"add,omitempty" yaml:"add,omitempty"`

	// +optional
	Override map[string]string `json:"override,omitempty" yaml:"override,omitempty"`

	// +optional
	Remove []string `json:"remove,omitempty" yaml:"remove,omitempty"`
}

// +kubebuilder:object:generate=true
// HeaderModifierFilterSpec defines the desired state of HeaderModifierFilter
type HeaderModifierFilterSpec struct {
	// Upstream holds modifications applied to the upstream (backend-bound) request.
	// +optional
	Upstream HeaderOptions `json:"upstream,omitempty" yaml:"upstream,omitempty"`

	// Downstream holds modifications applied to the downstream (client-bound) response.
	// +optional
	Downstream HeaderOptions `json:"downstream,omitempty" yaml:"downstream,omitempty"`

	// Add specifies headers to add to the request (top-level shorthand).
	// +optional
	Add map[string]string `json:"add,omitempty" yaml:"add,omitempty"`

	// Override specifies headers to set/override on the request (top-level shorthand).
	// +optional
	Override map[string]string `json:"override,omitempty" yaml:"override,omitempty"`

	// Remove specifies headers to remove from the request (top-level shorthand).
	// +optional
	Remove []string `json:"remove,omitempty" yaml:"remove,omitempty"`
}

// +kubebuilder:object:generate=true
// HeaderModifierFilterStatus defines the observed state of HeaderModifierFilter
type HeaderModifierFilterStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// HeaderModifierFilter is the Schema for the headermodifierfilters API
type HeaderModifierFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HeaderModifierFilterSpec   `json:"spec,omitempty"`
	Status HeaderModifierFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HeaderModifierFilterList contains a list of HeaderModifierFilter
type HeaderModifierFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HeaderModifierFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HeaderModifierFilter{}, &HeaderModifierFilterList{})
}
