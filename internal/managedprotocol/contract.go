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

// NetworkObjectBindingAnnotationKeys is the manager-owned identity envelope
// copied from a device-scoped network object to any child objects the shared
// network worker creates. Admission treats every key as protected metadata.
var NetworkObjectBindingAnnotationKeys = []string{
	AnnotationManaged,
	AnnotationDeviceNamespace,
	AnnotationDeviceName,
	AnnotationDeviceUID,
	AnnotationNetworkWorkerUsername,
	AnnotationNetworkWorkerPodName,
	AnnotationNetworkWorkerPodUID,
}

// CopyNetworkObjectBinding returns only the protected network identity keys.
// Empty values are deliberately omitted so a worker cannot manufacture a
// partially valid envelope during a rollout gap.
func CopyNetworkObjectBinding(source map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range NetworkObjectBindingAnnotationKeys {
		if value := source[key]; value != "" {
			out[key] = value
		}
	}
	return out
}

// NetworkObjectBindingComplete reports whether annotations contain the exact
// non-empty managed identity envelope that admission requires on a
// device-scoped network object. Callers use this at result producer boundaries
// so an incomplete owner cannot emit a partially bound child during rollout.
func NetworkObjectBindingComplete(annotations map[string]string) bool {
	if annotations[AnnotationManaged] != "true" {
		return false
	}
	for _, key := range NetworkObjectBindingAnnotationKeys {
		if annotations[key] == "" {
			return false
		}
	}
	return true
}

// NetworkObjectBindingMatches reports whether a child carries the complete
// protected identity envelope of its managed owner. Admission cannot
// dereference owner references, so result producers and consumers perform the
// cross-object comparison where both objects are available.
func NetworkObjectBindingMatches(owner, child map[string]string) bool {
	if !NetworkObjectBindingComplete(owner) {
		return false
	}
	for _, key := range NetworkObjectBindingAnnotationKeys {
		if child[key] != owner[key] {
			return false
		}
	}
	return true
}

// NetworkResultNamePrefix uses the immutable device UID rather than mutable,
// potentially 253-byte object names. Admission bounds UIDs to 64 DNS-safe
// bytes, leaving ample room for the owner UID and result discriminator.
func NetworkResultNamePrefix(deviceUID string) string {
	return "u" + deviceUID + "-result-"
}

const (
	Version                            = "rollout-v1"
	AdmissionContractVersion           = "v2"
	AnnotationAdmissionContractVersion = "topology.cisco.vk/admission-contract-version"
	AnnotationAdmissionContractDigest  = "topology.cisco.vk/admission-contract-digest"
	// AnnotationWorkerServiceAccountPolicy binds a shared or generated worker
	// ServiceAccount incarnation to the already-preflighted reserved-account
	// admission policy and binding. A contract upgrade therefore forces one UID
	// rotation instead of trusting legacy token Secret metadata.
	AnnotationWorkerServiceAccountPolicy = "topology.cisco.vk/service-account-policy"

	AnnotationManaged          = "topology.cisco.vk/managed"
	AnnotationDeviceNamespace  = "topology.cisco.vk/device-namespace"
	AnnotationDeviceName       = "topology.cisco.vk/device-name"
	AnnotationDeviceUID        = "topology.cisco.vk/device-uid"
	AnnotationDeviceGeneration = "topology.cisco.vk/device-generation"
	AnnotationNodeName         = "topology.cisco.vk/node-name"
	AnnotationNodeUID          = "topology.cisco.vk/node-uid"
	AnnotationWorkerUsername   = "topology.cisco.vk/worker-username"
	// The functional worker identities are shared by every managed device in a
	// namespace.  Bound ServiceAccount token Pod identity, recorded alongside
	// the username, restores an exact per-device admission boundary without
	// manufacturing one ServiceAccount per device.
	AnnotationAppWorkerUsername     = "topology.cisco.vk/app-worker-username"
	AnnotationAppWorkerPodName      = "topology.cisco.vk/app-worker-pod-name"
	AnnotationAppWorkerPodUID       = "topology.cisco.vk/app-worker-pod-uid"
	AnnotationNetworkWorkerUsername = "topology.cisco.vk/network-worker-username"
	AnnotationNetworkWorkerPodName  = "topology.cisco.vk/network-worker-pod-name"
	AnnotationNetworkWorkerPodUID   = "topology.cisco.vk/network-worker-pod-uid"
	AnnotationWorkerProtocol        = "topology.cisco.vk/worker-protocol"
	// AnnotationWorkerConfigRevision is the manager-computed content address
	// of the desired per-device PodTemplate. AnnotationWorkerObservedRevision
	// is emitted by the running managed worker through the Node status writer.
	AnnotationWorkerConfigRevision     = "topology.cisco.vk/worker-config-revision"
	AnnotationWorkerObservedRevision   = "topology.cisco.vk/worker-observed-revision"
	AnnotationCredentialSecretRevision = "cisco.vk/credential-resource-version"
	AnnotationGNOITLSSecretRevision    = "cisco.vk/gnoi-tls-secret-resource-version"
	AnnotationGNOIProvisioningRevision = "cisco.vk/gnoi-provisioning-secret-resource-version"
	AnnotationWorkerMode               = "topology.cisco.vk/worker-mode"
	AnnotationCampaignNamespace        = "topology.cisco.vk/campaign-namespace"
	AnnotationCampaignName             = "topology.cisco.vk/campaign-name"
	AnnotationCampaignUID              = "topology.cisco.vk/campaign-uid"
	AnnotationPlanHash                 = "topology.cisco.vk/plan-hash"
	AnnotationLedgerUID                = "topology.cisco.vk/ledger-uid"
	AnnotationReservationID            = "topology.cisco.vk/reservation-id"
	AnnotationSourceSecretUID          = "topology.cisco.vk/source-secret-uid"
	AnnotationProjectionHash           = "topology.cisco.vk/projection-hash"
	AnnotationProjectedKeys            = "topology.cisco.vk/projected-keys"
	AnnotationManagedTaints            = "topology.cisco.vk/managed-taints"
	// AnnotationAppHostingCordonDeviceUID records that the manager, rather
	// than an operator, set spec.unschedulable while app-hosting write access
	// was being removed. Its device UID value prevents a replacement object
	// from inheriting authority to restore an older incarnation's cordon.
	AnnotationAppHostingCordonDeviceUID        = "topology.cisco.vk/app-hosting-cordon-device-uid"
	AnnotationAdoptNodeUID                     = "topology.cisco.vk/adopt-node-uid"
	AnnotationReclassifyFrom                   = "topology.cisco.vk/approve-reclassification-from"
	AnnotationRequestLegacyHandoff             = "topology.cisco.vk/request-legacy-handoff"
	AnnotationLegacyHandoff                    = "topology.cisco.vk/legacy-handoff"
	AnnotationIsolatedLegacyWorker             = "topology.cisco.vk/isolated-legacy-worker"
	AnnotationLegacyHandoffRelease             = "topology.cisco.vk/legacy-handoff-release"
	AnnotationMaintenanceRequestVersion        = "topology.cisco.vk/maintenance-request-version"
	AnnotationMaintenanceSessionToken          = "topology.cisco.vk/maintenance-session-token"
	AnnotationMaintenanceRequestedAt           = "topology.cisco.vk/maintenance-requested-at"
	AnnotationMaintenanceOperationNS           = "topology.cisco.vk/maintenance-operation-namespace"
	AnnotationMaintenanceOperationName         = "topology.cisco.vk/maintenance-operation-name"
	AnnotationMaintenanceOperationUID          = "topology.cisco.vk/maintenance-operation-uid"
	AnnotationMaintenanceControlRevision       = "topology.cisco.vk/maintenance-control-revision"
	AnnotationLeasePurpose                     = "topology.cisco.vk/lease-purpose"
	LeasePurposeNodeHeartbeat                  = "node-heartbeat"
	LeasePurposeConfigFamily                   = "config-family"
	LeasePurposeDeviceMutation                 = "device-mutation"
	VirtualKubeletLastAppliedObjectMeta        = "virtual-kubelet.io/last-applied-object-meta"
	VirtualKubeletLastAppliedNodeStatus        = "virtual-kubelet.io/last-applied-node-status"
	TopologyInitializingTaint                  = "topology.cisco.vk/uninitialized"
	ManagedWorkerReadyCondition                = "CiscoVirtualKubeletManagedReady"
	ManagedWorkerReadyReason                   = "StatusOnlyRolloutV1"
	ManagedWorkerClusterRole                   = "cisco-virtual-kubelet-managed-worker"
	AppHostingServiceAccount                   = "cisco-vk-app-hosting"
	NetworkManagementServiceAccount            = "cisco-vk-network-management"
	AppHostingReadOnlyClusterRole              = "cisco-virtual-kubelet-app-hosting-read-only"
	AppHostingReadWriteClusterRole             = "cisco-virtual-kubelet-app-hosting-read-write"
	AppHostingDeviceReadClusterRole            = "cisco-virtual-kubelet-app-hosting-device-read"
	NetworkManagementReadOnlyClusterRole       = "cisco-virtual-kubelet-network-management-read-only"
	NetworkManagementReadWriteClusterRole      = "cisco-virtual-kubelet-network-management-read-write"
	NetworkManagementGlobalReadClusterRole     = "cisco-virtual-kubelet-network-management-global-read"
	NetworkManagementLeaseReadOnlyClusterRole  = "cisco-virtual-kubelet-network-management-lease-read-only"
	NetworkManagementLeaseReadWriteClusterRole = "cisco-virtual-kubelet-network-management-lease-read-write"
	WorkerModeLegacy                           = "legacy"
	WorkerModeCombined                         = "combined"
	WorkerModeAppHosting                       = "app-hosting"
	WorkerModeNetworkManagement                = "network-management"
	WorkerAccessReadOnly                       = "readOnly"
	WorkerAccessReadWrite                      = "readWrite"
	WorkerAccessDisabled                       = "disabled"
	ImageFamilyLabel                           = "operations.cisco.vk/image-family"
	QualificationCohortLabel                   = "operations.cisco.vk/qualification-cohort"

	EnvManagedTopology          = "CISCO_VK_MANAGED_TOPOLOGY"
	EnvDeviceNamespace          = "CISCO_VK_DEVICE_NAMESPACE"
	EnvDeviceName               = "CISCO_VK_DEVICE_NAME"
	EnvDeviceUID                = "CISCO_VK_DEVICE_UID"
	EnvNodeName                 = "CISCO_VK_NODE_NAME"
	EnvWorkerRevision           = "CISCO_VK_WORKER_CONFIG_REVISION"
	EnvWorkerMode               = "CISCO_VK_WORKER_MODE"
	EnvWorkerAccess             = "CISCO_VK_WORKER_ACCESS"
	EnvExpectedWorkerUsername   = "CISCO_VK_EXPECTED_WORKER_USERNAME"
	EnvCredentialSecretRevision = "CISCO_VK_CREDENTIAL_SECRET_RESOURCE_VERSION"
	EnvGNOITLSSecretRevision    = "CISCO_VK_GNOI_TLS_SECRET_RESOURCE_VERSION"
	EnvGNOIProvisioningRevision = "CISCO_VK_GNOI_PROVISIONING_SECRET_RESOURCE_VERSION"
)
