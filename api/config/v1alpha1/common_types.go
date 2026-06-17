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

// DriftPolicy controls how the config driver responds when the device's
// observed configuration no longer matches the resolved intent.
//
// +kubebuilder:validation:Enum=revert;report;pause
type DriftPolicy string

const (
	// DriftPolicyRevert rewrites the managed families back to the declared
	// intent. This is the default and matches continuous-reconciliation
	// GitOps semantics.
	DriftPolicyRevert DriftPolicy = "revert"

	// DriftPolicyReport records drift in status without writing. Used during
	// parallel-run cutovers from an existing pipeline (see RFC §11).
	DriftPolicyReport DriftPolicy = "report"

	// DriftPolicyPause disables reconciliation until the annotation is cleared.
	// Intended as a break-glass for live CLI troubleshooting.
	DriftPolicyPause DriftPolicy = "pause"
)

// DeviceRef names a CiscoDevice in the same namespace as the referring CR.
type DeviceRef struct {
	// Name of the CiscoDevice the CR targets.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ConfigurationSource provides the netascode-shaped configuration body.
// Exactly one of the fields must be set; the driver rejects a CR that sets
// neither or both.
type ConfigurationSource struct {
	// Inline is an inline netascode configuration. The payload is the content
	// of an iosxe.devices[].configuration block; the field is schemaless so
	// family shape evolves with the YANG release, not the CRD.
	//
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Inline *runtime.RawExtension `json:"inline,omitempty"`

	// ConfigMapRef references a ConfigMap in the same namespace whose keyed
	// entry holds a netascode YAML document. Used for large configurations
	// that would otherwise bloat the CR past etcd's per-object limit.
	// +optional
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// NetAsCodeModelFormat names the external intent model carried by a config CR.
// CVK accepts Cisco Network as Code payloads for IOS-XE and NX-OS in this
// runtime slice.
//
// +kubebuilder:validation:Enum=netascode-iosxe;netascode-nxos
type NetAsCodeModelFormat string

const (
	// NetAsCodeModelFormatIOSXE is the canonical Cisco IOS-XE NetAsCode
	// data model used by the terraform-iosxe-nac-iosxe module.
	NetAsCodeModelFormatIOSXE NetAsCodeModelFormat = "netascode-iosxe"

	// NetAsCodeModelFormatNXOS is the canonical Cisco NX-OS NetAsCode
	// data model used by the NX-OS network-as-code module family.
	NetAsCodeModelFormatNXOS NetAsCodeModelFormat = "netascode-nxos"
)

// NetAsCodeModelSource records where a NetAsCode payload came from. It is
// audit metadata, not desired device state; reconcilers still consume
// spec.source as the source of truth.
type NetAsCodeModelSource struct {
	// Format identifies the model dialect.
	// +kubebuilder:validation:Required
	Format NetAsCodeModelFormat `json:"format"`

	// ModelVersion is the upstream NetAsCode data-model version or module
	// version when the source pipeline can provide one.
	// +optional
	ModelVersion string `json:"modelVersion,omitempty"`

	// Resolved is true when the payload has already had defaults, templates,
	// device groups, and inheritance expanded by the source NetAsCode toolchain.
	// Production migrations should import resolved payloads so CVK only owns
	// reconciliation, not Terraform's model-expansion semantics.
	// +kubebuilder:default=true
	Resolved bool `json:"resolved"`

	// Exporter names the tool that produced the payload, such as
	// terraform-iosxe-nac-iosxe write_model_file or cvk-netascode-migrate.
	// +optional
	Exporter string `json:"exporter,omitempty"`

	// SourceRevision identifies the customer Git commit, Terraform plan ID,
	// or other immutable source revision used for the import.
	// +optional
	SourceRevision string `json:"sourceRevision,omitempty"`
}

// ConfigMapKeyRef selects one key from a namespaced ConfigMap.
type ConfigMapKeyRef struct {
	// Name of the ConfigMap (same namespace as the CR).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key within the ConfigMap whose value is the netascode YAML body.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// CommonConfigSpec is the shared desired-state contract for platform
// configuration CRDs whose intent is expressed as named NetAsCode families.
// Platform-specific CRDs reuse it as their concrete spec shape and document
// their supported family set at the concrete type.
type CommonConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// ManagedFamilies is the closed list of NetAsCode families this CR owns.
	// A family outside this list is not written by this CR even when the
	// resolved intent contains values for it.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies"`

	// Source carries the per-device or per-controller NetAsCode
	// configuration payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// ModelSource records the external NetAsCode model/export that produced
	// Source. It is deliberately metadata-only: CVK reconciles the canonical
	// payload in Source.
	// +optional
	ModelSource *NetAsCodeModelSource `json:"modelSource,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-fetches and
	// compares observed state to intent when the spec is otherwise quiescent.
	// Parsed as a Go duration; minimum 30s to avoid device/API hammering.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls what happens when drift is found.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// Transactional requests a candidate-datastore + commit apply when the
	// selected platform transport supports transactions. Non-transactional
	// transports apply directly to running config and surface a confirmed-
	// commit fallback when confirmTimeoutSeconds is also set.
	// +kubebuilder:default=false
	// +optional
	Transactional bool `json:"transactional,omitempty"`

	// WriteStartup persists running configuration to startup configuration
	// after a successful apply when the selected platform transport supports
	// it. Unsupported transports simply leave the apply result green.
	// +kubebuilder:default=false
	// +optional
	WriteStartup bool `json:"writeStartup,omitempty"`

	// ConfirmTimeoutSeconds enables confirmed-commit auto-revert semantics on
	// transports that support them. When unavailable, the engine falls back to
	// the platform's normal commit behavior and records the fallback reason.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=300
	// +optional
	ConfirmTimeoutSeconds int32 `json:"confirmTimeoutSeconds,omitempty"`

	// AtomicReplace treats the resolved intent as authoritative for this CR's
	// managed families when paired with a transactional transport. It reuses
	// the shared engine's replace/prune semantics.
	// +kubebuilder:default=false
	// +optional
	AtomicReplace bool `json:"atomicReplace,omitempty"`

	// PruneOnRelinquish asks capable family writers to delete device-side
	// entries that are absent from this CR's resolved intent.
	// +kubebuilder:default=false
	// +optional
	PruneOnRelinquish bool `json:"pruneOnRelinquish,omitempty"`

	// TargetYangVersion pins the platform model or software release used by
	// the writer registry. Empty lets the platform default decide.
	// +optional
	TargetYangVersion string `json:"targetYangVersion,omitempty"`

	// SecretRefs lets the resolver merge sensitive configuration into the
	// resolved intent from Kubernetes Secrets, so secret material never needs
	// to live in a ConfigMap or git-tracked YAML.
	// +optional
	SecretRefs []FamilySecretRef `json:"secretRefs,omitempty"`
}

// CommonConfigStatus is the shared status contract for platform configuration
// CRDs that use the common NetAsCode family reconciliation flow.
type CommonConfigStatus struct {
	// Phase is a coarse state summary: Pending, Validating, Planning,
	// Applying, Verifying, InSync, Drifted, Failed, Paused, or LeaseBlocked.
	// +kubebuilder:validation:Enum=Pending;Validating;Planning;Applying;Verifying;InSync;Drifted;Failed;Paused;LeaseBlocked
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the driver last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a stable SHA-256 over the canonicalised resolved
	// intent.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastAppliedTime records the most recent successful apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// LastDeviceCheck records the most recent reconcile tick that fetched
	// observed state from the device or controller.
	// +optional
	LastDeviceCheck *metav1.Time `json:"lastDeviceCheck,omitempty"`

	// SourceYangVersion records the platform release/model version used by the
	// writer registry on the last successful apply.
	// +optional
	SourceYangVersion string `json:"sourceYangVersion,omitempty"`

	// PlannedOps is the number of transport operations produced by the last
	// reconcile plan.
	// +optional
	PlannedOps int32 `json:"plannedOps,omitempty"`

	// AppliedOps is the number of transport operations handed to writers by
	// the last reconcile.
	// +optional
	AppliedOps int32 `json:"appliedOps,omitempty"`

	// PostApplyObservedHash is a stable hash over the observed state fetched
	// during verify after an apply.
	// +optional
	PostApplyObservedHash string `json:"postApplyObservedHash,omitempty"`

	// VerifiedFamilies lists families that completed a post-apply verify pass.
	// +optional
	// +listType=set
	VerifiedFamilies []string `json:"verifiedFamilies,omitempty"`

	// FamilyStatus reports per-family state for each family in
	// ManagedFamilies.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists the currently known divergences between intent and observed
	// state.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Drift []DriftEntry `json:"drift,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// TemplateRef refers to an IOSXETemplate and supplies parameter values used
// during expansion.
type TemplateRef struct {
	// Name of the IOSXETemplate (same namespace as the referring CR).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Values overrides the template's parameter defaults. Shape is validated
	// against the template's declared parameters at expansion time.
	//
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Values *runtime.RawExtension `json:"values,omitempty"`
}

// FamilyStatus reports per-family reconciliation state.
type FamilyStatus struct {
	// Name of the netascode family (e.g. "vlan", "interface_ethernet").
	Name string `json:"name"`

	// State is one of Pending, InSync, Drifted, ApplyError, Skipped,
	// Unsupported. The taxonomy is closed; unknown values are rejected.
	// +kubebuilder:validation:Enum=Pending;InSync;Drifted;ApplyError;Skipped;Unsupported
	State string `json:"state"`

	// Entries counts the number of keyed items the driver observed for this
	// family on the device (e.g. number of VLANs). Zero for singleton families.
	// +optional
	Entries int32 `json:"entries,omitempty"`

	// OpCount is the number of writer ops produced by this tick's diff —
	// the metric verify.sh release-blocker assertions consult to confirm
	// an apply produced device-side work. Zero on InSync no-op ticks
	// after a converged reconcile; positive after a tick that wrote.
	// +optional
	OpCount int32 `json:"opCount,omitempty"`

	// Message is a short human-readable description. Empty when state is InSync.
	// +optional
	Message string `json:"message,omitempty"`
}

// DriftEntry records a single observed-vs-desired divergence. Paths are YANG
// xpaths so the entry is unambiguous regardless of the family's shape.
type DriftEntry struct {
	// Family the drift belongs to.
	Family string `json:"family"`

	// Path is the YANG xpath of the leaf or list key that differs.
	Path string `json:"path"`

	// Desired is the declared value (optional; omitted for deletes).
	// +optional
	Desired string `json:"desired,omitempty"`

	// Observed is the value currently on the device (optional; omitted for
	// leaves absent from the device).
	// +optional
	Observed string `json:"observed,omitempty"`

	// Detected is the time the driver first observed this specific entry.
	// +optional
	Detected metav1.Time `json:"detected,omitempty"`
}
