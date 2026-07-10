package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// ClaimToHeaderMapping instructs the OIDC filter to extract a JWT/UserInfo
// claim and inject it as an upstream request header.
type ClaimToHeaderMapping struct {
	// HeaderName is the upstream request header that will receive the claim value.
	// +kubebuilder:validation:Required
	HeaderName string `json:"headerName" yaml:"header_name"`

	// ClaimName is the JWT or UserInfo claim to extract.
	// +kubebuilder:validation:Required
	ClaimName string `json:"claimName" yaml:"claim_name"`
}

// +kubebuilder:object:generate=true
// OidcFilterSpec defines the desired state of OidcFilter, mapping 1:1
// with the engine's OIDCFilterConfig (internal/config/config.go).
type OidcFilterSpec struct {
	// ProviderName is a human-readable label used in log messages.
	// +kubebuilder:validation:Required
	ProviderName string `json:"providerName" yaml:"provider_name"`

	// IssuerUrl is the OIDC issuer base URL (e.g. https://accounts.example.com).
	// +kubebuilder:validation:Required
	IssuerUrl string `json:"issuerUrl" yaml:"issuer_url"`

	// ClientId is the OAuth2 client identifier expected in the JWT aud claim.
	// +kubebuilder:validation:Required
	ClientId string `json:"clientId" yaml:"client_id"`

	// SkipDiscovery disables automatic OIDC discovery. When true, JwksUrl
	// must be supplied explicitly.
	// +kubebuilder:default=false
	// +optional
	SkipDiscovery bool `json:"skipDiscovery,omitempty" yaml:"skip_discovery,omitempty"`

	// JwksUrl is the explicit JWKS endpoint. Required when SkipDiscovery is
	// true; otherwise constructed automatically from IssuerUrl.
	// +optional
	JwksUrl string `json:"jwksUrl,omitempty" yaml:"jwks_url,omitempty"`

	// JwksCacheDuration is the maximum age of a cached JWKS key set.
	// Accepts a Go duration string (e.g. "300s", "5m"). Default: "300s".
	// +kubebuilder:default="300s"
	// +optional
	JwksCacheDuration string `json:"jwksCacheDuration,omitempty" yaml:"jwks_cache_duration,omitempty"`

	// JwksTimeout is the HTTP client deadline for fetching the JWKS endpoint.
	// Accepts a Go duration string (e.g. "2s"). Default: "2s".
	// +kubebuilder:default="2s"
	// +optional
	JwksTimeout string `json:"jwksTimeout,omitempty" yaml:"jwks_timeout,omitempty"`

	// ClockSkew is the maximum tolerated clock difference when validating JWT
	// time claims (nbf, exp). Accepts a Go duration string. Default: "60s".
	// +kubebuilder:default="60s"
	// +optional
	ClockSkew string `json:"clockSkew,omitempty" yaml:"clock_skew,omitempty"`

	// UserInfoUrl is the OIDC UserInfo endpoint used to validate opaque
	// (non-JWT) access tokens.
	// +optional
	UserInfoUrl string `json:"userInfoUrl,omitempty" yaml:"userinfo_url,omitempty"`

	// UserInfoTimeout is the HTTP client deadline for calling UserInfoUrl.
	// Accepts a Go duration string. Default: "2s".
	// +kubebuilder:default="2s"
	// +optional
	UserInfoTimeout string `json:"userInfoTimeout,omitempty" yaml:"userinfo_timeout,omitempty"`

	// UserIdClaim is the JWT claim used as the principal identifier.
	// Default: "sub".
	// +kubebuilder:default="sub"
	// +optional
	UserIdClaim string `json:"userIdClaim,omitempty" yaml:"user_id_claim,omitempty"`

	// EmailClaim is the JWT claim containing the user's email address.
	// Default: "email".
	// +kubebuilder:default="email"
	// +optional
	EmailClaim string `json:"emailClaim,omitempty" yaml:"email_claim,omitempty"`

	// GroupsClaim is the JWT claim containing the user's group memberships.
	// Default: "groups".
	// +kubebuilder:default="groups"
	// +optional
	GroupsClaim string `json:"groupsClaim,omitempty" yaml:"groups_claim,omitempty"`

	// AllowUnverifiedEmail permits requests from users whose email_verified
	// claim is explicitly false. Default: false (strict).
	// +kubebuilder:default=false
	// +optional
	AllowUnverifiedEmail bool `json:"allowUnverifiedEmail,omitempty" yaml:"allow_unverified_email,omitempty"`

	// ClaimToHeaders defines zero or more claim-to-upstream-header mappings.
	// +optional
	ClaimToHeaders []ClaimToHeaderMapping `json:"claimToHeaders,omitempty" yaml:"claim_to_headers,omitempty"`
}

// +kubebuilder:object:generate=true
// OidcFilterStatus defines the observed state of OidcFilter
type OidcFilterStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=oidcf
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.providerName"
// +kubebuilder:printcolumn:name="Issuer",type="string",JSONPath=".spec.issuerUrl"

// OidcFilter is the Schema for the oidcfilters API
type OidcFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OidcFilterSpec   `json:"spec,omitempty"`
	Status OidcFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OidcFilterList contains a list of OidcFilter
type OidcFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OidcFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OidcFilter{}, &OidcFilterList{})
}
