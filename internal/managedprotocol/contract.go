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

// Package managedprotocol defines the versioned manager/worker identity and
// rollout annotation contract. It contains no controller behavior so both
// sides can validate the exact same wire keys without importing each other.
package managedprotocol

const (
	Version                            = "rollout-v1"
	AdmissionContractVersion           = "v1"
	AnnotationAdmissionContractVersion = "topology.cisco.vk/admission-contract-version"

	AnnotationManaged          = "topology.cisco.vk/managed"
	AnnotationDeviceNamespace  = "topology.cisco.vk/device-namespace"
	AnnotationDeviceName       = "topology.cisco.vk/device-name"
	AnnotationDeviceUID        = "topology.cisco.vk/device-uid"
	AnnotationDeviceGeneration = "topology.cisco.vk/device-generation"
	AnnotationNodeName         = "topology.cisco.vk/node-name"
	AnnotationNodeUID          = "topology.cisco.vk/node-uid"
	AnnotationWorkerUsername   = "topology.cisco.vk/worker-username"
	AnnotationWorkerProtocol   = "topology.cisco.vk/worker-protocol"
	// AnnotationWorkerConfigRevision is the manager-computed content address
	// of the desired per-device PodTemplate. AnnotationWorkerObservedRevision
	// is emitted by the running managed worker through the Node status writer.
	AnnotationWorkerConfigRevision       = "topology.cisco.vk/worker-config-revision"
	AnnotationWorkerObservedRevision     = "topology.cisco.vk/worker-observed-revision"
	AnnotationCredentialSecretRevision   = "cisco.vk/credential-resource-version"
	AnnotationGNOITLSSecretRevision      = "cisco.vk/gnoi-tls-secret-resource-version"
	AnnotationGNOIProvisioningRevision   = "cisco.vk/gnoi-provisioning-secret-resource-version"
	AnnotationWorkerMode                 = "topology.cisco.vk/worker-mode"
	AnnotationCampaignNamespace          = "topology.cisco.vk/campaign-namespace"
	AnnotationCampaignName               = "topology.cisco.vk/campaign-name"
	AnnotationCampaignUID                = "topology.cisco.vk/campaign-uid"
	AnnotationPlanHash                   = "topology.cisco.vk/plan-hash"
	AnnotationLedgerUID                  = "topology.cisco.vk/ledger-uid"
	AnnotationReservationID              = "topology.cisco.vk/reservation-id"
	AnnotationSourceSecretUID            = "topology.cisco.vk/source-secret-uid"
	AnnotationProjectionHash             = "topology.cisco.vk/projection-hash"
	AnnotationProjectedKeys              = "topology.cisco.vk/projected-keys"
	AnnotationManagedTaints              = "topology.cisco.vk/managed-taints"
	AnnotationAdoptNodeUID               = "topology.cisco.vk/adopt-node-uid"
	AnnotationReclassifyFrom             = "topology.cisco.vk/approve-reclassification-from"
	AnnotationRequestLegacyHandoff       = "topology.cisco.vk/request-legacy-handoff"
	AnnotationLegacyHandoff              = "topology.cisco.vk/legacy-handoff"
	AnnotationIsolatedLegacyWorker       = "topology.cisco.vk/isolated-legacy-worker"
	AnnotationLegacyHandoffRelease       = "topology.cisco.vk/legacy-handoff-release"
	AnnotationMaintenanceRequestVersion  = "topology.cisco.vk/maintenance-request-version"
	AnnotationMaintenanceSessionToken    = "topology.cisco.vk/maintenance-session-token"
	AnnotationMaintenanceRequestedAt     = "topology.cisco.vk/maintenance-requested-at"
	AnnotationMaintenanceOperationNS     = "topology.cisco.vk/maintenance-operation-namespace"
	AnnotationMaintenanceOperationName   = "topology.cisco.vk/maintenance-operation-name"
	AnnotationMaintenanceOperationUID    = "topology.cisco.vk/maintenance-operation-uid"
	AnnotationMaintenanceControlRevision = "topology.cisco.vk/maintenance-control-revision"
	AnnotationLeasePurpose               = "topology.cisco.vk/lease-purpose"
	LeasePurposeNodeHeartbeat            = "node-heartbeat"
	LeasePurposeConfigFamily             = "config-family"
	LeasePurposeDeviceMutation           = "device-mutation"
	VirtualKubeletLastAppliedObjectMeta  = "virtual-kubelet.io/last-applied-object-meta"
	VirtualKubeletLastAppliedNodeStatus  = "virtual-kubelet.io/last-applied-node-status"
	TopologyInitializingTaint            = "topology.cisco.vk/uninitialized"
	ManagedWorkerReadyCondition          = "CiscoVirtualKubeletManagedReady"
	ManagedWorkerReadyReason             = "StatusOnlyRolloutV1"
	ManagedWorkerClusterRole             = "cisco-virtual-kubelet-managed-worker"
	WorkerModeLegacy                     = "legacy"
	ImageFamilyLabel                     = "operations.cisco.vk/image-family"
	QualificationCohortLabel             = "operations.cisco.vk/qualification-cohort"

	EnvManagedTopology          = "CISCO_VK_MANAGED_TOPOLOGY"
	EnvDeviceNamespace          = "CISCO_VK_DEVICE_NAMESPACE"
	EnvDeviceName               = "CISCO_VK_DEVICE_NAME"
	EnvDeviceUID                = "CISCO_VK_DEVICE_UID"
	EnvNodeName                 = "CISCO_VK_NODE_NAME"
	EnvWorkerRevision           = "CISCO_VK_WORKER_CONFIG_REVISION"
	EnvCredentialSecretRevision = "CISCO_VK_CREDENTIAL_SECRET_RESOURCE_VERSION"
	EnvGNOITLSSecretRevision    = "CISCO_VK_GNOI_TLS_SECRET_RESOURCE_VERSION"
	EnvGNOIProvisioningRevision = "CISCO_VK_GNOI_PROVISIONING_SECRET_RESOURCE_VERSION"
)
