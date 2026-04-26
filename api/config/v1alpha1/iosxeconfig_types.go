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
)

// IOSXEConfigSpec declares the desired IOS-XE configuration for a single
// device. The config driver running inside cisco-vk run merges this spec
// with any matching defaults, device groups, and referenced templates to
// produce the resolved intent that is reconciled against the device.
type IOSXEConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// IOSXEConfigTemplateSpec holds every per-device knob that is
	// also valid inside an IOSXEConfigBundle template. It's embedded
	// inline so existing call sites that read e.g. spec.ManagedFamilies
	// keep working unchanged. Wave 3B (external-review Finding #10):
	// hoisting the shared fields lets the bundle's template field be
	// typed as IOSXEConfigTemplateSpec — which doesn't carry DeviceRef
	// — so a selector-based bundle manifest no longer has to write a
	// dummy deviceRef just to clear admission.
	IOSXEConfigTemplateSpec `json:",inline"`
}

// IOSXEConfigTemplateSpec is the per-device configuration shape with
// DeviceRef removed. Used in two places:
//
//   - Embedded into IOSXEConfigSpec, where the operator also writes
//     a deviceRef (the standalone CR shape).
//   - Used directly as IOSXEConfigBundle.spec.template, where the
//     controller fills DeviceRef per device during fan-out.
//
// Splitting the struct (rather than relaxing IOSXEConfigSpec.DeviceRef
// to optional) keeps the standalone CR's required-field guarantee at
// the schema level, while letting the bundle template skip the field
// without needing CEL or schemaless escape hatches.
type IOSXEConfigTemplateSpec struct {
	// DeviceGroups names the IOSXEDeviceGroupConfig CRs whose configuration
	// is merged into the resolved intent before this CR's source.
	// Merge order follows netascode semantics: defaults → device groups →
	// interface groups → templates → per-device, rightmost wins.
	// +optional
	DeviceGroups []string `json:"deviceGroups,omitempty"`

	// InterfaceGroups names the IOSXEInterfaceGroupConfig CRs whose
	// per-interface configuration is expanded and merged into the
	// resolved intent. Each named group must be in the same namespace
	// as the CR. Matches netascode's `interface_groups[]`.
	// +optional
	InterfaceGroups []string `json:"interfaceGroups,omitempty"`

	// TemplateRefs names IOSXETemplate CRs to expand and merge before the
	// per-device source.
	// +optional
	TemplateRefs []TemplateRef `json:"templateRefs,omitempty"`

	// ManagedFamilies is the closed list of netascode families this CR owns.
	// A family outside this list is not written by this CR even when the
	// merged intent contains values for it, so config can be adopted family
	// by family during a cutover.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies"`

	// Source carries the per-device netascode configuration payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// Transactional requests a candidate-datastore + commit apply when the
	// transport supports it (NETCONF). Ignored with a warning on RESTCONF,
	// which has no candidate datastore; Phase-2 lifts this limitation.
	// +kubebuilder:default=false
	// +optional
	Transactional bool `json:"transactional,omitempty"`

	// WriteStartup copies running-config to startup-config after a successful
	// apply. Off by default to keep reconciles cheap; turn on for devices
	// expected to reboot without a config-save orchestrator.
	// +kubebuilder:default=false
	// +optional
	WriteStartup bool `json:"writeStartup,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-fetches and
	// compares observed state to intent when the spec is otherwise quiescent.
	// Parsed as a Go duration; minimum 30s to avoid device hammering.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls what happens when drift is found. See DriftPolicy.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// PruneOnRelinquish, when true, makes this CR AUTHORITATIVE over
	// every entry in every still-managed family on every reconcile:
	// the engine emits VerbDelete ops for any device-side entry that
	// is not in the resolved intent for those families. The flag's
	// effect is continuous, not just at relinquishment — operators
	// who add entries out-of-band in the same families will see
	// them deleted on the next reconcile.
	//
	// Wave 7A.4 (external-review-next-actions Finding #4): the
	// docstring previously said "families *removed from
	// ManagedFamilies*" — that was the original design intent but
	// the engine implementation treats the flag as continuous
	// authoritative pruning. The CiscoDevice controller's
	// configPrereqs CR therefore sets this flag only during
	// teardown, never on steady-state, so day-0 prereqs apply is
	// additive. User-authored IOSXEConfig CRs may set it
	// explicitly to opt into authoritative pruning.
	//
	// A future v1 cut may rename this to authoritativePrune (or
	// split it from a relinquish-only flag) — tracked in
	// crd-v1-promotion-plan.md.
	//
	// +kubebuilder:default=false
	// +optional
	PruneOnRelinquish bool `json:"pruneOnRelinquish,omitempty"`

	// TargetYangVersion pins the IOS-XE YANG release the writers
	// should compile their managed-leaf set against. When set, the
	// resolver validates the value is in `schema/yang-versions.yaml`
	// and the engine records it on `status.sourceYangVersion` after
	// every successful apply so an operator can correlate device
	// state with the release that drove it.
	//
	// Empty (the common case) means the driver picks the default
	// release. Multi-release writer sets are not yet shipped — every
	// release currently maps to the same writer set — but the field
	// is here so operators don't need a schema migration when they
	// do.
	// +optional
	TargetYangVersion string `json:"targetYangVersion,omitempty"`

	// SecretRefs lets the resolver merge sensitive configuration
	// (BGP MD5 keys, IPSec PSKs, SNMPv3 auth/priv passphrases,
	// enable secrets, RADIUS shared keys) into the resolved intent
	// from Kubernetes Secrets, so secret material never lives in a
	// ConfigMap or git-tracked YAML.
	//
	// Each entry names a family and a Secret-key whose value is a
	// YAML/JSON snippet that deep-merges into the family's intent
	// block. Snippets are merged after defaults, device groups,
	// interface groups, templates, and the per-device source —
	// secret material wins on overlap so a placeholder value in
	// the source can never leak past resolution.
	//
	// The cisco-vk pod's ServiceAccount must have get/watch
	// permission on the referenced Secrets in their namespace; the
	// shipped Helm RBAC already covers this for the pod's own
	// namespace.
	// +optional
	SecretRefs []FamilySecretRef `json:"secretRefs,omitempty"`
}

// FamilySecretRef binds a Kubernetes Secret to a family's intent
// block. The named Secret is read from the same namespace as the
// IOSXEConfig CR; cross-namespace references are deliberately
// disallowed to keep RBAC trivial. Shape mirrors ConfigMapKeyRef
// so operators have one mental model for both kinds of external
// payload.
type FamilySecretRef struct {
	// Family is the netascode family the snippet merges into. Must
	// be one of the names in ManagedFamilies; resolution fails if
	// it isn't, so a typo doesn't quietly produce a no-op.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Family string `json:"family"`

	// Name of the Secret in the same namespace as the CR.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the entry inside Secret.Data whose value is parsed as
	// a YAML/JSON snippet and merged into the family's intent. The
	// snippet must shape itself to fit under the family root —
	// `{"neighbors":[...]}` for bgp, `{"communities":[...]}` for
	// snmp_server — exactly as it would appear in a per-device
	// source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// IOSXEConfigStatus reports reconciliation state to users and GitOps agents.
type IOSXEConfigStatus struct {
	// Phase is a coarse state summary: Pending, Validating, Planning,
	// Applying, Verifying, InSync, Drifted, Failed, Paused, or
	// LeaseBlocked. LeaseBlocked is the transient state when a
	// foreign holder owns one or more managed-family leases and this
	// CR's reconcile cannot run them yet; the controller requeues at
	// a sub-TTL interval until the contention clears.
	// +kubebuilder:validation:Enum=Pending;Validating;Planning;Applying;Verifying;InSync;Drifted;Failed;Paused;LeaseBlocked
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the driver last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a stable SHA-256 over the canonicalised resolved
	// intent. The driver short-circuits reconcile when this matches the
	// current intent and the device is known-fresh.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastAppliedTime records the most recent successful apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// LastDeviceCheck records the most recent reconcile tick that
	// actually fetched device state and ran the diff (i.e., the
	// hash short-circuit was bypassed). Used to honour
	// spec.driftDetectInterval — the next reconcile fetches when
	// LastDeviceCheck + interval has elapsed, even if intent and
	// generation are unchanged.
	// +optional
	LastDeviceCheck *metav1.Time `json:"lastDeviceCheck,omitempty"`

	// SourceYangVersion is the YANG release the driver used to translate
	// the intent on the last successful apply.
	// +optional
	SourceYangVersion string `json:"sourceYangVersion,omitempty"`

	// FamilyStatus reports per-family state for each family in ManagedFamilies.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists the currently known divergences between intent and device.
	// Capped at 50 entries; additional drift is reflected in the
	// cisco_vk_config_drift_entries_truncated_total counter only.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Drift []DriftEntry `json:"drift,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape. The
	// driver maintains Ready, Reconciling, and a Healthy-<family> entry
	// for each managed family.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// IOSXEConfig is the per-device desired-configuration CR. A single device
// may be targeted by at most one IOSXEConfig for any given family; the
// driver arbitrates via a per-family lease and surfaces conflicts in status.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxecfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigSpec   `json:"spec"`
	Status IOSXEConfigStatus `json:"status,omitempty"`
}

// IOSXEConfigList is the list type for IOSXEConfig.
//
// +kubebuilder:object:root=true
type IOSXEConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfig{}, &IOSXEConfigList{})
}
