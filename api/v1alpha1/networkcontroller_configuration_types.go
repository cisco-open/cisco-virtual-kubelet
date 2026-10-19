// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NetworkControllerConfigurationStatus survives deletion or replacement of a
// referring config. Ownership is not an expiring lease: an uncertain remote
// operation must not become another writer's permission to submit.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.metadataHandoff) || has(self.metadataHandoff) || has(oldSelf.metadataHandoff.releasedAt)",message="unreleased metadata custody cannot be discarded"
type NetworkControllerConfigurationStatus struct {
	// MetadataHandoff retains manager/worker cleanup custody independently of
	// successful effects. An outstanding handoff holds new preparations.
	// +optional
	MetadataHandoff *NetworkControllerConfigurationMetadataHandoff `json:"metadataHandoff,omitempty"`

	// GroupCancellations retain cleanup evidence after a cancelled operation
	// releases its journal slot. They are not successful application receipts.
	// Entries may only be retired after their member reservations are retired.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=operationID
	// +optional
	GroupCancellations []NetworkControllerConfigurationGroupCancellation `json:"groupCancellations,omitempty"`

	// ActionApplications retain verified imperative executions after releasing
	// the operation slot. They prevent an unchanged intent from being replayed.
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=section
	// +listMapKey=resource
	// +optional
	ActionApplications []NetworkControllerConfigurationActionApplication `json:"actionApplications,omitempty"`

	// Owners records exclusive ownership of global model sections. Narrower
	// scopes require a separate adapter contract and are not represented here.
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=section
	// +optional
	Owners []NetworkControllerSectionOwner `json:"owners,omitempty"`

	// Operation serializes configuration writes for this controller identity.
	// No payload, authentication material or digest of secrets belongs here.
	// +optional
	Operation *NetworkControllerConfigurationOperation `json:"operation,omitempty"`
	// CredentialApplications records successful write-only version application
	// to an observed resource identity. It is not credential read-back or an
	// authentication test. Receipts survive operation-slot release.
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=section
	// +listMapKey=resource
	// +optional
	CredentialApplications []NetworkControllerConfigurationCredentialApplication `json:"credentialApplications,omitempty"`
}

// NetworkControllerConfigurationActionApplication records one verified action
// using the same immutable source/target binding as its write-ahead operation.
type NetworkControllerConfigurationActionApplication struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Section string `json:"section"`
	// Resource is the stable action key, excluding transient baseline evidence.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Resource  string                                  `json:"resource"`
	Operation NetworkControllerConfigurationOperation `json:"operation"`
	// VerifiedAt records the first independently verified receipt persistence.
	// Optional for legacy receipts; absence cannot establish dependency pacing.
	// +optional
	VerifiedAt *metav1.Time `json:"verifiedAt,omitempty"`
	// EffectReceipts are bounded adapter-defined proofs for related objects
	// changed by this same native operation. They never represent extra native
	// operations. Adapters must bind the complete set in the producer/result
	// contract and refuse settlement if that set is missing or altered.
	// Only public identities and keyed comparison evidence belong here, never
	// native payloads or credential values. Legacy receipts leave this nil.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	EffectReceipts []string `json:"effectReceipts,omitempty"`
}

type NetworkControllerConfigurationCredentialApplication struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Section string `json:"section"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Resource string `json:"resource"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ConfigUID string `json:"configUID"`
	// TargetID is the UUID independently observed by the resource executor.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	TargetID string `json:"targetID"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	OperationID string `json:"operationID"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	// +listType=map
	// +listMapKey=path
	Versions []NetworkControllerConfigurationCredentialVersion `json:"versions"`
	// SSIDCredentialHistory retains per-priority version high-water marks,
	// including retired priorities. It never contains credential values.
	// +kubebuilder:validation:MaxItems=5
	// +listType=map
	// +listMapKey=priority
	// +optional
	SSIDCredentialHistory []NetworkControllerConfigurationSSIDCredentialHistory `json:"ssidCredentialHistory,omitempty"`
}

type NetworkControllerSectionOwner struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Section string `json:"section"`
	// ConfigName is in the NetworkController's namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ConfigName string `json:"configName"`
	// ConfigUID prevents a recreated config from inheriting ownership.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ConfigUID string `json:"configUID"`
}

// NetworkControllerConfigurationOperation is a write-ahead record. Submitting
// is durable BEFORE the HTTP call; seeing it after a restart is not permission
// to retry. Accepted is not success: authoritative read-back must verify it.
// +kubebuilder:validation:XValidation:rule="!has(self.physicalGroup) || !has(self.targetSerial) || size(self.targetSerial) == 0",message="group physical scope cannot also declare a single target serial"
// +kubebuilder:validation:XValidation:rule="!has(self.cancellationRequestedAt) || (has(self.physicalGroup) && self.phase == 'Cancelling' && !has(self.submittedAt) && (!has(self.taskID) || size(self.taskID) == 0))",message="group cancellation must precede submission"
// +kubebuilder:validation:XValidation:rule="self.phase != 'Cancelling' || has(self.cancellationRequestedAt)",message="cancelling requires a durable cancellation time"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.cancellationRequestedAt) || (has(self.cancellationRequestedAt) && self.cancellationRequestedAt == oldSelf.cancellationRequestedAt && self.phase == 'Cancelling')",message="group cancellation cannot be reversed"
type NetworkControllerConfigurationOperation struct {
	// DependencyVersions retains nonsecret native version bindings. Admission
	// is validated independently for supported physical operation kinds.
	// +optional
	DependencyVersions *NetworkControllerConfigurationDependencyVersions `json:"dependencyVersions,omitempty"`

	// PhysicalGroup binds fabric-wide physical scope separately from TargetID,
	// which remains the native logical resource ID. Existing v1 physical paths
	// must reject this field; the group protocol is not yet runtime-enabled.
	// +optional
	PhysicalGroup *NetworkControllerConfigurationGroupIdentity `json:"physicalGroup,omitempty"`

	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	// +kubebuilder:validation:MaxLength=32
	ID string `json:"id"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ConfigName string `json:"configName"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ConfigUID string `json:"configUID"`
	// +kubebuilder:validation:Minimum=1
	ConfigGeneration int64 `json:"configGeneration"`
	// ControllerGeneration binds connection and credential policy, not just
	// the immutable endpoint. Changed policy cannot resume submission.
	// +kubebuilder:validation:Minimum=1
	ControllerGeneration int64 `json:"controllerGeneration"`
	// SourceUID and SourceVersion bind a ConfigMap snapshot; both are empty
	// for inline intent, which is bound by ConfigGeneration.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	SourceUID string `json:"sourceUID,omitempty"`
	// +kubebuilder:validation:MaxLength=128
	// +optional
	SourceVersion string `json:"sourceVersion,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Section string `json:"section"`
	// Resource is a non-secret adapter identity, never configuration content.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Resource string `json:"resource"`
	// TargetID pins an existing resource across task completion and restart.
	// Empty denotes creation, whose returned identity must be observed.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	TargetID string `json:"targetID,omitempty"`
	// TargetSerial binds a physical device independently of the controller's
	// inventory UUID. Device executors must corroborate it during recovery.
	// Empty is reserved for resources that do not target a physical device.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	TargetSerial string `json:"targetSerial,omitempty"`
	// ParentID pins the resolved parent of a hierarchical resource.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ParentID string `json:"parentID,omitempty"`
	// +kubebuilder:validation:Enum=Create;Update;Delete
	Action string `json:"action"`
	// +kubebuilder:validation:Enum=Prepared;Submitting;Accepted;Verifying;Verified;Unknown;Cancelling
	Phase string `json:"phase"`
	// PreparedAt is the persisted start of this exact operation's preparation.
	// Adapters may use it for dependency settling when no mutation receipt exists.
	// It never grants submission authority or proves native convergence.
	// +optional
	PreparedAt *metav1.Time `json:"preparedAt,omitempty"`
	// CancellationRequestedAt fences a Prepared group against submission before
	// member cleanup. It never proves lease release or manager settlement.
	// +optional
	CancellationRequestedAt *metav1.Time `json:"cancellationRequestedAt,omitempty"`
	// SubmittedAt is persisted with the single submission permit. Recovery
	// uses it to reject controller receipts predating this operation.
	// +optional
	SubmittedAt *metav1.Time `json:"submittedAt,omitempty"`
	// TaskID is the controller's receipt. Task failure alone does not prove
	// absence of side effects and therefore cannot release the journal.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	TaskID string `json:"taskID,omitempty"`
	// CreatedResourceID is the nonsecret UUID returned by a synchronous create.
	// It is response evidence, not a task ID or physical reservation identity.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	CreatedResourceID string `json:"createdResourceID,omitempty"`
	// CredentialVersions binds explicit model write-only version counters to
	// the authorized source references submitted by this operation. These are
	// not secret values or hashes and do not themselves prove application.
	// +kubebuilder:validation:MaxItems=128
	// +listType=map
	// +listMapKey=path
	// +optional
	CredentialVersions []NetworkControllerConfigurationCredentialVersion `json:"credentialVersions,omitempty"`
	// SSIDCredentialHistory retains per-priority version high-water marks,
	// including retired priorities. It never contains credential values.
	// +kubebuilder:validation:MaxItems=5
	// +listType=map
	// +listMapKey=priority
	// +optional
	SSIDCredentialHistory []NetworkControllerConfigurationSSIDCredentialHistory `json:"ssidCredentialHistory,omitempty"`
}

// NetworkControllerConfigurationCredentialVersion identifies a requested
// write-only credential rotation without persisting credential material.
type NetworkControllerConfigurationCredentialVersion struct {
	// Key is an optional non-secret semantic identity within the resource.
	// It binds list credentials to stable model keys rather than array indices.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Key string `json:"key,omitempty"`
	// Path is relative to the operation's managed section.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^/.*`
	Path string `json:"path"`
	// Version is the explicit positive NetAsCode write-only version counter.
	// It is never inferred from credential bytes or filesystem metadata.
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	SourceAlias string `json:"sourceAlias"`
	// SecretName and SecretKey pin the administrator's alias mapping. The
	// referenced Secret is in the NetworkController's namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SecretName string `json:"secretName"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SecretKey string `json:"secretKey"`
}

// NetworkControllerConfigurationSSIDCredentialHistory is non-secret retained
// history for one Multi-PSK priority on an owned native SSID.
type NetworkControllerConfigurationSSIDCredentialHistory struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4
	Priority int64 `json:"priority"`
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`
}
