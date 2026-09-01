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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NetworkControllerApplyMode controls whether controller drift is only
// reported or actively reconciled.
//
// +kubebuilder:validation:Enum=report;apply
type NetworkControllerApplyMode string

const (
	NetworkControllerApplyModeReport NetworkControllerApplyMode = "report"
	NetworkControllerApplyModeApply  NetworkControllerApplyMode = "apply"
)

// NetworkControllerRetentionPolicy controls whether remote resources are
// retained or deleted at an explicit ownership boundary.
//
// +kubebuilder:validation:Enum=Retain;Delete
type NetworkControllerRetentionPolicy string

const (
	NetworkControllerRetentionPolicyRetain NetworkControllerRetentionPolicy = "Retain"
	NetworkControllerRetentionPolicyDelete NetworkControllerRetentionPolicy = "Delete"
)

// NetworkControllerConfigPhase is the coarse reconciliation state of
// controller-centric Network as Code intent.
//
// +kubebuilder:validation:Enum=Pending;Validating;Planning;Applying;Waiting;Verifying;InSync;Drifted;Failed;Paused;LeaseBlocked
type NetworkControllerConfigPhase string

const (
	NetworkControllerConfigPhasePending      NetworkControllerConfigPhase = "Pending"
	NetworkControllerConfigPhaseValidating   NetworkControllerConfigPhase = "Validating"
	NetworkControllerConfigPhasePlanning     NetworkControllerConfigPhase = "Planning"
	NetworkControllerConfigPhaseApplying     NetworkControllerConfigPhase = "Applying"
	NetworkControllerConfigPhaseWaiting      NetworkControllerConfigPhase = "Waiting"
	NetworkControllerConfigPhaseVerifying    NetworkControllerConfigPhase = "Verifying"
	NetworkControllerConfigPhaseInSync       NetworkControllerConfigPhase = "InSync"
	NetworkControllerConfigPhaseDrifted      NetworkControllerConfigPhase = "Drifted"
	NetworkControllerConfigPhaseFailed       NetworkControllerConfigPhase = "Failed"
	NetworkControllerConfigPhasePaused       NetworkControllerConfigPhase = "Paused"
	NetworkControllerConfigPhaseLeaseBlocked NetworkControllerConfigPhase = "LeaseBlocked"
)

// Standard NetworkControllerConfig condition types.
const (
	NetworkControllerConfigConditionReady              = "Ready"
	NetworkControllerConfigConditionIntentValid        = "IntentValid"
	NetworkControllerConfigConditionIntentSecretsReady = "IntentSecretsReady"
	NetworkControllerConfigConditionAdapterAvailable   = "AdapterAvailable"
	NetworkControllerConfigConditionOwnershipAcquired  = "OwnershipAcquired"
)

// NetworkControllerRef names a NetworkController in the same namespace as the
// referring configuration object.
type NetworkControllerRef struct {
	// Name of the NetworkController.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// NetworkControllerSecretRef injects one Secret key at an RFC 6901-style path
// below a managed Network as Code section. Resolvers must not persist the
// injected value in status, revisions, logs, or events.
type NetworkControllerSecretRef struct {
	// Section must name one of spec.managedSections.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Section string `json:"section"`

	// Path is the JSON pointer within the section at which the Secret value is
	// injected.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^/.*`
	Path string `json:"path"`

	// Source names an alias authorized by the target
	// NetworkController.spec.intentSecretSources allow-list. The config object
	// cannot name a Secret or key directly.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]*[a-z0-9])?$`
	Source string `json:"source"`
}

// NetworkControllerNetAsCodeModelFormat names a controller-centric Network as
// Code model. Unlike the closed device model enum, this type is intentionally
// extensible; a registered adapter descriptor still requires one exact format
// before any plan or controller API call.
//
// +kubebuilder:validation:Pattern=`^netascode-[a-z0-9]+(-[a-z0-9]+)*$`
type NetworkControllerNetAsCodeModelFormat string

// NetworkControllerNetAsCodeModelSource records the exact controller-centric
// model contract and immutable source provenance used to produce intent.
type NetworkControllerNetAsCodeModelSource struct {
	// Format identifies the controller model dialect.
	// +kubebuilder:validation:Required
	Format NetworkControllerNetAsCodeModelFormat `json:"format"`

	// ModelVersion is the qualified upstream data-model or module version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ModelVersion string `json:"modelVersion"`

	// SchemaDigest identifies the canonical schema snapshot to which Source
	// declares conformance. It is compatibility metadata, not a payload digest.
	// +kubebuilder:validation:Pattern=`^sha256:[0-9a-f]{64}$`
	// +optional
	SchemaDigest string `json:"schemaDigest,omitempty"`

	// Resolved must be true: templates, defaults, groups, and inheritance are
	// expanded by the upstream Network as Code toolchain before CVK receives
	// the payload.
	// +kubebuilder:default=true
	Resolved bool `json:"resolved"`

	// Exporter identifies the tool and immutable version or digest that
	// produced Source.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Exporter string `json:"exporter,omitempty"`

	// SourceRevision identifies the customer Git commit, plan, or equivalent
	// immutable source revision.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	SourceRevision string `json:"sourceRevision,omitempty"`
}

// NetworkControllerConfigurationSource carries a controller-centric Network
// as Code document. It intentionally has its own API type rather than reusing
// the established device source type: the JSON shape stays familiar while the
// controller schema remains neutral and can evolve without changing existing
// IOS-XE or NX-OS CRDs.
type NetworkControllerConfigurationSource struct {
	// Inline is a resolved controller-centric Network as Code document or
	// fragment. The field is schemaless so controller model sections can evolve
	// with their qualified upstream model version.
	//
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Inline *runtime.RawExtension `json:"inline,omitempty"`

	// ConfigMapRef references a ConfigMap in the same namespace whose keyed
	// entry contains the resolved controller-centric Network as Code document.
	// +optional
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// NetworkControllerConfigSpec declares controller-centric Network as Code
// intent. ControllerRef and Scope are immutable because together they form the
// durable per-(controller,scope,section) ownership identity.
//
// +kubebuilder:validation:XValidation:rule="self.controllerRef == oldSelf.controllerRef",message="controllerRef is immutable"
// +kubebuilder:validation:XValidation:rule="self.scope == oldSelf.scope",message="scope is immutable"
// +kubebuilder:validation:XValidation:rule="self.modelSource.resolved == true",message="modelSource.resolved must be true"
// +kubebuilder:validation:XValidation:rule="has(self.modelSource.modelVersion) && size(self.modelSource.modelVersion) > 0",message="modelSource.modelVersion is required"
// +kubebuilder:validation:XValidation:rule="has(self.source.inline) != has(self.source.configMapRef)",message="exactly one of source.inline or source.configMapRef must be set"
type NetworkControllerConfigSpec struct {
	// ControllerRef targets a NetworkController in the same namespace.
	// +kubebuilder:validation:Required
	ControllerRef NetworkControllerRef `json:"controllerRef"`

	// Scope is an adapter-normalized ownership scope. "global" owns the
	// selected sections across the whole controller; adapters may define stable
	// narrower scopes without changing this API.
	// +kubebuilder:default=global
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`
	// +optional
	Scope string `json:"scope,omitempty"`

	// ManagedSections is the closed list of Network as Code sections this CR
	// owns within Scope. The selected adapter validates the supported set.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9_]*$`
	// +listType=set
	ManagedSections []string `json:"managedSections"`

	// Source carries the resolved controller-centric Network as Code document
	// or fragment.
	// +kubebuilder:validation:Required
	Source NetworkControllerConfigurationSource `json:"source"`

	// ModelSource records the exact Network as Code contract and provenance
	// used to produce Source. Controller config requires a resolved, versioned
	// model; the adapter validates its format and compatible versions.
	// +kubebuilder:validation:Required
	ModelSource NetworkControllerNetAsCodeModelSource `json:"modelSource"`

	// SecretRefs injects sensitive leaf values after resolving Source. Each
	// entry selects only an alias from the target NetworkController's
	// intentSecretSources allow-list; it cannot authorize a new Secret.
	// +kubebuilder:validation:MaxItems=128
	// +listType=map
	// +listMapKey=section
	// +listMapKey=path
	// +optional
	SecretRefs []NetworkControllerSecretRef `json:"secretRefs,omitempty"`

	// DriftDetectInterval is the cadence for authoritative controller reads.
	// It must be in [30s, 720h].
	// +kubebuilder:default="5m"
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('30s') && duration(self) <= duration('720h')",message="driftDetectInterval must be at least 30s and at most 720h"
	// +optional
	DriftDetectInterval *metav1.Duration `json:"driftDetectInterval,omitempty"`

	// Mode defaults to report so importing existing Network as Code ownership
	// cannot mutate a controller before an explicit cutover.
	// +kubebuilder:default=report
	// +optional
	Mode NetworkControllerApplyMode `json:"mode,omitempty"`

	// PrunePolicy controls resources absent from the current desired source.
	// Delete is honored only by adapters and sections that advertise safe
	// pruning support.
	// +kubebuilder:default=Retain
	// +optional
	PrunePolicy NetworkControllerRetentionPolicy `json:"prunePolicy,omitempty"`

	// DeletionPolicy controls whether remote resources are retained when this
	// CR is deleted. Retain is the non-destructive default.
	// +kubebuilder:default=Retain
	// +optional
	DeletionPolicy NetworkControllerRetentionPolicy `json:"deletionPolicy,omitempty"`

	// TaskTimeout bounds an asynchronous controller task before the reconciler
	// reports failure or an ambiguous outcome. It must be in (0s, 720h].
	// +kubebuilder:default="30m"
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s') && duration(self) <= duration('720h')",message="taskTimeout must be greater than 0s and at most 720h"
	// +optional
	TaskTimeout *metav1.Duration `json:"taskTimeout,omitempty"`
}

// NetworkControllerSectionState reports one managed section's state.
//
// +kubebuilder:validation:Enum=Pending;InSync;Drifted;Applying;ApplyError;Unsupported;Skipped
type NetworkControllerSectionState string

const (
	NetworkControllerSectionStatePending     NetworkControllerSectionState = "Pending"
	NetworkControllerSectionStateInSync      NetworkControllerSectionState = "InSync"
	NetworkControllerSectionStateDrifted     NetworkControllerSectionState = "Drifted"
	NetworkControllerSectionStateApplying    NetworkControllerSectionState = "Applying"
	NetworkControllerSectionStateApplyError  NetworkControllerSectionState = "ApplyError"
	NetworkControllerSectionStateUnsupported NetworkControllerSectionState = "Unsupported"
	NetworkControllerSectionStateSkipped     NetworkControllerSectionState = "Skipped"
)

// NetworkControllerSectionStatus reports reconciliation state for one managed
// Network as Code section.
type NetworkControllerSectionStatus struct {
	// Name is the managed section name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// State is the section reconciliation state.
	// +kubebuilder:validation:Required
	State NetworkControllerSectionState `json:"state"`

	// ResourceCount is the number of controller resources represented by this
	// section when the adapter can report it.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ResourceCount int64 `json:"resourceCount,omitempty"`

	// Message is a bounded, sanitized explanation.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkControllerTaskPhase reports a durable asynchronous controller task.
//
// +kubebuilder:validation:Enum=Accepted;Running;Succeeded;Failed;Unknown
type NetworkControllerTaskPhase string

const (
	NetworkControllerTaskPhaseAccepted  NetworkControllerTaskPhase = "Accepted"
	NetworkControllerTaskPhaseRunning   NetworkControllerTaskPhase = "Running"
	NetworkControllerTaskPhaseSucceeded NetworkControllerTaskPhase = "Succeeded"
	NetworkControllerTaskPhaseFailed    NetworkControllerTaskPhase = "Failed"
	NetworkControllerTaskPhaseUnknown   NetworkControllerTaskPhase = "Unknown"
)

// NetworkControllerTaskStatus stores only the task identity and sanitized
// lifecycle metadata needed to resume polling after a controller restart.
type NetworkControllerTaskStatus struct {
	// ID is the adapter-provided task identifier.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	ID string `json:"id"`

	// Section is the managed section that initiated the task.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Section string `json:"section"`

	// Operation is a stable adapter operation name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Operation string `json:"operation"`

	// Phase is the last observed task phase.
	// +kubebuilder:validation:Required
	Phase NetworkControllerTaskPhase `json:"phase"`

	// SubmittedAt records when the task was accepted by the controller.
	// +optional
	SubmittedAt *metav1.Time `json:"submittedAt,omitempty"`

	// LastPollTime records the most recent task status read.
	// +optional
	LastPollTime *metav1.Time `json:"lastPollTime,omitempty"`

	// Message is sanitized and must not contain raw controller response bodies.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkControllerDriftChange identifies the class of drift without storing
// desired or observed values.
//
// +kubebuilder:validation:Enum=Add;Update;Delete
type NetworkControllerDriftChange string

const (
	NetworkControllerDriftChangeAdd    NetworkControllerDriftChange = "Add"
	NetworkControllerDriftChangeUpdate NetworkControllerDriftChange = "Update"
	NetworkControllerDriftChangeDelete NetworkControllerDriftChange = "Delete"
)

// NetworkControllerDriftEntry records one redacted desired-vs-observed
// divergence. Raw values are deliberately excluded because controller models
// can contain credentials and security-policy data.
type NetworkControllerDriftEntry struct {
	// Section is the managed Network as Code section.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Section string `json:"section"`

	// Resource is an adapter-normalized, non-secret resource identity.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Resource string `json:"resource"`

	// Path identifies the divergent field without recording its value.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Path string `json:"path,omitempty"`

	// Change describes the reconciliation action implied by the drift.
	// +kubebuilder:validation:Required
	Change NetworkControllerDriftChange `json:"change"`

	// Detected records when the drift was first observed.
	// +optional
	Detected *metav1.Time `json:"detected,omitempty"`

	// Message is a bounded, sanitized explanation.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkControllerConfigStatus is the bounded observed state of
// controller-centric intent. It contains no raw desired or observed values.
type NetworkControllerConfigStatus struct {
	// Phase is the coarse reconciliation state.
	// +optional
	Phase NetworkControllerConfigPhase `json:"phase,omitempty"`

	// ObservedGeneration is the metadata generation most recently processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a SHA-256 over canonical, secret-redacted intent. It
	// never incorporates raw Secret values.
	// +kubebuilder:validation:Pattern=`^sha256:[0-9a-f]{64}$`
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastObservedHash is a SHA-256 over canonical, redacted observed state.
	// +kubebuilder:validation:Pattern=`^sha256:[0-9a-f]{64}$`
	// +optional
	LastObservedHash string `json:"lastObservedHash,omitempty"`

	// LastAppliedTime records the most recent verified apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// LastControllerCheck records the most recent authoritative state read.
	// +optional
	LastControllerCheck *metav1.Time `json:"lastControllerCheck,omitempty"`

	// ControllerAPIVersion is the API version used by the last reconcile.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ControllerAPIVersion string `json:"controllerAPIVersion,omitempty"`

	// LastAppliedSourceRevision records the Network as Code source revision
	// corresponding to LastAppliedHash.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	LastAppliedSourceRevision string `json:"lastAppliedSourceRevision,omitempty"`

	// SectionStatus reports each managed section's current state.
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	// +optional
	SectionStatus []NetworkControllerSectionStatus `json:"sectionStatus,omitempty"`

	// Tasks contains the bounded set of tasks required to resume the current
	// reconciliation after restart.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=id
	// +optional
	Tasks []NetworkControllerTaskStatus `json:"tasks,omitempty"`

	// Drift is a bounded and redacted divergence summary.
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Drift []NetworkControllerDriftEntry `json:"drift,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +kubebuilder:validation:MaxItems=32
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// NetworkControllerConfig declares controller-centric Network as Code intent.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=nccfg
// +kubebuilder:printcolumn:name="Controller",type=string,JSONPath=`.spec.controllerRef.name`
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=`.spec.scope`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NetworkControllerConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkControllerConfigSpec   `json:"spec"`
	Status NetworkControllerConfigStatus `json:"status,omitempty"`
}

// NetworkControllerConfigList contains a list of NetworkControllerConfig
// objects.
//
// +kubebuilder:object:root=true
type NetworkControllerConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkControllerConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkControllerConfig{}, &NetworkControllerConfigList{})
}
