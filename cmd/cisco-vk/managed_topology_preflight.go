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

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

var managedTopologyCRDs = []schema.GroupVersionResource{
	opsv1alpha1.GroupVersion.WithResource("iosxesoftwarerollouts"),
}

var managedAdmissionPolicySuffixes = []string{
	"managed-node",
	"legacy-node-marker",
	"managed-pod-status",
	"managed-pod-delete",
	"managed-drain-pod",
	"managed-device",
	"managed-rollout",
	"managed-upgrade-leaf",
	"topology-policy",
	"topology-ledger",
	"managed-maintenance-lease",
	"shared-pod-status",
	"shared-pod-binding",
	"shared-pod-delete",
	"shared-node-status",
	"shared-upgrade-leaf",
	"shared-network-object",
	"shared-network-result",
	"shared-maintenance-lease",
	"shared-worker-serviceaccount",
	"generated-worker-serviceaccount",
	"shared-worker-token",
	"shared-worker-token-secret",
	"shared-worker-deployment",
	"shared-worker-replicaset",
	"shared-worker-pod",
	"shared-worker-pod-update",
}

type admissionContractExpectation struct {
	apiGroups         []string
	apiVersions       []string
	resources         []string
	operations        []admissionv1.OperationType
	scope             admissionv1.ScopeType
	matchConditions   []string
	variables         []string
	validations       int
	requiredFragments []string
	coreTyped         bool
	publishDigest     bool
	digest            string
}

var managedAdmissionExpectations = map[string]admissionContractExpectation{
	"managed-node": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"nodes", "nodes/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.ClusterScope,
		matchConditions: []string{"managed-node"}, variables: []string{"nativeNodeLifecycle", "manager", "oldManaged", "managerLegacyHandoff", "managerReleasedLegacyNodeUpdate", "legacyHandoffMarkerPreserved", "managerFunctionalWorkerBinding"}, validations: 5, coreTyped: true,
		requiredFragments: []string{"worker-username", "app-worker-username", "network-worker-username", "request.subResource == 'status'", "object.spec == oldObject.spec", "node-uid", "device-uid", "worker-protocol", "worker-observed-revision", "last-applied-node-status", "managerLegacyHandoff", "managerReleasedLegacyNodeUpdate", "legacy-handoff", "oldObject.metadata.annotations['topology.cisco.vk/legacy-handoff'] ==", "oldObject.metadata.uid", "topology.cisco.vk/uninitialized", "t.effect == 'NoSchedule'", "oldObject.spec.taints.filter", "projected-keys", "managed-taints"},
		digest:            "sha256:5d59b86010277307c9821769347b8a46e58696d0002eb3f7e92bbab5a31d2d34",
	},
	"legacy-node-marker": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"nodes", "nodes/status"},
		operations: []admissionv1.OperationType{admissionv1.Update, admissionv1.Delete}, scope: admissionv1.ClusterScope,
		matchConditions: []string{"released-node"}, variables: []string{"manager"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"topology.cisco.vk/legacy-handoff", "oldObject.metadata.uid", "request.operation == 'UPDATE'", "request.userInfo.username"},
		publishDigest:     true,
		digest:            "sha256:02c0e65602ac0ebcc3d19b15bd7cbcd7c3840c081d72f7541efbafc394f2ee76",
	},
	"managed-pod-status": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods/status"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"generated-worker"}, variables: []string{"usernameParts", "workerServiceAccount"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"cisco-vk-managed-", "cisco-vk-legacy-", "oldObject.spec.nodeName", "object.spec == oldObject.spec", "workerServiceAccount", "request.userInfo.username"},
		digest:            "sha256:0a2d27b4e3eb3c6051b1068173448a1b81920b97fc8a4f723b4d0ec01af9bea9",
	},
	"managed-pod-delete": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods"},
		operations: []admissionv1.OperationType{admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"generated-worker"}, variables: []string{"usernameParts", "workerServiceAccount"}, validations: 4, coreTyped: true,
		requiredFragments: []string{"cisco-vk-managed-", "cisco-vk-legacy-", "oldObject.metadata.deletionTimestamp", "oldObject.spec.nodeName", "workerServiceAccount", "request.userInfo.username", "request.options.preconditions.uid", "request.options.gracePeriodSeconds"},
		digest:            "sha256:f8fee4c9b2d450fc105adb62b955d555ed98c52d036966a0015384e628a3a01f",
	},
	"managed-drain-pod": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"drain-metadata"},
		variables:       []string{"manager", "oldSession", "newSession", "oldFinalizers", "newFinalizers"},
		validations:     3, coreTyped: true,
		requiredFragments: []string{"drain-session", "iosxe-rollout-drain", "request.userInfo.username", "object.spec == oldObject.spec", "metadata.finalizers"},
		digest:            "sha256:ae625c5437f318ac3ea9d6116be90768ae2cdc774be71f8e6ad2f00eb0313355",
	},
	"managed-device": {
		apiGroups: []string{"cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"ciscodevices", "ciscodevices/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		variables:         []string{"manager", "newProtectedLabels", "oldProtectedLabels", "newProtectedAnnotations", "oldProtectedAnnotations", "newDrainCordonHold", "oldDrainCordonHold"},
		validations:       14,
		requiredFragments: []string{"drain-cordon-hold", "check('topology')", "nodeIdentity", "topologyProjection", "topologyLock", "maintenanceSession", "distribution.cisco.vk/", "request-legacy-handoff", "isolated-legacy-worker", "legacyHandoff", "SharedWriterPending", "healthObservation", "workerRevision", "networkWorkerRevision", "request.subResource", "object.spec == oldObject.spec", "object.spec.labels == oldObject.spec.labels", "object.spec.taints == oldObject.spec.taints", "object.spec.maxPods", "object.spec.maxPods <= 110", "ownerReferences", "finalizers", "cisco.vk/device-cleanup", "oldObject.status.legacyHandoff.phase == 'Complete'"},
		digest:            "sha256:351c7b22f077b3fe1b345ee0407680ec9b730d26bc5fbe8ac9ae63c016698777",
	},
	"managed-rollout": {
		apiGroups: []string{"ops.cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"iosxesoftwarerollouts", "iosxesoftwarerollouts/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		variables:         []string{"manager"},
		validations:       6,
		requiredFragments: []string{"requestedBy", "check('approve')", "planHash", "check('control')", "request.subResource != 'status'", "spec.control.revision == 0"},
		digest:            "sha256:d9dd48735236cddeb73385993cafc67da0cb5177cd9e2482c056b4472665e0f4",
	},
	"managed-upgrade-leaf": {
		apiGroups: []string{"ops.cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"iosxesoftwareupgrades", "iosxesoftwareupgrades/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions:   []string{"managed-leaf"},
		variables:         []string{"manager", "appDrainWriter", "oldClaims", "newClaims", "managerFunctionalWorkerBinding"},
		validations:       8,
		requiredFragments: []string{"worker-username", "network-worker-username", "iosxesoftwareupgrade-cleanup", "managerAdmission", "managerControl", "managerDrain", "workerDrain", "managedMutationClaims", "primarySupervisorInstallRequested", "reservationID", "policyEpoch", "topologyLockID", "observedWorkerConfigRevision"},
		digest:            "sha256:b20d64d81c79a07900f62927c2420feb9cc489fb463763a3989ffc48bf8d2bdf",
	},
	"topology-policy": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"configmaps"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"chart-policy"}, variables: []string{"manager", "objectName", "namespaceCleanup", "policyEditor"}, validations: 5, coreTyped: true,
		requiredFragments: []string{"managed-policy", "admission-policy-prefix", "check('topology')", "ledger-uid", "request.namespace", "request.name", "oldObject.metadata.name", "namespace-controller", "app-hosting-service-account", "network-management-service-account", "config-lease-namespace"},
		digest:            "sha256:62f1a1cb22497d1c6faae5e29d5e7b214b5ca91f398bf03d409f8aa7f8be501a",
	},
	"topology-ledger": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"configmaps"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"chart-ledger"}, variables: []string{"manager", "objectName", "namespaceCleanup", "breakglass"}, validations: 4, coreTyped: true,
		requiredFragments: []string{"request.operation != 'UPDATE'", "managed-ledger", "ledger.json", "check('manage-ledger')", "request.namespace", "request.name", "oldObject.metadata.name", "namespace-controller"},
		digest:            "sha256:5e40bbae27af40185a5cabd34587a12b086061d2d632715328799010c005fa19",
	},
	"managed-maintenance-lease": {
		apiGroups: []string{"coordination.k8s.io"}, apiVersions: []string{"v1"}, resources: []string{"leases"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"managed-maintenance-request"},
		variables:       []string{"manager", "oldRequest", "newRequest", "oldHeld", "newHeld", "oldTransitions", "managerCreate", "managerAdopt", "managerRebind", "boundWorker", "holderChanged"},
		validations:     8, coreTyped: true,
		requiredFragments: []string{"maintenance-request-version", "maintenance-session-token", "maintenance-operation-uid", "maintenance-control-revision", "maintenance-purpose", "software-drain", "worker-username", "app-worker-username", "network-worker-username", "holderIdentity", "device-uid"},
		digest:            "sha256:0d4abc14a64c22d25a9e43f8a08ec0107de9b754f36a50fb6f59dfc53180db7d",
	},
	"shared-pod-status": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods/status"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"shared-app-worker"}, variables: []string{"podUIDs", "podNames"}, validations: 2, coreTyped: true,
		requiredFragments: []string{"app-worker-username", "app-worker-pod-name", "app-worker-pod-uid", "authentication.kubernetes.io/pod-uid", "object.spec == oldObject.spec"},
		digest:            "sha256:024f68f192cad0323977779a564ce4267b3eba7f291a895980333d5ff2f24c77",
	},
	"shared-pod-binding": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods", "pods/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"shared-binding"}, variables: []string{"manager", "oldBindings", "newBindings"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"app-worker-username", "app-worker-pod-name", "app-worker-pod-uid", "object.status == oldObject.status"},
		digest:            "sha256:5db4a5bf041c8213c935831c2a75e1746943a85eae25d595618a2f9b7eb1a7b6",
	},
	"shared-pod-delete": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods"},
		operations: []admissionv1.OperationType{admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"shared-app-worker"}, variables: []string{"podUIDs", "podNames"}, validations: 2, coreTyped: true,
		requiredFragments: []string{"preconditions.uid", "gracePeriodSeconds", "deletionTimestamp", "app-worker-pod-uid", "authentication.kubernetes.io/pod-name"},
		digest:            "sha256:5f04c0132efb534843ef60505a6d66cd35b64c79ad00ccabc7a07bcffff84c45",
	},
	"shared-node-status": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"nodes/status"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.ClusterScope,
		matchConditions: []string{"shared-app-worker"}, variables: []string{"podUIDs", "podNames"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"app-worker-username", "app-worker-pod-name", "app-worker-pod-uid", "authentication.kubernetes.io/pod-uid"},
		digest:            "sha256:fec50e3f2c0c6c8c8b80c2d2c44acfce3fb76a897856f71492437222dac2ce3a",
	},
	"shared-upgrade-leaf": {
		apiGroups: []string{"ops.cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"iosxesoftwareupgrades", "iosxesoftwareupgrades/status"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"shared-network-worker"}, variables: []string{"plane", "podUIDs", "podNames"}, validations: 1,
		requiredFragments: []string{"variables.plane", "worker-username", "worker-pod-name", "worker-pod-uid", "authentication.kubernetes.io/pod-uid"},
		digest:            "sha256:d6b4f6b4517a3bd1df4c82c2735366572a4db97413188b54159b1b75e40dfdc4",
	},
	"shared-network-object": {
		apiGroups: []string{"config.cisco.vk", "ops.cisco.vk"}, apiVersions: []string{"v1alpha1"},
		resources:  []string{"iosxeconfigs", "iosxeconfigs/status", "nxosconfigs", "nxosconfigs/status", "iosxetelemetries", "iosxetelemetries/status", "iosxediagnostics", "iosxediagnostics/status", "iosxeconfigapplylogs", "iosxeconfigapplylogs/status", "iosxeconfigrevisions", "iosxeconfigrevisions/status", "deviceoperations", "deviceoperations/status", "iosxeoperationalactions", "iosxeoperationalactions/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"protected-network-object"}, variables: []string{"manager", "sharedWorker", "nativeGarbageCollector", "nativeNamespaceCleanup", "podUIDs", "podNames", "oldBindings", "newBindings", "boundObject", "oldBindingComplete"}, validations: 6,
		requiredFragments: []string{"deviceRef.name", "device-uid", "network-worker-pod-name", "authentication.kubernetes.io/pod-uid", "-network-", "object.spec == oldObject.spec", "object.status == oldObject.status", "config.cisco.vk/lease-cleanup", "config.cisco.vk/telemetry-cleanup", "ops.cisco.vk/iosxeoperationalaction-finalizer", "iosxeconfigrevisions", "IOSXEConfigBundle", "blockOwnerDeletion", "system:serviceaccount:kube-system:generic-garbage-collector", "namespace-controller", "system:kube-controller-manager", "request.userInfo.groups", "request.options.preconditions.uid"},
		digest:            "sha256:593f4d8e2a1b46fb12055bd4b4a079671b03b068ece8ec7e74e87a1d6ddcad58",
	},
	"shared-network-result": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"configmaps"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"protected-network-result"}, variables: []string{"manager", "sharedWorker", "nativeGarbageCollector", "nativeNamespaceCleanup", "podUIDs", "podNames", "oldBindings", "newBindings", "boundObject"}, validations: 9, coreTyped: true,
		requiredFragments: []string{"device-uid", "network-worker-pod-name", "authentication.kubernetes.io/pod-name", "IOSXEDiagnostic", "DeviceOperation", "cisco.vk/diagnostic-uid", "object.data == oldObject.data", "result-u", "system:serviceaccount:kube-system:generic-garbage-collector", "namespace-controller", "system:kube-controller-manager", "request.userInfo.groups", "request.options.preconditions.uid"},
		digest:            "sha256:8b50fd531c0519d75a0e6bba3785ce9240ae39e3d5d42717fed35cb41611467d",
	},
	"shared-maintenance-lease": {
		apiGroups: []string{"coordination.k8s.io"}, apiVersions: []string{"v1"}, resources: []string{"leases"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"shared-functional-worker"}, variables: []string{"podUIDs", "podNames", "appWorker", "networkWorker"}, validations: 2, coreTyped: true,
		requiredFragments: []string{"node-heartbeat", "config-family", "device-mutation", "app-worker-pod-uid", "network-worker-pod-uid", "authentication.kubernetes.io/pod-name"},
		digest:            "sha256:39bfaca691b5d90bcc743b1a522db3fcb69982b725f6f25911f2592714b06dd4",
	},
	"shared-worker-serviceaccount": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"serviceaccounts"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"request.name", "oldObject.metadata.name", "namespace-controller", "request.userInfo.username"},
		digest:            "sha256:345ef258b702eda8a1f4f609dbbe53b43cf5a05fc4f4242c860a3e2b66145a21",
	},
	"generated-worker-serviceaccount": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"serviceaccounts"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"generated-worker-account"}, validations: 2, coreTyped: true,
		requiredFragments: []string{"request.name", "oldObject.metadata.name", "namespace-controller", "request.userInfo.username", "cisco-vk-(managed|legacy)-", "worker-protocol", "CiscoDevice", "ownerReferences", "blockOwnerDeletion"},
		digest:            "sha256:75937a46b672683cbec30b2c3fea101198520653f574e30b9c00fd78bd45b234",
	},
	"shared-worker-token": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"serviceaccounts/token"},
		operations: []admissionv1.OperationType{admissionv1.Create}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"request.name", "system:node:", "system:authenticated", "system:nodes", "boundObjectRef", "Pod", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:f971cf1df70c65cbb07bdc9ced73226d93068fa72fe3521178c53b829de0815d",
	},
	"shared-worker-token-secret": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"secrets"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-token-secret"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"kubernetes.io/service-account-token", "kubernetes.io/service-account.name", "request.operation == 'DELETE'", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:a6cbb20a7cf07e5625e65fb0474acf666ab71246737b4516849b178026f49926",
	},
	"shared-worker-deployment": {
		apiGroups: []string{"apps"}, apiVersions: []string{"v1"}, resources: []string{"deployments", "deployments/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, variables: []string{"nativeForegroundCleanup", "manager", "namespaceCleanup", "nativeDeploymentMetadata", "nativeDeploymentStatus"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"serviceAccountName", "namespace-controller", "!has(request.name)", "deployment-controller", "deployment.kubernetes.io/revision", "request.subResource == 'status'", "system:authenticated", "object.metadata.uid == oldObject.metadata.uid", "object.spec == oldObject.spec", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:4fef309c1ef90eb17cfd6489a7f8c9b844256cd8cb5d6aff1d43ce01decd9ac0",
	},
	"shared-worker-replicaset": {
		apiGroups: []string{"apps"}, apiVersions: []string{"v1"}, resources: []string{"replicasets", "replicasets/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, variables: []string{"nativeForegroundCleanup"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"deployment-controller", "replicaset-controller", "namespace-controller", "!has(request.name)", "request.subResource == 'status'", "system:authenticated", "generic-garbage-collector", "request.options.preconditions.uid", "ownerReferences", "object.metadata.uid == oldObject.metadata.uid", "object.spec == oldObject.spec", "serviceAccountName", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:f4c6a5df3e4e0f0dca84f2d06a2253e429143a07ab0e3cf1352dfccb3044b3d3",
	},
	"shared-worker-pod": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"replicaset-controller", "namespace-controller", "!has(request.name)", "system:node:", "gracePeriodSeconds", "generic-garbage-collector", "request.options.preconditions.uid", "ownerReferences", "serviceAccountName", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:30c1032a48f7453f0953c5e2d92ef2d386ba2a5cbf7148e42f634a28bb193e33",
	},
	"shared-worker-pod-update": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods", "pods/status", "pods/ephemeralcontainers", "pods/resize"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"reserved-worker-account"}, variables: []string{"nativeForegroundCleanup", "manager", "nativeKubeletStatus"}, validations: 1, coreTyped: true,
		requiredFragments: []string{"serviceAccountName", "request.subResource == 'status'", "oldObject.spec.nodeName", "system:node:", "system:nodes", "system:authenticated", "object.metadata.annotations == oldObject.metadata.annotations", "object.spec == oldObject.spec", "cisco-vk-(managed|legacy)-"},
		digest:            "sha256:0637fde3c5495a98de8a86da1f1902ed359268d2f08a611c08e5395e6d8fb8bd",
	},
}

type managedAdmissionContractVerification struct {
	WorkerServiceAccountPolicyEpoch string
}

type workerServiceAccountAdmissionGeneration struct {
	suffix            string
	policyUID         types.UID
	policyGeneration  int64
	policySpecDigest  string
	bindingUID        types.UID
	bindingGeneration int64
	bindingSpecDigest string
}

var workerServiceAccountPolicyEpochSuffixes = []string{
	"shared-worker-serviceaccount",
	"generated-worker-serviceaccount",
	"shared-worker-token",
	"shared-worker-token-secret",
	"shared-worker-deployment",
	"shared-worker-replicaset",
	"shared-worker-pod",
	"shared-worker-pod-update",
}

// deriveWorkerServiceAccountPolicyEpoch intentionally excludes
// resourceVersion and all labels/annotations. It changes only when one of the
// verified ownership, token-issuance, token-Secret, or workload policy/binding
// incarnations changes, its Spec generation changes, or the binary's compiled
// policy contract changes.
func deriveWorkerServiceAccountPolicyEpoch(generations []workerServiceAccountAdmissionGeneration) (string, error) {
	want := make(map[string]struct{}, len(workerServiceAccountPolicyEpochSuffixes))
	for _, suffix := range workerServiceAccountPolicyEpochSuffixes {
		want[suffix] = struct{}{}
	}
	if len(generations) != len(want) {
		return "", fmt.Errorf("worker ServiceAccount admission epoch requires exactly %d verified policy/binding generations", len(want))
	}
	generations = append([]workerServiceAccountAdmissionGeneration(nil), generations...)
	sort.Slice(generations, func(i, j int) bool { return generations[i].suffix < generations[j].suffix })
	// Rotate prior accounts/workloads before adopting the UID-first network
	// Pod naming contract. Planned network rotation still waits for durable
	// mutation settlement; old and replacement workers must never overlap.
	fields := []string{"worker-serviceaccount-policy-epoch-v2-uid-first-network"}
	for _, generation := range generations {
		if _, ok := want[generation.suffix]; !ok {
			return "", fmt.Errorf("unexpected worker ServiceAccount admission policy suffix %q", generation.suffix)
		}
		delete(want, generation.suffix)
		if generation.policyUID == "" || generation.policyGeneration <= 0 || generation.policySpecDigest == "" ||
			generation.bindingUID == "" || generation.bindingGeneration <= 0 || generation.bindingSpecDigest == "" {
			return "", fmt.Errorf("worker ServiceAccount admission policy %q lacks an immutable UID, positive generation, or compiled Spec digest", generation.suffix)
		}
		fields = append(fields,
			generation.suffix,
			string(generation.policyUID), strconv.FormatInt(generation.policyGeneration, 10), generation.policySpecDigest,
			string(generation.bindingUID), strconv.FormatInt(generation.bindingGeneration, 10), generation.bindingSpecDigest,
		)
	}
	if len(want) != 0 {
		return "", fmt.Errorf("worker ServiceAccount admission epoch is missing a required policy/binding generation")
	}
	digest := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func contributesToWorkerServiceAccountPolicyEpoch(suffix string) bool {
	for _, candidate := range workerServiceAccountPolicyEpochSuffixes {
		if suffix == candidate {
			return true
		}
	}
	return false
}

func managedAdmissionBindingSpecDigest(binding *admissionv1.ValidatingAdmissionPolicyBinding) (string, error) {
	if binding == nil {
		return "", fmt.Errorf("compiled admission policy binding is nil")
	}
	encoded, err := json.Marshal(binding.Spec)
	if err != nil {
		return "", fmt.Errorf("encode compiled admission binding contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// verifyManagedAdmissionContract refuses to start a managed-topology manager
// unless every native policy is compiled for its current generation and every
// exact binding enforces Deny. A missing policy must never degrade this
// privileged mode to ordinary RBAC alone.
func verifyManagedAdmissionContract(
	ctx context.Context,
	cfg *rest.Config,
	prefix string,
	policyKey types.NamespacedName,
	ledgerName string,
	appHostingServiceAccount string,
	networkManagementServiceAccount string,
) (managedAdmissionContractVerification, error) {
	var verification managedAdmissionContractVerification
	if prefix == "" {
		return verification, fmt.Errorf("managed admission policy prefix is empty")
	}
	if policyKey.Namespace == "" || policyKey.Name == "" || ledgerName == "" {
		return verification, fmt.Errorf("managed admission policy bindings are incomplete")
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return verification, fmt.Errorf("create admission preflight client: %w", err)
	}
	self, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(
		ctx,
		&authenticationv1.SelfSubjectReview{},
		metav1.CreateOptions{},
	)
	if err != nil {
		return verification, fmt.Errorf("resolve authenticated manager identity: %w", err)
	}
	contractBindings := admissionContractBindings{
		AdmissionPrefix:                 prefix,
		ManagerUsername:                 self.Status.UserInfo.Username,
		PolicyNamespace:                 policyKey.Namespace,
		PolicyName:                      policyKey.Name,
		LedgerName:                      ledgerName,
		AppHostingServiceAccount:        appHostingServiceAccount,
		NetworkManagementServiceAccount: networkManagementServiceAccount,
	}
	if err := contractBindings.validate(); err != nil {
		return verification, err
	}
	policies := clientset.AdmissionregistrationV1().ValidatingAdmissionPolicies()
	policyBindings := clientset.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings()
	workerAccountGenerations := make([]workerServiceAccountAdmissionGeneration, 0,
		len(workerServiceAccountPolicyEpochSuffixes))
	for _, suffix := range managedAdmissionPolicySuffixes {
		name := prefix + "-" + suffix
		policy, err := policies.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return verification, fmt.Errorf("get required ValidatingAdmissionPolicy %q: %w", name, err)
		}
		expectation, ok := managedAdmissionExpectations[suffix]
		if !ok {
			return verification, fmt.Errorf("no compiled admission expectation for %q", suffix)
		}
		if err := validateManagedAdmissionPolicy(policy, expectation); err != nil {
			return verification, fmt.Errorf("ValidatingAdmissionPolicy %q: %w", name, err)
		}
		if err := validateManagedAdmissionPolicyDigest(policy, expectation, contractBindings); err != nil {
			return verification, fmt.Errorf("ValidatingAdmissionPolicy %q: %w", name, err)
		}

		binding, err := policyBindings.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return verification, fmt.Errorf("get required ValidatingAdmissionPolicyBinding %q: %w", name, err)
		}
		if err := validateManagedAdmissionBinding(binding, name); err != nil {
			return verification, fmt.Errorf("ValidatingAdmissionPolicyBinding %q: %w", name, err)
		}
		if expectation.publishDigest &&
			binding.Annotations[managedprotocol.AnnotationAdmissionContractDigest] != expectation.digest {
			return verification, fmt.Errorf("ValidatingAdmissionPolicyBinding %q: published contract digest does not match %s", name, expectation.digest)
		}
		if contributesToWorkerServiceAccountPolicyEpoch(suffix) {
			bindingDigest, err := managedAdmissionBindingSpecDigest(binding)
			if err != nil {
				return verification, fmt.Errorf("ValidatingAdmissionPolicyBinding %q: %w", name, err)
			}
			workerAccountGenerations = append(workerAccountGenerations, workerServiceAccountAdmissionGeneration{
				suffix: suffix, policyUID: policy.UID, policyGeneration: policy.Generation,
				policySpecDigest: expectation.digest, bindingUID: binding.UID,
				bindingGeneration: binding.Generation, bindingSpecDigest: bindingDigest,
			})
		}
	}
	verification.WorkerServiceAccountPolicyEpoch, err = deriveWorkerServiceAccountPolicyEpoch(workerAccountGenerations)
	if err != nil {
		return verification, err
	}
	return verification, nil
}

type admissionContractBindings struct {
	AdmissionPrefix                 string
	ManagerUsername                 string
	PolicyNamespace                 string
	PolicyName                      string
	LedgerName                      string
	AppHostingServiceAccount        string
	NetworkManagementServiceAccount string
}

func (b admissionContractBindings) validate() error {
	bindings := []struct{ name, value string }{
		{"admission policy prefix", b.AdmissionPrefix},
		{"authenticated manager username", b.ManagerUsername},
		{"policy namespace", b.PolicyNamespace},
		{"policy name", b.PolicyName},
		{"ledger name", b.LedgerName},
		{"app-hosting ServiceAccount", b.AppHostingServiceAccount},
		{"network-management ServiceAccount", b.NetworkManagementServiceAccount},
	}
	seen := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if strings.TrimSpace(binding.value) == "" {
			return fmt.Errorf("managed admission %s is empty", binding.name)
		}
		if previous, found := seen[binding.value]; found {
			return fmt.Errorf("managed admission contract bindings must be pairwise distinct: %s and %s both resolve to %q",
				previous, binding.name, binding.value)
		}
		seen[binding.value] = binding.name
	}
	return nil
}

const (
	contractAdmissionPrefixToken       = "${CVK_ADMISSION_PREFIX}"
	contractManagerToken               = "${CVK_MANAGER_USERNAME}"
	contractPolicyNamespaceToken       = "${CVK_POLICY_NAMESPACE}"
	contractPolicyNameToken            = "${CVK_POLICY_NAME}"
	contractLedgerNameToken            = "${CVK_LEDGER_NAME}"
	contractAppServiceAccountToken     = "${CVK_APP_SERVICE_ACCOUNT}"
	contractNetworkServiceAccountToken = "${CVK_NETWORK_SERVICE_ACCOUNT}"
)

func validateManagedAdmissionPolicyDigest(
	policy *admissionv1.ValidatingAdmissionPolicy,
	expected admissionContractExpectation,
	bindings admissionContractBindings,
) error {
	if expected.digest == "" {
		return fmt.Errorf("compiled contract digest is absent")
	}
	actual, err := managedAdmissionPolicyDigest(policy, bindings)
	if err != nil {
		return err
	}
	if actual != expected.digest {
		return fmt.Errorf("compiled contract digest is %s, want %s", actual, expected.digest)
	}
	if expected.publishDigest && policy.Annotations[managedprotocol.AnnotationAdmissionContractDigest] != expected.digest {
		return fmt.Errorf("published contract digest does not match %s", expected.digest)
	}
	return nil
}

// managedAdmissionPolicyDigest canonicalizes only the chart-dependent literal
// identities, then hashes the entire typed policy Spec. Counts and substrings
// are useful diagnostics, but this digest is the fail-closed proof that a CEL
// expression, validation message, selector, rule, or other policy field was
// not narrowed or otherwise changed after the matching binary was built.
func managedAdmissionPolicyDigest(
	policy *admissionv1.ValidatingAdmissionPolicy,
	bindings admissionContractBindings,
) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("compiled admission policy is nil")
	}
	if err := bindings.validate(); err != nil {
		return "", err
	}
	spec := policy.DeepCopy().Spec
	// API-server defaulting materializes omitted selectors as empty objects.
	// Empty and absent both match all resources; canonicalize only that semantic
	// no-op so the same chart contract hashes identically before and after a
	// real API round trip. Non-empty selectors remain part of the digest and are
	// rejected below as narrowing.
	if spec.MatchConstraints != nil {
		if labelSelectorEmpty(spec.MatchConstraints.NamespaceSelector) {
			spec.MatchConstraints.NamespaceSelector = nil
		}
		if labelSelectorEmpty(spec.MatchConstraints.ObjectSelector) {
			spec.MatchConstraints.ObjectSelector = nil
		}
	}
	replacements := []struct{ from, to string }{
		{bindings.AdmissionPrefix, contractAdmissionPrefixToken},
		{bindings.ManagerUsername, contractManagerToken},
		{bindings.PolicyNamespace, contractPolicyNamespaceToken},
		{bindings.PolicyName, contractPolicyNameToken},
		{bindings.LedgerName, contractLedgerNameToken},
		{bindings.AppHostingServiceAccount, contractAppServiceAccountToken},
		{bindings.NetworkManagementServiceAccount, contractNetworkServiceAccountToken},
	}
	normalize := func(expression string) string {
		return normalizeManagedAdmissionCELLiterals(expression, replacements)
	}
	for i := range spec.MatchConditions {
		spec.MatchConditions[i].Expression = normalize(spec.MatchConditions[i].Expression)
	}
	for i := range spec.Variables {
		spec.Variables[i].Expression = normalize(spec.Variables[i].Expression)
	}
	for i := range spec.Validations {
		spec.Validations[i].Expression = normalize(spec.Validations[i].Expression)
		spec.Validations[i].MessageExpression = normalize(spec.Validations[i].MessageExpression)
	}
	for i := range spec.AuditAnnotations {
		spec.AuditAnnotations[i].ValueExpression = normalize(spec.AuditAnnotations[i].ValueExpression)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode compiled admission contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// normalizeManagedAdmissionCELLiterals canonicalizes only chart-bound CEL
// string literals. A raw substring replacement is unsafe here: valid short
// identities such as "managed" also occur inside fixed annotation keys and
// would make the live chart fail its own compiled-contract preflight.
//
// Helm's quote function emits configurable identities as double-quoted CEL
// literals. The two worker account names also occur as the complete suffix of
// a single-quoted ServiceAccount username literal. Restrict replacements to
// those exact forms so fixed single-quoted contract vocabulary remains part of
// the digest.
func normalizeManagedAdmissionCELLiterals(expression string, replacements []struct{ from, to string }) string {
	type literalReplacement struct {
		quote    byte
		from, to string
	}
	literals := make([]literalReplacement, 0, len(replacements)+2)
	for _, replacement := range replacements {
		literals = append(literals, literalReplacement{quote: '"', from: replacement.from, to: replacement.to})
	}
	for _, replacement := range replacements[len(replacements)-2:] {
		literals = append(literals, literalReplacement{
			quote: '\'', from: ":" + replacement.from, to: ":" + replacement.to,
		})
	}

	var normalized strings.Builder
	normalized.Grow(len(expression))
	for offset := 0; offset < len(expression); {
		quote := expression[offset]
		if quote != '\'' && quote != '"' {
			normalized.WriteByte(expression[offset])
			offset++
			continue
		}

		end := offset + 1
		for end < len(expression) {
			if expression[end] == '\\' {
				end += 2
				continue
			}
			if expression[end] == quote {
				break
			}
			end++
		}
		if end >= len(expression) {
			// The API server rejects malformed CEL before this preflight runs.
			// Preserve the tail verbatim so digest diagnostics still describe the
			// object actually read rather than silently repairing it.
			normalized.WriteString(expression[offset:])
			break
		}

		literal := expression[offset+1 : end]
		for _, replacement := range literals {
			if quote == replacement.quote && literal == replacement.from {
				literal = replacement.to
				break
			}
		}
		normalized.WriteByte(quote)
		normalized.WriteString(literal)
		normalized.WriteByte(quote)
		offset = end + 1
	}
	return normalized.String()
}

func validateManagedAdmissionPolicy(policy *admissionv1.ValidatingAdmissionPolicy, expected admissionContractExpectation) error {
	if policy.Annotations[managedprotocol.AnnotationAdmissionContractVersion] != managedprotocol.AdmissionContractVersion {
		return fmt.Errorf("missing compiled admission contract version %q", managedprotocol.AdmissionContractVersion)
	}
	if policy.Spec.FailurePolicy == nil || *policy.Spec.FailurePolicy != admissionv1.Fail {
		return fmt.Errorf("failurePolicy must be Fail")
	}
	if policy.Spec.ParamKind != nil {
		return fmt.Errorf("paramKind must be absent")
	}
	if policy.Spec.MatchConstraints == nil || policy.Spec.MatchConstraints.MatchPolicy == nil ||
		*policy.Spec.MatchConstraints.MatchPolicy != admissionv1.Equivalent ||
		!labelSelectorEmpty(policy.Spec.MatchConstraints.NamespaceSelector) ||
		!labelSelectorEmpty(policy.Spec.MatchConstraints.ObjectSelector) ||
		len(policy.Spec.MatchConstraints.ExcludeResourceRules) != 0 || len(policy.Spec.MatchConstraints.ResourceRules) != 1 {
		return fmt.Errorf("match constraints differ from the compiled fail-closed contract")
	}
	rule := policy.Spec.MatchConstraints.ResourceRules[0]
	if len(rule.ResourceNames) != 0 || !sameStrings(rule.Rule.APIGroups, expected.apiGroups) || !sameStrings(rule.Rule.Resources, expected.resources) ||
		!sameOperations(rule.RuleWithOperations.Operations, expected.operations) || rule.Rule.Scope == nil || *rule.Rule.Scope != expected.scope ||
		!sameStrings(rule.Rule.APIVersions, expected.apiVersions) {
		return fmt.Errorf("resource rule differs from the compiled contract")
	}
	if !sameStrings(matchConditionNames(policy.Spec.MatchConditions), expected.matchConditions) {
		return fmt.Errorf("match conditions differ from the compiled contract")
	}
	if !sameStrings(variableNames(policy.Spec.Variables), expected.variables) {
		return fmt.Errorf("variables differ from the compiled contract")
	}
	if len(policy.Spec.Validations) != expected.validations {
		return fmt.Errorf("has %d validations, want exactly %d", len(policy.Spec.Validations), expected.validations)
	}
	var expressions strings.Builder
	for _, condition := range policy.Spec.MatchConditions {
		if expressionIsUnconditional(condition.Expression) {
			return fmt.Errorf("contains an unconditional match condition")
		}
		expressions.WriteString(condition.Expression)
		expressions.WriteByte('\n')
	}
	for _, variable := range policy.Spec.Variables {
		expressions.WriteString(variable.Expression)
		expressions.WriteByte('\n')
	}
	for _, validation := range policy.Spec.Validations {
		if expressionIsUnconditional(validation.Expression) {
			return fmt.Errorf("contains an unconditional validation")
		}
		expressions.WriteString(validation.Expression)
		expressions.WriteByte('\n')
	}
	contract := expressions.String()
	for _, fragment := range expected.requiredFragments {
		if !strings.Contains(contract, fragment) {
			return fmt.Errorf("compiled contract fragment %q is absent", fragment)
		}
	}
	if policy.Status.ObservedGeneration != policy.Generation {
		return fmt.Errorf("generation %d is not observed", policy.Generation)
	}
	// Kubernetes cannot type-check expressions against arbitrary CRD schemas.
	// Require warning-free completion only for built-in resources and cover the
	// CRD policies with real-apiserver denial tests.
	if expected.coreTyped {
		if policy.Status.TypeChecking == nil {
			return fmt.Errorf("type checking has not completed")
		}
		if len(policy.Status.TypeChecking.ExpressionWarnings) != 0 {
			return fmt.Errorf("has %d expression warning(s)", len(policy.Status.TypeChecking.ExpressionWarnings))
		}
	}
	return nil
}

func matchConditionNames(conditions []admissionv1.MatchCondition) []string {
	names := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		names = append(names, condition.Name)
	}
	return names
}

func variableNames(variables []admissionv1.Variable) []string {
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		names = append(names, variable.Name)
	}
	return names
}

func expressionIsUnconditional(expression string) bool {
	expression = strings.TrimSpace(strings.Trim(strings.TrimSpace(expression), "()"))
	return expression == "true" || expression == "false"
}

func validateManagedAdmissionBinding(binding *admissionv1.ValidatingAdmissionPolicyBinding, policyName string) error {
	if binding.Annotations[managedprotocol.AnnotationAdmissionContractVersion] != managedprotocol.AdmissionContractVersion {
		return fmt.Errorf("missing compiled admission contract version %q", managedprotocol.AdmissionContractVersion)
	}
	if binding.Spec.PolicyName != policyName || !reflect.DeepEqual(binding.Spec.ValidationActions, []admissionv1.ValidationAction{admissionv1.Deny}) ||
		binding.Spec.ParamRef != nil {
		return fmt.Errorf("must enforce only Deny for its exact unparameterized policy")
	}
	if resources := binding.Spec.MatchResources; resources != nil &&
		((resources.MatchPolicy != nil && *resources.MatchPolicy != admissionv1.Equivalent) ||
			!labelSelectorEmpty(resources.NamespaceSelector) || !labelSelectorEmpty(resources.ObjectSelector) ||
			len(resources.ResourceRules) != 0 || len(resources.ExcludeResourceRules) != 0) {
		return fmt.Errorf("must not narrow the policy match set")
	}
	return nil
}

func labelSelectorEmpty(selector *metav1.LabelSelector) bool {
	return selector == nil ||
		(len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0)
}

func sameStrings(left, right []string) bool {
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return reflect.DeepEqual(leftCopy, rightCopy)
}

func sameOperations(left, right []admissionv1.OperationType) bool {
	leftCopy, rightCopy := append([]admissionv1.OperationType(nil), left...), append([]admissionv1.OperationType(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	return reflect.DeepEqual(leftCopy, rightCopy)
}
