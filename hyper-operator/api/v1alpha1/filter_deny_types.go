package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// DenyMatchConfig holds criteria to match a request for blocking.
type DenyMatchConfig struct {
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty" yaml:"path_prefix,omitempty"`

	// +optional
	PathRegex string `json:"pathRegex,omitempty" yaml:"path_regex,omitempty"`

	// +optional
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	// +optional
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty" yaml:"response_headers,omitempty"`

	// +optional
	NotHeaders map[string]string `json:"notHeaders,omitempty" yaml:"not_headers,omitempty"`

	// +optional
	NotResponseHeaders map[string]string `json:"notResponseHeaders,omitempty" yaml:"not_response_headers,omitempty"`
}

// +kubebuilder:object:generate=true
// DenyFilterSpec defines the desired state of DenyFilter
type DenyFilterSpec struct {
	// +kubebuilder:default=403
	// +optional
	StatusCode int32 `json:"statusCode,omitempty" yaml:"status_code,omitempty"`

	// +optional
	Body string `json:"body,omitempty" yaml:"body,omitempty"`

	// +optional
	Match DenyMatchConfig `json:"match,omitempty" yaml:"match,omitempty"`
}

// +kubebuilder:object:generate=true
// DenyFilterStatus defines the observed state of DenyFilter
type DenyFilterStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DenyFilter is the Schema for the denyfilters API
type DenyFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DenyFilterSpec   `json:"spec,omitempty"`
	Status DenyFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DenyFilterList contains a list of DenyFilter
type DenyFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DenyFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DenyFilter{}, &DenyFilterList{})
}
