package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FilterConfig struct {
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// +kubebuilder:validation:Required
	Options *apiextensionsv1.JSON `json:"options"`
}

// HyperPolicySpec defines the desired state of HyperPolicy
type HyperPolicySpec struct {
	// +kubebuilder:validation:Required
	Filters []FilterConfig `json:"filters"`
}

// HyperPolicyStatus defines the observed state of HyperPolicy
type HyperPolicyStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// HyperPolicy is the Schema for the hyperpolicies API
type HyperPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HyperPolicySpec   `json:"spec,omitempty"`
	Status HyperPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HyperPolicyList contains a list of HyperPolicy
type HyperPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperPolicy{}, &HyperPolicyList{})
}
