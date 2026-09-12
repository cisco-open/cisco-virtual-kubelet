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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:validation:Enum=XE;XR;NXOS;OPENCONFIG;FAKE
type DeviceDriver string

const (
	DeviceDriverXE         DeviceDriver = "XE"
	DeviceDriverXR         DeviceDriver = "XR"
	DeviceDriverNXOS       DeviceDriver = "NXOS"
	DeviceDriverOPENCONFIG DeviceDriver = "OPENCONFIG"
	DeviceDriverFAKE       DeviceDriver = "FAKE"
)

const (
	// CiscoDeviceConditionAggregatorOwning is set while the controller is
	// transferring config-reconcile ownership to the aggregator. Per-device
	// Pods may still be terminating. Aggregator MUST NOT act on a device whose
	// AggregatorOwned condition is still false.
	CiscoDeviceConditionAggregatorOwning = "AggregatorOwning"
	// CiscoDeviceConditionAggregatorOwned is set by the CiscoDevice controller
	// once aggregator topology owns a configdriver-backed device.
	CiscoDeviceConditionAggregatorOwned = "AggregatorOwned"
	// CiscoDeviceConditionAggregatorTopologyStuck is set when a topology shift
	// to aggregator ownership cannot finish because old per-device Pods remain.
	CiscoDeviceConditionAggregatorTopologyStuck = "AggregatorTopologyStuck"
	// CiscoDeviceConditionPrereqTeardownObserved records that the controller
	// has seen the owned prereq IOSXEConfig enter deletion.
	CiscoDeviceConditionPrereqTeardownObserved = "PrereqTeardownObserved"
	// CiscoDeviceConditionGNOIConfigurationReady reports local gNOI Secret
	// validation. It does not assert device reachability or OS service readiness.
	CiscoDeviceConditionGNOIConfigurationReady = "GNOIConfigurationReady"
	// CiscoDeviceConditionNodeIdentityReady reports whether the manager has
	// reserved and UID-bound the cluster-scoped Node named by spec.nodeName (or
	// the controller-resolved compatibility default).
	CiscoDeviceConditionNodeIdentityReady = "NodeIdentityReady"
	// CiscoDeviceConditionTopologyReady reports that the manager-owned,
	// allowlisted topology projection is complete on the identity-bound Node.
	CiscoDeviceConditionTopologyReady = "TopologyReady"
	// CiscoDeviceConditionTopologyIncomplete reports that an administrator-
	// required topology label is missing or invalid.
	CiscoDeviceConditionTopologyIncomplete = "TopologyIncomplete"
	// CiscoDeviceConditionTopologyConflict reports conflicting legacy and
	// metadata topology intent or a foreign Node-name reservation.
	CiscoDeviceConditionTopologyConflict = "TopologyConflict"
	// CiscoDeviceConditionMaintenanceReady reports whether the manager-owned
	// maintenance request/Node-guard acknowledgement is coherent.
	CiscoDeviceConditionMaintenanceReady = "MaintenanceReady"
	// CiscoDeviceConditionLegacyHandoffReady reports the durable transfer from
	// the managed status-only writer to the isolated per-device legacy writer.
	// It never authorizes use of the release-wide shared worker identity.
	CiscoDeviceConditionLegacyHandoffReady = "LegacyHandoffReady"
)

// CiscoDevice is the Schema for the ciscodevices API.
// It represents a single Cisco device managed by the virtual kubelet operator.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cvk
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.nodeIdentity) || (has(self.spec.physicalIdentity) && self.status.nodeIdentity.physicalIdentity == self.spec.physicalIdentity.lowerAscii())",message="status.nodeIdentity.physicalIdentity must equal the canonical declared spec.physicalIdentity"
type CiscoDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeviceSpec   `json:"spec"`
	Status DeviceStatus `json:"status,omitempty"`
}

// CiscoDeviceList contains a list of CiscoDevice.
//
// +kubebuilder:object:root=true
type CiscoDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CiscoDevice `json:"items"`
}

// DeviceSpec defines the desired state of a Cisco device.
// Shared fields are common to all drivers; driver-specific configuration lives
// under the corresponding driver section (XE, XR, etc.).
// +kubebuilder:validation:XValidation:rule="!has(self.xe) || !has(self.xe.gnoi) || !has(self.xe.gnoi.certificateProvisioning) || self.driver == 'XE'",message="gNOI certificate provisioning is supported only for driver XE"
// +kubebuilder:validation:XValidation:rule="!has(self.xe) || !has(self.xe.gnoi) || !has(self.xe.gnoi.certificateProvisioning) || (has(self.gnoi) && has(self.gnoi.transportSecurity) && self.gnoi.transportSecurity == 'tls')",message="spec.xe.gnoi.certificateProvisioning requires spec.gnoi.transportSecurity to be tls"
// +kubebuilder:validation:XValidation:rule="!has(self.gnoi) || !has(self.gnoi.tls) || (has(self.gnoi.transportSecurity) && self.gnoi.transportSecurity == 'tls')",message="spec.gnoi.tls requires spec.gnoi.transportSecurity to be tls"
// +kubebuilder:validation:XValidation:rule="!has(self.gnoi) || !has(self.gnoi.tls) || !has(self.xe) || !has(self.xe.gnoi) || !has(self.xe.gnoi.certificateProvisioning)",message="spec.gnoi.tls and spec.xe.gnoi.certificateProvisioning cannot both be configured"
// +kubebuilder:validation:XValidation:rule="!has(self.gnoi) || !has(self.gnoi.transportSecurity) || self.gnoi.transportSecurity != 'tls' || has(self.gnoi.tls) || (has(self.xe) && has(self.xe.gnoi) && has(self.xe.gnoi.certificateProvisioning)) || !has(self.tls) || !has(self.tls.insecureSkipVerify) || !self.tls.insecureSkipVerify",message="explicit secure gNOI requires system or verified shared TLS, spec.gnoi.tls, or IOS XE certificate provisioning trust"
// +kubebuilder:validation:XValidation:rule="has(self.nodeName) == has(oldSelf.nodeName) && (!has(self.nodeName) || self.nodeName == oldSelf.nodeName)",message="nodeName is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.physicalIdentity) || (has(self.physicalIdentity) && self.physicalIdentity == oldSelf.physicalIdentity)",message="physicalIdentity is write-once"
type DeviceSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=XE;XR;NXOS;OPENCONFIG;FAKE
	Driver DeviceDriver `json:"driver" mapstructure:"driver"`

	// Address is the management IP or hostname of the device.
	// +kubebuilder:validation:Required
	Address string `json:"address" mapstructure:"address"`

	// NodeName reserves the cluster-scoped Kubernetes Node identity for this
	// namespaced device. It is immutable because renaming would change the
	// scheduler and worker security boundary. When omitted, the controller
	// resolves metadata.name for backward compatibility; CRD defaulting cannot
	// dynamically copy metadata.name. The resolved name and Node UID are
	// recorded in status.nodeIdentity.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	NodeName string `json:"nodeName,omitempty" mapstructure:"nodeName,omitempty"`

	// PhysicalIdentity is an operator-declared stable chassis serial, UUID, or
	// equivalent hardware identity. Managed topology requires it and treats its
	// lowercase canonical form as the rollout admission and deduplication
	// authority. The authenticated worker's live device inventory must agree,
	// but cannot replace this immutable declaration. It remains optional outside
	// managed topology for backward compatibility.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9._:/-]{0,126}[A-Za-z0-9])?$`
	PhysicalIdentity string `json:"physicalIdentity,omitempty" mapstructure:"physicalIdentity,omitempty"`

	// Port for device communication (default: 443 for TLS, 80 otherwise).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port,omitempty" mapstructure:"port"`

	// Username for device authentication.
	// +kubebuilder:validation:Required
	Username string `json:"username" mapstructure:"username"`

	// Password for device authentication.
	// In CRD mode the controller should source this from a Secret reference.
	// +kubebuilder:validation:Optional
	Password string `json:"password,omitempty" mapstructure:"password"`

	// CredentialSecretRef references a Secret containing device credentials.
	// Used by the controller when creating VK deployments from CRDs.
	// +kubebuilder:validation:Optional
	CredentialSecretRef *v1.LocalObjectReference `json:"credentialSecretRef,omitempty"`

	// TLS configuration for device communication.
	// +kubebuilder:validation:Optional
	TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls,omitempty"`

	// GNOI contains opt-in, per-device gNOI settings. Omitting it, or providing
	// an empty block, preserves historical transport and port inference. The
	// selected driver owns authentication policy; secure IOS-XE gNOI derives it
	// from Username and the resolved device password.
	// +kubebuilder:validation:Optional
	GNOI *GNOIConfig `json:"gnoi,omitempty" mapstructure:"gnoi,omitempty"`

	// PodCIDR is the CIDR to use for pod network interfaces when using static IP allocation.
	// +kubebuilder:validation:Optional
	PodCIDR string `json:"podCIDR,omitempty" mapstructure:"podCIDR"`

	// Labels to apply to the virtual kubelet node.
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty" mapstructure:"labels,omitempty"`

	// Taints to apply to the virtual kubelet node.
	// +kubebuilder:validation:Optional
	Taints []v1.Taint `json:"taints,omitempty" mapstructure:"taints,omitempty"`

	// MaxPods is the maximum number of pods the device can host.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=16
	MaxPods int32 `json:"maxPods,omitempty" mapstructure:"maxPods"`

	// Region for node topology.
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty" mapstructure:"region,omitempty"`

	// Zone for node topology.
	// +kubebuilder:validation:Optional
	Zone string `json:"zone,omitempty" mapstructure:"zone,omitempty"`

	// LogLevel sets the logging verbosity for the VK instance.
	// Valid values are: debug, info, warn, error.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=debug;info;warn;error
	// +kubebuilder:default=info
	LogLevel string `json:"logLevel,omitempty" mapstructure:"logLevel"`

	// ResourceLimits defines default and maximum resource allocations.
	// +kubebuilder:validation:Optional
	ResourceLimits ResourceConfig `json:"resourceLimits,omitempty" mapstructure:"resourceLimits"`

	// Worker configures the Kubernetes per-device CVK worker, independently of
	// ResourceLimits for applications hosted on the Cisco device. Omitted fields
	// inherit the manager's worker defaults. Ignored in aggregator topology.
	// +kubebuilder:validation:Optional
	Worker *DeviceWorkerConfig `json:"worker,omitempty"`

	// OTEL holds OpenTelemetry topology export configuration.
	// When enabled, the VK emits OTLP traces representing the device's
	// network topology (neighbors, links) to the configured collector endpoint.
	// +kubebuilder:validation:Optional
	OTEL *OTELConfig `json:"otel,omitempty" mapstructure:"otel,omitempty"`

	// AllowUnsignedApps indicates the device permits unsigned application packages.
	// When true, the reconciler will not fail pods based on a transient
	// iox-pkg-policy-invalid value during the INSTALLING state.
	// When false (default), the reconciler treats iox-pkg-policy-invalid during
	// INSTALLING as a terminal failure only when confirmed by an install
	// notification from the device.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	AllowUnsignedApps bool `json:"allowUnsignedApps,omitempty" mapstructure:"allowUnsignedApps"`

	// Transport selects the device channel used by declarative config
	// drivers. IOS-XE defaults to RESTCONF and also supports NETCONF/gNMI.
	// NX-OS declarative config uses NX-API REST/DME, selected with "rest";
	// because this field has a historical global RESTCONF default, NX-OS treats
	// an omitted/defaulted "restconf" value as REST/DME too. The legacy "nxapi"
	// value remains accepted as an alias for existing manifests. Apphosting
	// operations always use the platform apphosting transport regardless of
	// this field.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=rest;restconf;netconf;gnmi;nxapi
	// +kubebuilder:default=restconf
	Transport string `json:"transport,omitempty" mapstructure:"transport"`

	// ConfigPrereqs declares the network configuration this device requires
	// before pods can be hosted on it, for example a VirtualPortGroup
	// interface, DHCP pool, app egress ACL, or NX-OS apphosting prerequisite
	// family. When set, the controller materializes the platform config CR
	// described by the platform descriptor: IOS-XE keeps the legacy
	// IOSXEConfig prereq family set, while NX-OS and future platforms derive
	// managed families from the source payload unless explicitly supplied.
	//
	// The payload carries the same netascode-shaped YAML as
	// IOSXEConfig.spec.source.inline. Operator-authored IOSXEConfig CRs may
	// coexist with this controller-owned CR as long as they do not claim the
	// same families.
	// +kubebuilder:validation:Optional
	ConfigPrereqs *ConfigPrereqs `json:"configPrereqs,omitempty" mapstructure:"configPrereqs,omitempty"`

	// OpsPolicy declares per-device DeviceOperation gates the controller
	// templates into the per-device VK pod's environment. The CiscoDevice
	// reconciler is the authoritative source — imperative `kubectl set env`
	// edits are reverted on the next reconcile.
	// +kubebuilder:validation:Optional
	OpsPolicy *OpsPolicy `json:"opsPolicy,omitempty" mapstructure:"opsPolicy,omitempty"`

	// --- Driver-specific networking configuration (union) ---
	// Only the section matching Driver should be set.

	// XE holds IOS-XE specific configuration.
	// Required when driver=XE.
	// +kubebuilder:validation:Optional
	XE *XEConfig `json:"xe,omitempty" mapstructure:"xe,omitempty"`

	// XR holds IOS-XR specific networking configuration (future).
	// +kubebuilder:validation:Optional
	// XR *XRConfig `json:"xr,omitempty" mapstructure:"xr,omitempty"`

	// NXOS holds NX-OS specific networking configuration.
	// +kubebuilder:validation:Optional
	NXOS *NXOSConfig `json:"nxos,omitempty" mapstructure:"nxos,omitempty"`
}

// OpsPolicy carries per-device DeviceOperation gates the CiscoDevice
// controller templates into the per-device VK pod's environment. Adding a
// field here means: (a) declare it in this struct, (b) extend
// opsPolicyEnv() in the controller to translate it into the pod env, (c)
// teach the receiving reconciler to read the matching env var.
type OpsPolicy struct {
	// ConfigDiffAllowedNamespaces, when non-empty, restricts the namespaces
	// from which DeviceOperation/ConfigDiff requests targeting this device
	// will run. Requests from other namespaces fail at admission with
	// reason=NamespaceNotAuthorized. The controller renders this list as
	// CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES (comma-separated) on the
	// per-device VK pod. An empty/nil list preserves the unrestricted
	// default for backward compatibility.
	// +kubebuilder:validation:Optional
	// +listType=set
	ConfigDiffAllowedNamespaces []string `json:"configDiffAllowedNamespaces,omitempty" mapstructure:"configDiffAllowedNamespaces,omitempty"`
}

// ConfigPrereqs is the inline netascode-shaped configuration block the
// controller uses to auto-create an owned platform config CR for a device.
type ConfigPrereqs struct {
	// ManagedFamilies optionally overrides the platform prereq family policy.
	// IOS-XE normally uses the legacy fixed apphosting prereq family set.
	// NX-OS and future platforms derive this list from Configuration's
	// top-level keys when it is omitted.
	// +kubebuilder:validation:Optional
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies,omitempty" mapstructure:"managedFamilies,omitempty"`

	// Configuration is the netascode-shaped fragment. Same shape as
	// the target platform config CR's spec.source.inline.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration"`
}

// DeviceStatus defines the observed state of a CiscoDevice.
//
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.legacyHandoff) || has(self.legacyHandoff) || (oldSelf.legacyHandoff.phase == 'Complete' && has(self.nodeIdentity))",message="a legacy handoff may be cleared only by a new managed Node binding"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.nodeIdentity) || has(self.nodeIdentity) || (has(self.legacyHandoff) && self.legacyHandoff.phase == 'Complete')",message="managed Node identity may be cleared only by a completed legacy handoff"
// +kubebuilder:validation:XValidation:rule="!has(self.legacyHandoff) || (self.legacyHandoff.phase == 'Complete' ? (!has(self.nodeIdentity) && !has(self.topologyProjection) && !has(self.healthObservation) && !has(self.workerRevision)) : (has(self.nodeIdentity) && has(self.topologyProjection)))",message="an in-flight legacy handoff retains managed binding state and a completed handoff releases it"
type DeviceStatus struct {
	// Phase represents the current lifecycle phase of the device.
	// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Error;Deleting
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the device's state.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// NodeIdentity is the bounded manager-owned binding between this
	// namespaced CiscoDevice and its cluster-scoped virtual Node. The Node UID,
	// not the name alone, must be verified before manager patch or delete.
	// +kubebuilder:validation:Optional
	NodeIdentity *DeviceNodeIdentityStatus `json:"nodeIdentity,omitempty"`

	// TopologyProjection is an audit summary of the manager-owned label
	// projection. Desired labels remain in metadata; status intentionally does
	// not duplicate them.
	// +kubebuilder:validation:Optional
	TopologyProjection *DeviceTopologyProjectionStatus `json:"topologyProjection,omitempty"`

	// HealthObservation is a manager-authenticated snapshot binding the latest
	// observed Node Ready heartbeat to the current CiscoDevice condition set.
	// Rollout admission requires both sides of this snapshot to remain current;
	// a fresh Node heartbeat cannot mask stale or subsequently changed device
	// health evidence.
	// +kubebuilder:validation:Optional
	HealthObservation *DeviceHealthObservationStatus `json:"healthObservation,omitempty"`

	// WorkerRevision binds the desired per-device Deployment PodTemplate to the
	// exact running Pod and its post-start managed Node heartbeat. A rollout may
	// use gNOI only when desiredRevision and observedRevision are identical.
	// +kubebuilder:validation:Optional
	WorkerRevision *DeviceWorkerRevisionStatus `json:"workerRevision,omitempty"`

	// LegacyHandoff records the explicit, UID-bound reverse writer handoff from
	// managed topology to an isolated per-device legacy worker. A Complete
	// record is retained after NodeIdentity is cleared so later reconciles never
	// fall back to the release-wide shared ServiceAccount.
	// +kubebuilder:validation:Optional
	LegacyHandoff *DeviceLegacyHandoffStatus `json:"legacyHandoff,omitempty"`

	// TopologyLock is the manager-owned CAS barrier that freezes this device's
	// risk-domain projection between campaign admission and exact reservation
	// release. Main-resource admission rejects protected topology changes while
	// the lock is present.
	// +kubebuilder:validation:Optional
	TopologyLock *DeviceTopologyLockStatus `json:"topologyLock,omitempty"`

	// MaintenanceSession is the bounded durable manager-owned acknowledgement
	// for a worker mutation request. Managed workers must observe a matching
	// active token, Lease holder, operation, device, and Node incarnation before
	// dispatching a new mutation.
	// +kubebuilder:validation:Optional
	MaintenanceSession *DeviceMaintenanceSessionStatus `json:"maintenanceSession,omitempty"`

	// WorkerTopology records where device-local work is currently executed.
	// The production default is PerDevice: one horizontally scheduled CVK
	// worker owns apphosting, config, telemetry, diagnostics, and lifecycle
	// actions for this device. Aggregated is reserved for opt-in manager-side
	// config aggregation and must not be assumed by future orchestrators.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=PerDevice;Aggregated;None
	WorkerTopology WorkerTopology `json:"workerTopology,omitempty"`

	// WorkerCapabilities is a bounded summary of the runtimes the current
	// topology exposes for this device. It gives later topology-aware
	// orchestrators a stable read surface without moving execution out of
	// per-device workers.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	WorkerCapabilities []WorkerCapabilityStatus `json:"workerCapabilities,omitempty"`

	// NetAsCode records the Network as Code model stripe this device should
	// align with. It is descriptive status: the platform-specific config CRDs
	// remain the source of desired intent.
	// +kubebuilder:validation:Optional
	NetAsCode *NetAsCodeModelStatus `json:"netAsCode,omitempty"`
}

// DeviceNodeIdentityStatus records the resolved, UID-bound Node and physical
// device identity. It is manager-owned status and is never an adoption request.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="managed Node identity is immutable"
type DeviceNodeIdentityStatus struct {
	// NodeName is the immutable resolved Node name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	NodeName string `json:"nodeName"`

	// NodeUID is the UID observed when the manager atomically reserved or
	// explicitly adopted NodeName.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// DeviceUID binds the record to this exact CiscoDevice incarnation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// PhysicalIdentity is the lowercase canonical form of the immutable
	// administrator declaration in spec.physicalIdentity. It is the trusted
	// rollout/deduplication binding; authenticated worker inventory is only a
	// live consistency signal and must agree with it.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9._:/-]{0,126}[a-z0-9])?$`
	PhysicalIdentity string `json:"physicalIdentity"`
}

// DeviceLegacyHandoffPhase is the durable reverse writer-handoff phase.
//
// +kubebuilder:validation:Enum=Preparing;LegacyWriterPending;Complete
type DeviceLegacyHandoffPhase string

const (
	// DeviceLegacyHandoffPreparing means the managed mutation boundary was
	// proven idle and the isolated legacy identity is being rolled out.
	DeviceLegacyHandoffPreparing DeviceLegacyHandoffPhase = "Preparing"
	// DeviceLegacyHandoffLegacyWriterPending means managed API authority and
	// Leases were revoked and the guarded Node was released to the legacy writer.
	DeviceLegacyHandoffLegacyWriterPending DeviceLegacyHandoffPhase = "LegacyWriterPending"
	// DeviceLegacyHandoffComplete means a new legacy worker and post-release
	// Node Ready heartbeat were observed. The record remains as identity state.
	DeviceLegacyHandoffComplete DeviceLegacyHandoffPhase = "Complete"
)

// DeviceLegacyHandoffStatus binds a reverse writer handoff to one exact
// CiscoDevice and Node incarnation. Identity is immutable for the life of the
// record and phase may only advance. A later managed enrollment replaces the
// record only after a fresh forward writer handoff.
//
// +kubebuilder:validation:XValidation:rule="self.deviceUID == oldSelf.deviceUID && self.nodeName == oldSelf.nodeName && self.nodeUID == oldSelf.nodeUID && self.projectionHash == oldSelf.projectionHash && self.legacyWorkerUsername == oldSelf.legacyWorkerUsername && self.requestedAt == oldSelf.requestedAt",message="legacy handoff identity is immutable"
// +kubebuilder:validation:XValidation:rule="oldSelf.phase == 'Preparing' ? self.phase in ['Preparing', 'LegacyWriterPending'] : (oldSelf.phase == 'LegacyWriterPending' ? self.phase in ['LegacyWriterPending', 'Complete'] : self.phase == 'Complete')",message="legacy handoff phase may only advance"
// +kubebuilder:validation:XValidation:rule="self.phase == 'Preparing' ? !has(self.nodeReleasedAt) && !has(self.completedAt) : (self.phase == 'LegacyWriterPending' ? has(self.nodeReleasedAt) && !has(self.completedAt) : has(self.nodeReleasedAt) && has(self.completedAt))",message="legacy handoff timestamps must match phase"
// +kubebuilder:validation:XValidation:rule="!has(self.nodeReleasedAt) || self.nodeReleasedAt >= self.requestedAt",message="legacy handoff release cannot precede its request"
// +kubebuilder:validation:XValidation:rule="!has(self.completedAt) || (has(self.nodeReleasedAt) && self.completedAt >= self.nodeReleasedAt)",message="legacy handoff completion cannot precede release"
type DeviceLegacyHandoffStatus struct {
	// Phase is the current durable reverse handoff phase.
	// +kubebuilder:validation:Required
	Phase DeviceLegacyHandoffPhase `json:"phase"`

	// DeviceUID binds the handoff to this CiscoDevice incarnation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// NodeName and NodeUID bind the handoff to the exact released Node.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	NodeName string `json:"nodeName"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// ProjectionHash is the last managed projection released by this handoff.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ProjectionHash string `json:"projectionHash"`

	// LegacyWorkerUsername is the exact isolated ServiceAccount username that
	// must prove readiness. It can never name the chart-wide shared identity.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=320
	LegacyWorkerUsername string `json:"legacyWorkerUsername"`

	// RequestedAt is manager time when the authorized request was accepted.
	// +kubebuilder:validation:Required
	RequestedAt metav1.Time `json:"requestedAt"`

	// NodeReleasedAt is set immediately before the manager removes managed Node
	// ownership. The legacy readiness heartbeat must not predate it.
	// +kubebuilder:validation:Optional
	NodeReleasedAt *metav1.Time `json:"nodeReleasedAt,omitempty"`

	// CompletedAt is set only after the legacy writer readiness proof passes.
	// +kubebuilder:validation:Optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// DeviceTopologyProjectionStatus is a bounded audit record for the effective
// scheduling-label projection. Conditions on DeviceStatus carry failures and
// conflicts; this record is updated only after a successful projection.
type DeviceTopologyProjectionStatus struct {
	// EffectiveLabelHash is the content address of the normalized, allowlisted
	// labels projected to the Node.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	EffectiveLabelHash string `json:"effectiveLabelHash"`

	// SourceResourceVersion is the CiscoDevice metadata.resourceVersion whose
	// labels were projected. Label changes do not advance generation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	SourceResourceVersion string `json:"sourceResourceVersion"`

	// LastSuccessfulTime is when the manager established this effective
	// projection on the bound Node. The managed worker's separate status
	// heartbeat proves that the writer handoff occurred after this epoch.
	// +kubebuilder:validation:Required
	LastSuccessfulTime metav1.Time `json:"lastSuccessfulTime"`
}

// DeviceHealthObservationStatus binds independently changing Node and
// CiscoDevice health evidence at one manager observation boundary. It is not a
// desired-state source and is refreshed only when the source heartbeat or the
// normalized condition snapshot changes.
type DeviceHealthObservationStatus struct {
	// ObservedAt is manager time when this exact source snapshot was verified.
	// +kubebuilder:validation:Required
	ObservedAt metav1.Time `json:"observedAt"`

	// NodeReadyHeartbeatTime is the bound worker's Ready heartbeat in the
	// snapshot. The rollout controller compares it with the live Node.
	// +kubebuilder:validation:Required
	NodeReadyHeartbeatTime metav1.Time `json:"nodeReadyHeartbeatTime"`

	// DeviceConditionsHash covers device phase and all current conditions,
	// including their status, reason, generation, transition time, and message.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	DeviceConditionsHash string `json:"deviceConditionsHash"`

	// ConditionObservations carries independent producer observation times for
	// manager-evaluated conditions. A fresh Node heartbeat does not refresh
	// these entries. Rollout-required conditions without a matching entry are
	// rejected; LastTransitionTime is not proof that the manager ran a producer.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=type
	ConditionObservations []DeviceConditionObservationStatus `json:"conditionObservations,omitempty"`
}

// DeviceWorkerRevisionStatus is manager-authenticated evidence that the
// current managed worker Pod loaded the exact desired PodTemplate inputs. The
// revision is an opaque SHA-256 content address; Secret bytes never enter it.
//
// +kubebuilder:validation:XValidation:rule="has(self.deploymentUID) == has(self.deploymentGeneration)",message="deployment UID and generation must be present together"
// +kubebuilder:validation:XValidation:rule="has(self.observedRevision) ? has(self.deploymentUID) : true",message="an observed worker revision requires Deployment identity"
// +kubebuilder:validation:XValidation:rule="has(self.podUID) == has(self.podStartTime)",message="Pod UID and start time must be present together"
// +kubebuilder:validation:XValidation:rule="has(self.podUID) ? has(self.deploymentUID) : true",message="Pod identity requires Deployment identity"
// +kubebuilder:validation:XValidation:rule="has(self.readyHeartbeatTime) ? (has(self.podUID) && has(self.observedRevision) && self.observedRevision == self.desiredRevision) : true",message="a ready heartbeat requires matching desired/observed revisions and Pod identity"
type DeviceWorkerRevisionStatus struct {
	// DesiredRevision is computed from the desired PodTemplate after removing
	// only the revision's own carrier annotation and environment variable.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	DesiredRevision string `json:"desiredRevision"`

	// ObservedRevision is reported by the running managed worker through its
	// status-only Node writer. It is optional while a Deployment is converging.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ObservedRevision string `json:"observedRevision,omitempty"`

	// DeploymentUID and DeploymentGeneration identify the exact desired
	// Deployment incarnation and revision observed by the manager.
	// They are absent in the durable pre-rollout fence, then populated together
	// after the manager observes the desired Deployment incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeploymentUID string `json:"deploymentUID,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	DeploymentGeneration int64 `json:"deploymentGeneration,omitempty"`

	// PodUID and PodStartTime identify the sole ready Pod of a completed
	// Deployment rollout. They are absent while the rollout is converging.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	PodUID string `json:"podUID,omitempty"`

	// +kubebuilder:validation:Optional
	PodStartTime *metav1.Time `json:"podStartTime,omitempty"`

	// ReadyHeartbeatTime is the matching managed-worker Node heartbeat and must
	// not predate PodStartTime. It is absent until the new Pod has reported.
	// +kubebuilder:validation:Optional
	ReadyHeartbeatTime *metav1.Time `json:"readyHeartbeatTime,omitempty"`

	// ObservedAt is manager time for this desired/observed snapshot.
	// +kubebuilder:validation:Required
	ObservedAt metav1.Time `json:"observedAt"`
}

// DeviceConditionObservationStatus records when the manager last evaluated
// one condition's underlying source rather than merely re-reading its value.
type DeviceConditionObservationStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Za-z]([A-Za-z0-9_.-]*[A-Za-z0-9])?$`
	Type string `json:"type"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status metav1.ConditionStatus `json:"status"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration"`

	// +kubebuilder:validation:Required
	ObservedAt metav1.Time `json:"observedAt"`
}

// DeviceTopologyLockStatus binds one exact rollout reservation transaction to
// the CiscoDevice and Node incarnations and the topology projection used for
// admission. Release is a durable two-step fence: the owning manager first
// marks the exact lock Releasing, then removes the reservation, and clears the
// lock only after proving that no reservation for the DeviceUID remains.
//
// +kubebuilder:validation:XValidation:rule="self.campaignNamespace == oldSelf.campaignNamespace && self.campaignName == oldSelf.campaignName && self.campaignUID == oldSelf.campaignUID && self.planHash == oldSelf.planHash && self.reservationID == oldSelf.reservationID && self.deviceUID == oldSelf.deviceUID && self.deviceGeneration == oldSelf.deviceGeneration && self.nodeUID == oldSelf.nodeUID && self.projectionHash == oldSelf.projectionHash && self.policyEpoch == oldSelf.policyEpoch && self.acquisitionID == oldSelf.acquisitionID && self.acquiredAt == oldSelf.acquiredAt",message="topology lock transaction identity is immutable"
// +kubebuilder:validation:XValidation:rule="oldSelf.state == 'Active' ? self.state in ['Active', 'Releasing'] : self.state == 'Releasing'",message="topology lock may only advance from Active to Releasing"
type DeviceTopologyLockStatus struct {
	// State is Active while a reservation may be acquired or retained and
	// Releasing before ledger cleanup begins. Releasing is itself still a full
	// topology/reclassification fence.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Active;Releasing
	State DeviceTopologyLockState `json:"state"`

	// PolicyEpoch prevents cleanup from an older administrator-policy epoch from
	// fencing or clearing a later reservation that reuses the deterministic ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	PolicyEpoch int64 `json:"policyEpoch"`

	// AcquisitionID distinguishes separate lock acquisitions for the same
	// campaign, target, policy epoch, and deterministic reservation ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	AcquisitionID string `json:"acquisitionID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	CampaignNamespace string `json:"campaignNamespace"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	CampaignName string `json:"campaignName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	CampaignUID string `json:"campaignUID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ReservationID string `json:"reservationID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	DeviceGeneration int64 `json:"deviceGeneration"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ProjectionHash string `json:"projectionHash"`

	// +kubebuilder:validation:Required
	AcquiredAt metav1.Time `json:"acquiredAt"`
}

// DeviceTopologyLockState is the durable release fence for one topology lock.
//
// +kubebuilder:validation:Enum=Active;Releasing
type DeviceTopologyLockState string

const (
	DeviceTopologyLockActive    DeviceTopologyLockState = "Active"
	DeviceTopologyLockReleasing DeviceTopologyLockState = "Releasing"
)

// DeviceMaintenanceSessionPhase is the manager-owned maintenance handoff
// state. A settled record may be replaced by a later distinct session; active
// session identity fields cannot be rewritten in place.
//
// +kubebuilder:validation:Enum=Acknowledged;Active;Settled
type DeviceMaintenanceSessionPhase string

const (
	DeviceMaintenanceSessionAcknowledged DeviceMaintenanceSessionPhase = "Acknowledged"
	DeviceMaintenanceSessionActive       DeviceMaintenanceSessionPhase = "Active"
	DeviceMaintenanceSessionSettled      DeviceMaintenanceSessionPhase = "Settled"
)

// DeviceMaintenanceObjectReference binds a maintenance session to one exact
// namespaced API object incarnation.
type DeviceMaintenanceObjectReference struct {
	// Namespace is the object's namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// Name is the object's name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// UID rejects delete/recreate identity substitution.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

// DeviceMaintenanceLeaseReference binds a session request to the current
// mutation Lease incarnation and holder. Request metadata must never transfer
// to a successor holder.
type DeviceMaintenanceLeaseReference struct {
	DeviceMaintenanceObjectReference `json:",inline"`

	// Holder is the exact Lease holderIdentity that submitted the request.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Holder string `json:"holder"`
}

// DeviceMaintenanceSessionStatus is a bounded durable request/acknowledgement
// protocol between a managed device worker and the manager. Native admission
// must reserve this status field to the manager; the worker writes its request
// metadata through the separately owned mutation Lease.
//
// +kubebuilder:validation:XValidation:rule="has(self.acknowledgedAt)",message="acknowledgedAt is required after manager acknowledgement"
// +kubebuilder:validation:XValidation:rule="oldSelf.phase == 'Settled' || (self.sessionToken == oldSelf.sessionToken && self.lease == oldSelf.lease && self.operation == oldSelf.operation && self.deviceUID == oldSelf.deviceUID && self.nodeName == oldSelf.nodeName && self.nodeUID == oldSelf.nodeUID && self.requestedAt == oldSelf.requestedAt)",message="active maintenance session identity is immutable"
// +kubebuilder:validation:XValidation:rule="self.sessionToken != oldSelf.sessionToken || self.controlRevision >= oldSelf.controlRevision",message="maintenance controlRevision cannot decrease within a session"
type DeviceMaintenanceSessionStatus struct {
	// Phase is the current durable manager handoff phase.
	// +kubebuilder:validation:Required
	Phase DeviceMaintenanceSessionPhase `json:"phase"`

	// SessionToken uniquely binds one request/acknowledgement exchange and must
	// not be reused for a later Lease holder or operation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=16
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._:-]*$`
	SessionToken string `json:"sessionToken"`

	// Lease is the exact current mutation Lease and holder that requested the
	// session.
	// +kubebuilder:validation:Required
	Lease DeviceMaintenanceLeaseReference `json:"lease"`

	// Operation is the exact namespaced operation object requesting mutation.
	// +kubebuilder:validation:Required
	Operation DeviceMaintenanceObjectReference `json:"operation"`

	// DeviceUID binds the session to this CiscoDevice incarnation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// NodeName is the manager-resolved Node identity.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	NodeName string `json:"nodeName"`

	// NodeUID binds the guard to the exact Node incarnation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// RequestedAt is the request timestamp preserved across renewal and restart.
	// +kubebuilder:validation:Required
	RequestedAt metav1.Time `json:"requestedAt"`

	// AcknowledgedAt is set only after the manager applies and verifies the
	// Node guard for this exact session.
	// +kubebuilder:validation:Optional
	AcknowledgedAt *metav1.Time `json:"acknowledgedAt,omitempty"`

	// ControlRevision is the campaign/operation control revision acknowledged
	// for this managed session. A new operation starts its own revision sequence.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ControlRevision int64 `json:"controlRevision"`

	// Message is a bounded human-readable blocked or transition explanation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Message string `json:"message,omitempty"`
}

// WorkerTopology identifies the execution placement for per-device work.
type WorkerTopology string

const (
	// WorkerTopologyPerDevice means a per-device CVK worker Deployment owns
	// the device-local execution loops.
	WorkerTopologyPerDevice WorkerTopology = "PerDevice"
	// WorkerTopologyAggregated means the manager-side config aggregator owns
	// config reconciliation for this device.
	WorkerTopologyAggregated WorkerTopology = "Aggregated"
	// WorkerTopologyNone means no CVK execution worker is active.
	WorkerTopologyNone WorkerTopology = "None"
)

// WorkerCapabilityName is a stable, low-cardinality worker runtime name.
type WorkerCapabilityName string

const (
	WorkerCapabilityAppHosting  WorkerCapabilityName = "apphosting"
	WorkerCapabilityConfig      WorkerCapabilityName = "config"
	WorkerCapabilityTelemetry   WorkerCapabilityName = "telemetry"
	WorkerCapabilityDiagnostics WorkerCapabilityName = "diagnostics"
	WorkerCapabilityOperations  WorkerCapabilityName = "operations"
	WorkerCapabilityLifecycle   WorkerCapabilityName = "lifecycle"
)

// WorkerRuntime records the runtime that owns a capability.
type WorkerRuntime string

const (
	WorkerRuntimePerDeviceWorker WorkerRuntime = "per-device-worker"
	WorkerRuntimeAggregator      WorkerRuntime = "manager-aggregator"
)

// WorkerCapabilityStatus reports whether a device capability is active in
// the current execution topology.
type WorkerCapabilityStatus struct {
	// Name is the stable capability key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=apphosting;config;telemetry;diagnostics;operations;lifecycle
	Name WorkerCapabilityName `json:"name"`

	// Enabled is true when the current topology exposes this capability.
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// Runtime names the process topology that owns the capability.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=per-device-worker;manager-aggregator
	Runtime WorkerRuntime `json:"runtime,omitempty"`

	// Message is a short human-readable explanation for disabled or shifted
	// capabilities.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// NetAsCodeModelType mirrors Network as Code's model categories: controller,
// device, and solution centric.
type NetAsCodeModelType string

const (
	NetAsCodeModelControllerCentric NetAsCodeModelType = "ControllerCentric"
	NetAsCodeModelDeviceCentric     NetAsCodeModelType = "DeviceCentric"
	NetAsCodeModelSolutionCentric   NetAsCodeModelType = "SolutionCentric"
)

// NetAsCodeModelStatus describes the Network as Code stripe aligned with a
// platform. Keep this summary small; detailed family coverage belongs in the
// platform config CRD and writer registry.
type NetAsCodeModelStatus struct {
	// Type is the Network as Code model category.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ControllerCentric;DeviceCentric;SolutionCentric
	Type NetAsCodeModelType `json:"type,omitempty"`

	// Format is the platform model format string used by config CRDs, for
	// example netascode-iosxe or netascode-nxos.
	// +kubebuilder:validation:Optional
	Format string `json:"format,omitempty"`

	// Stripe is the technology stripe, for example iosxe, nxos, ise, or fmc.
	// +kubebuilder:validation:Optional
	Stripe string `json:"stripe,omitempty"`

	// Sections names the high-level NetAsCode sections used for this stripe.
	// +kubebuilder:validation:Optional
	// +listType=set
	Sections []string `json:"sections,omitempty"`
}

// TLSConfig represents TLS configuration for device communication.
type TLSConfig struct {
	// Enabled toggles TLS for device communication.
	Enabled bool `json:"enabled" mapstructure:"enabled"`

	// InsecureSkipVerify disables TLS certificate verification.
	// +kubebuilder:validation:Optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" mapstructure:"insecureSkipVerify"`

	// CertFile is the path to the client certificate file.
	// +kubebuilder:validation:Optional
	CertFile string `json:"certFile,omitempty" mapstructure:"certFile,omitempty"`

	// KeyFile is the path to the client key file.
	// +kubebuilder:validation:Optional
	KeyFile string `json:"keyFile,omitempty" mapstructure:"keyFile,omitempty"`

	// CAFile is the path to the CA certificate file.
	// +kubebuilder:validation:Optional
	CAFile string `json:"caFile,omitempty" mapstructure:"caFile,omitempty"`
}

// OTELConfig holds OpenTelemetry topology export configuration.
type OTELConfig struct {
	// Enabled toggles OTEL topology trace emission.
	Enabled bool `json:"enabled" mapstructure:"enabled"`

	// Endpoint is the OTLP gRPC collector endpoint (e.g. "otel-collector:4317").
	// +kubebuilder:validation:Optional
	Endpoint string `json:"endpoint,omitempty" mapstructure:"endpoint"`

	// Insecure disables TLS for the gRPC connection to the OTLP endpoint.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	Insecure bool `json:"insecure,omitempty" mapstructure:"insecure"`

	// ServiceName is the base service name used in OTEL resource attributes.
	// The device hostname is appended: "<serviceName>.<hostname>".
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="cisco-network"
	ServiceName string `json:"serviceName,omitempty" mapstructure:"serviceName"`

	// IntervalSecs is the interval in seconds between topology trace emissions.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:default=60
	IntervalSecs int `json:"intervalSecs,omitempty" mapstructure:"intervalSecs"`

	// MaxLinkSpans caps link spans emitted per topology cycle. Extra links are
	// counted on the root span as topology.dropped_link_count so large devices
	// cannot create unbounded Tempo write bursts.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=256
	MaxLinkSpans int `json:"maxLinkSpans,omitempty" mapstructure:"maxLinkSpans"`
}

// ResourceConfig represents resource limits and defaults for container workloads.
type ResourceConfig struct {
	// +kubebuilder:validation:Optional
	DefaultCPU string `json:"defaultCPU,omitempty" mapstructure:"defaultCPU"`
	// +kubebuilder:validation:Optional
	DefaultMemory string `json:"defaultMemory,omitempty" mapstructure:"defaultMemory"`
	// +kubebuilder:validation:Optional
	DefaultStorage string `json:"defaultStorage,omitempty" mapstructure:"defaultStorage"`
	// +kubebuilder:validation:Optional
	MaxCPU string `json:"maxCPU,omitempty" mapstructure:"maxCPU"`
	// +kubebuilder:validation:Optional
	MaxMemory string `json:"maxMemory,omitempty" mapstructure:"maxMemory"`
	// +kubebuilder:validation:Optional
	MaxStorage string `json:"maxStorage,omitempty" mapstructure:"maxStorage"`
	// +kubebuilder:validation:Optional
	Others map[string]string `json:"others,omitempty" mapstructure:"others,omitempty"`
}
