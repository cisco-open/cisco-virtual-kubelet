// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NetworkControllerType is the stable registry key for a controller adapter.
// It is intentionally pattern-constrained rather than enumerated so a new
// adapter can be registered without changing the API schema.
//
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=63
// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]*[a-z0-9])?$`
type NetworkControllerType string

// NetworkControllerPhase is the coarse connection state of a controller.
//
// +kubebuilder:validation:Enum=Pending;Connecting;Ready;Degraded;Error;Paused
type NetworkControllerPhase string

const (
	NetworkControllerPhasePending    NetworkControllerPhase = "Pending"
	NetworkControllerPhaseConnecting NetworkControllerPhase = "Connecting"
	NetworkControllerPhaseReady      NetworkControllerPhase = "Ready"
	NetworkControllerPhaseDegraded   NetworkControllerPhase = "Degraded"
	NetworkControllerPhaseError      NetworkControllerPhase = "Error"
	NetworkControllerPhasePaused     NetworkControllerPhase = "Paused"
)

// Standard NetworkController condition types. Adapters may add narrowly
// scoped conditions while keeping Ready as the user-facing roll-up.
const (
	NetworkControllerConditionReady            = "Ready"
	NetworkControllerConditionAdapterAvailable = "AdapterAvailable"
	NetworkControllerConditionEndpointUnique   = "EndpointUnique"
	NetworkControllerConditionWorkerAvailable  = "WorkerAvailable"
	NetworkControllerConditionAuthenticated    = "Authenticated"
	NetworkControllerConditionAPICompatible    = "APICompatible"
)

// NetworkControllerIntentSecretSource authorizes one Secret key for use by
// controller-centric Network as Code intent. NetworkControllerConfig objects
// refer to Alias only; they can never broaden the worker's Secret access by
// naming an arbitrary Secret or key themselves.
//
// All references are implicitly in the NetworkController's namespace.
type NetworkControllerIntentSecretSource struct {
	// Alias is the stable, non-sensitive name used by
	// NetworkControllerConfig.spec.secretRefs[].source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]*[a-z0-9])?$`
	Alias string `json:"alias"`

	// Name is the Secret in the same namespace as the NetworkController.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`

	// Key selects one entry from Secret.data.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[-._a-zA-Z0-9]+$`
	Key string `json:"key"`
}

// NetworkControllerSecretReference names a Secret in the same namespace as
// the NetworkController. The registered adapter owns the expected Secret key
// contract; credential material is never copied into spec or status.
type NetworkControllerSecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// NetworkControllerConfigMapKeyReference selects one key from a ConfigMap in
// the same namespace as the NetworkController.
type NetworkControllerConfigMapKeyReference struct {
	// Name of the ConfigMap.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`

	// Key containing the PEM-encoded CA bundle.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[-._a-zA-Z0-9]+$`
	Key string `json:"key"`
}

// NetworkControllerTLSConfig controls server certificate verification.
//
// +kubebuilder:validation:XValidation:rule="!has(self.caConfigMapRef) || !self.insecureSkipVerify",message="insecureSkipVerify cannot be combined with caConfigMapRef"
type NetworkControllerTLSConfig struct {
	// CAConfigMapRef selects a PEM CA bundle. When omitted, the system trust
	// store is used.
	// +optional
	CAConfigMapRef *NetworkControllerConfigMapKeyReference `json:"caConfigMapRef,omitempty"`

	// InsecureSkipVerify disables server certificate verification. It exists
	// only for controlled lab migrations and defaults to false.
	// +kubebuilder:default=false
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// NetworkControllerRateLimit overrides an adapter's conservative request-rate
// defaults. Omitting it lets the adapter select a controller-appropriate
// policy.
type NetworkControllerRateLimit struct {
	// RequestsPerSecond is the sustained request rate.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	RequestsPerSecond int32 `json:"requestsPerSecond"`

	// Burst is the maximum instantaneous request burst.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	Burst int32 `json:"burst"`
}

// NetworkControllerConnectionPolicy contains bounded, controller-neutral HTTP
// and health-check behavior. Authentication, pagination, and task semantics
// remain adapter-owned.
type NetworkControllerConnectionPolicy struct {
	// RequestTimeout bounds one controller API request to (0s, 24h].
	// +kubebuilder:default="30s"
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s') && duration(self) <= duration('24h')",message="requestTimeout must be greater than 0s and at most 24h"
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`

	// HealthCheckInterval is the cadence for controller reachability checks and
	// must be in [30s, 24h].
	// +kubebuilder:default="1m"
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('30s') && duration(self) <= duration('24h')",message="healthCheckInterval must be at least 30s and at most 24h"
	// +optional
	HealthCheckInterval *metav1.Duration `json:"healthCheckInterval,omitempty"`

	// MaxConcurrentRequests bounds in-flight calls for this endpoint.
	// +kubebuilder:default=4
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	// +optional
	MaxConcurrentRequests int32 `json:"maxConcurrentRequests,omitempty"`

	// RateLimit optionally overrides the adapter's endpoint rate policy.
	// +optional
	RateLimit *NetworkControllerRateLimit `json:"rateLimit,omitempty"`
}

// NetworkControllerSpec declares one external network-controller endpoint.
// Controller type and endpoint are immutable because redirecting either in
// place would make existing status, task IDs, and ownership claims ambiguous.
//
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="self.endpoint == oldSelf.endpoint",message="endpoint is immutable; create a new NetworkController for a different endpoint"
type NetworkControllerSpec struct {
	// Type selects a registered controller adapter, for example
	// catalyst-center or nso.
	// +kubebuilder:validation:Required
	Type NetworkControllerType `json:"type"`

	// Endpoint is the immutable HTTPS base URL for one logical controller.
	// Credentials in URL userinfo are rejected by runtime validation. Create a
	// new NetworkController instead of redirecting task and ownership state to a
	// different endpoint.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('@') && !self.contains('?') && !self.contains('#')",message="endpoint must not contain URL userinfo, query, or fragment components"
	Endpoint string `json:"endpoint"`

	// CredentialSecretRef names the controller credential Secret in this
	// NetworkController's namespace.
	// +kubebuilder:validation:Required
	CredentialSecretRef NetworkControllerSecretReference `json:"credentialSecretRef"`

	// IntentSecretSources is the controller-owned authorization allow-list for
	// sensitive values injected into Network as Code intent. Config resources
	// can reference these entries only by Alias; Secret names and keys remain
	// centralized on this endpoint object for review and worker-volume wiring.
	// +kubebuilder:validation:MaxItems=128
	// +listType=map
	// +listMapKey=alias
	// +optional
	IntentSecretSources []NetworkControllerIntentSecretSource `json:"intentSecretSources,omitempty"`

	// TLS controls controller server trust.
	// +optional
	TLS *NetworkControllerTLSConfig `json:"tls,omitempty"`

	// PreferredAPIVersion optionally pins an adapter-supported API version.
	// Empty requests adapter discovery and negotiation.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PreferredAPIVersion string `json:"preferredAPIVersion,omitempty"`

	// Connection configures neutral request and health-check bounds.
	// +kubebuilder:default={}
	// +optional
	Connection NetworkControllerConnectionPolicy `json:"connection,omitempty"`

	// Paused stops external reconciliation while retaining observed status.
	// +kubebuilder:default=false
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// NetworkControllerCapabilityStatus reports one optional adapter capability.
type NetworkControllerCapabilityStatus struct {
	// Name is a stable, low-cardinality capability key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]*[a-z0-9])?$`
	Name string `json:"name"`

	// Supported reports whether this endpoint exposes the capability.
	// +kubebuilder:validation:Required
	Supported bool `json:"supported"`

	// Message is a bounded, sanitized explanation.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkControllerNetAsCodeStatus is the bounded Network as Code contract
// advertised by the registered adapter.
type NetworkControllerNetAsCodeStatus struct {
	// Format is the qualified model format, for example
	// netascode-catalyst-center.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^netascode-[a-z0-9]+(-[a-z0-9]+)*$`
	Format string `json:"format"`

	// Stripe is the upstream Network as Code technology stripe.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Stripe string `json:"stripe"`

	// ModelVersions lists versions qualified by this adapter build.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	ModelVersions []string `json:"modelVersions,omitempty"`

	// Sections lists the controller-centric sections understood by the
	// adapter.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9_]*$`
	// +listType=set
	// +optional
	Sections []string `json:"sections,omitempty"`
}

// NetworkControllerWorkerStatus identifies the isolated connector worker
// materialized for this controller. Names are status-only references; they do
// not broaden the NetworkController's credential scope.
type NetworkControllerWorkerStatus struct {
	// Name is the stable logical worker name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// DeploymentName is the namespaced Kubernetes Deployment that runs the
	// connector worker.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	DeploymentName string `json:"deploymentName"`

	// ReadyReplicas is copied from the worker Deployment status.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
}

// NetworkControllerStatus is the bounded observed state of a controller
// endpoint. It never contains credentials, tokens, or raw API responses.
type NetworkControllerStatus struct {
	// Phase is a coarse connection summary.
	// +optional
	Phase NetworkControllerPhase `json:"phase,omitempty"`

	// ObservedGeneration is the metadata generation most recently processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ControllerVersion is the discovered product version.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ControllerVersion string `json:"controllerVersion,omitempty"`

	// APIVersion is the negotiated controller API version.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// LastAttemptTime records the most recent connection attempt.
	// +optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`

	// LastSuccessfulConnectionTime records the most recent successful API
	// exchange.
	// +optional
	LastSuccessfulConnectionTime *metav1.Time `json:"lastSuccessfulConnectionTime,omitempty"`

	// Capabilities is the adapter capability summary for this endpoint.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	// +optional
	Capabilities []NetworkControllerCapabilityStatus `json:"capabilities,omitempty"`

	// NetAsCode is the controller-centric model contract advertised by the
	// adapter.
	// +optional
	NetAsCode *NetworkControllerNetAsCodeStatus `json:"netAsCode,omitempty"`

	// Worker identifies the isolated connector worker for this endpoint.
	// +optional
	Worker *NetworkControllerWorkerStatus `json:"worker,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +kubebuilder:validation:MaxItems=32
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// NetworkController declares a controller endpoint and its connection policy.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=netctrl
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.controllerVersion`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NetworkController struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkControllerSpec   `json:"spec"`
	Status NetworkControllerStatus `json:"status,omitempty"`
}

// NetworkControllerList contains a list of NetworkController objects.
//
// +kubebuilder:object:root=true
type NetworkControllerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkController `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkController{}, &NetworkControllerList{})
}
