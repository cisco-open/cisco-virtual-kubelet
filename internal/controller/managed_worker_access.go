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

package controller

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

const (
	annotationSharedWorkerAccount   = "topology.cisco.vk/shared-worker-account"
	annotationSharedWorkerNamespace = "topology.cisco.vk/shared-worker-namespace"
	annotationSharedWorkerRole      = "topology.cisco.vk/shared-worker-role"
)

type sharedWorkerAccessTransition struct {
	planes   []string
	blockers []string
}

type sharedServiceAccountEpochTarget struct {
	plane          string
	serviceAccount string
}

func (e *sharedWorkerAccessTransition) Error() string {
	message := "shared worker access transition for " + strings.Join(e.planes, " and ")
	if len(e.blockers) != 0 {
		return message + " is blocked: " + strings.Join(e.blockers, "; ")
	}
	return message + " is waiting for all prior Deployments, ReplicaSets, and Pods to terminate"
}

func normalizeWorkerAccessMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "readwrite", "read-write", "rw":
		return managedprotocol.WorkerAccessReadWrite, nil
	case "readonly", "read-only", "ro":
		return managedprotocol.WorkerAccessReadOnly, nil
	case "disabled", "off", "none":
		return managedprotocol.WorkerAccessDisabled, nil
	default:
		return "", fmt.Errorf("unsupported worker access mode %q (expected readOnly, readWrite, or disabled)", value)
	}
}

func (r *CiscoDeviceReconciler) appHostingServiceAccountName() string {
	if name := strings.TrimSpace(r.AppHostingServiceAccount); name != "" {
		return name
	}
	return managedprotocol.AppHostingServiceAccount
}

func (r *CiscoDeviceReconciler) networkManagementServiceAccountName() string {
	if name := strings.TrimSpace(r.NetworkManagementServiceAccount); name != "" {
		return name
	}
	return managedprotocol.NetworkManagementServiceAccount
}

func (r *CiscoDeviceReconciler) appHostingDeviceReadRoleName() string {
	if role := strings.TrimSpace(r.AppHostingDeviceReadRole); role != "" {
		return role
	}
	return managedprotocol.AppHostingDeviceReadClusterRole
}

func (r *CiscoDeviceReconciler) managedWorkerProfiles() (appRole, appAccess, networkRole, networkAccess, globalReadRole string, err error) {
	appAccess, err = normalizeWorkerAccessMode(r.AppHostingAccessMode)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("app-hosting access: %w", err)
	}
	networkAccess, err = normalizeWorkerAccessMode(r.NetworkManagementAccessMode)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("network-management access: %w", err)
	}
	appRole = strings.TrimSpace(r.AppHostingClusterRole)
	if appRole == "" {
		appRole = managedprotocol.AppHostingReadWriteClusterRole
		if appAccess == managedprotocol.WorkerAccessReadOnly {
			appRole = managedprotocol.AppHostingReadOnlyClusterRole
		}
	}
	networkRole = strings.TrimSpace(r.NetworkManagementClusterRole)
	if networkRole == "" {
		networkRole = managedprotocol.NetworkManagementReadWriteClusterRole
		if networkAccess == managedprotocol.WorkerAccessReadOnly {
			networkRole = managedprotocol.NetworkManagementReadOnlyClusterRole
		}
	}
	globalReadRole = strings.TrimSpace(r.NetworkManagementGlobalReadRole)
	if globalReadRole == "" {
		globalReadRole = managedprotocol.NetworkManagementGlobalReadClusterRole
	}
	expectedAppRole := managedprotocol.AppHostingReadWriteClusterRole
	if appAccess == managedprotocol.WorkerAccessReadOnly {
		expectedAppRole = managedprotocol.AppHostingReadOnlyClusterRole
	}
	if appAccess != managedprotocol.WorkerAccessDisabled && appRole != expectedAppRole {
		return "", "", "", "", "", fmt.Errorf("app-hosting access %s requires fixed profile role %q, got %q",
			appAccess, expectedAppRole, appRole)
	}
	expectedNetworkRole := managedprotocol.NetworkManagementReadWriteClusterRole
	if networkAccess == managedprotocol.WorkerAccessReadOnly {
		expectedNetworkRole = managedprotocol.NetworkManagementReadOnlyClusterRole
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled && networkRole != expectedNetworkRole {
		return "", "", "", "", "", fmt.Errorf("network-management access %s requires fixed profile role %q, got %q",
			networkAccess, expectedNetworkRole, networkRole)
	}
	if globalReadRole != managedprotocol.NetworkManagementGlobalReadClusterRole {
		return "", "", "", "", "", fmt.Errorf("network-management global-read role must be fixed to %q, got %q",
			managedprotocol.NetworkManagementGlobalReadClusterRole, globalReadRole)
	}
	return appRole, appAccess, networkRole, networkAccess, globalReadRole, nil
}

func sharedWorkerAnnotations(namespace, account, role string) map[string]string {
	return map[string]string{
		annotationSharedWorkerAccount:            account,
		annotationSharedWorkerNamespace:          namespace,
		annotationSharedWorkerRole:               role,
		managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version,
	}
}

func sharedWorkerMetadataMatches(meta metav1.Object, namespace, account, role string) bool {
	if meta == nil || len(meta.GetOwnerReferences()) != 0 {
		return false
	}
	want := sharedWorkerAnnotations(namespace, account, role)
	for key, value := range want {
		if meta.GetAnnotations()[key] != value {
			return false
		}
	}
	return true
}

func applySharedWorkerAnnotations(meta metav1.Object, namespace, account, role string) {
	annotations := meta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	for key, value := range sharedWorkerAnnotations(namespace, account, role) {
		annotations[key] = value
	}
	meta.SetAnnotations(annotations)
}

func sharedWorkerSubject(namespace, name string) []rbacv1.Subject {
	return []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Namespace: namespace, Name: name}}
}

func networkLeaseRoleBindingName(accountNamespace, serviceAccount string) string {
	return serviceAccount + "-leases-" + shortHash(accountNamespace+"/"+serviceAccount)
}

func sharedWorkerRoleAllowed(account, role string) bool {
	switch account {
	case managedprotocol.WorkerModeAppHosting:
		return role == managedprotocol.AppHostingReadOnlyClusterRole ||
			role == managedprotocol.AppHostingReadWriteClusterRole ||
			role == managedprotocol.AppHostingDeviceReadClusterRole
	case managedprotocol.WorkerModeNetworkManagement:
		return role == managedprotocol.NetworkManagementReadOnlyClusterRole ||
			role == managedprotocol.NetworkManagementReadWriteClusterRole ||
			role == managedprotocol.NetworkManagementGlobalReadClusterRole ||
			role == managedprotocol.NetworkManagementLeaseReadOnlyClusterRole ||
			role == managedprotocol.NetworkManagementLeaseReadWriteClusterRole
	default:
		return false
	}
}

func networkManagementLeaseRole(access string) string {
	if access == managedprotocol.WorkerAccessReadOnly {
		return managedprotocol.NetworkManagementLeaseReadOnlyClusterRole
	}
	return managedprotocol.NetworkManagementLeaseReadWriteClusterRole
}

// ensureManagedSharedWorkerAccess installs exactly two reusable identities in
// the CiscoDevice namespace. The app identity needs a cluster binding for Node
// and cross-namespace workload Pod status. Network mutations remain namespace
// scoped; only its read-only inventory role is cluster bound.
func (r *CiscoDeviceReconciler) ensureManagedSharedWorkerAccess(ctx context.Context, device *ciskov1.CiscoDevice) error {
	appRole, appAccess, networkRole, networkAccess, globalReadRole, err := r.managedWorkerProfiles()
	if err != nil {
		return err
	}
	appSA := r.appHostingServiceAccountName()
	networkSA := r.networkManagementServiceAccountName()
	appDeviceRole := r.appHostingDeviceReadRoleName()
	networkLeaseRole := networkManagementLeaseRole(networkAccess)
	if appDeviceRole != managedprotocol.AppHostingDeviceReadClusterRole {
		return fmt.Errorf("app-hosting device-read role must be fixed to %q, got %q",
			managedprotocol.AppHostingDeviceReadClusterRole, appDeviceRole)
	}
	if appSA == networkSA {
		return fmt.Errorf("app-hosting and network-management ServiceAccounts must be different")
	}
	// Namespace-scoped editors can otherwise exec into a worker, scale it to
	// preserve an old projected token, or impersonate a reusable identity. Run
	// this audit before creating or binding either shared ServiceAccount. Any
	// failure also revokes exact existing shared grants, so a newly introduced
	// RoleBinding cannot leave a previously authorized worker exposed.
	if err := r.enforceManagedWorkerNamespaceRBAC(ctx, device, appSA, networkSA); err != nil {
		return err
	}
	epochPending, epochBlockers, err := r.prepareSharedServiceAccountPolicyEpoch(ctx, device, []sharedServiceAccountEpochTarget{
		{plane: managedprotocol.WorkerModeAppHosting, serviceAccount: appSA},
		{plane: managedprotocol.WorkerModeNetworkManagement, serviceAccount: networkSA},
	})
	if err != nil {
		return err
	}
	// App-hosting and network-management use independent identities. An active
	// network mutation may fence only the network epoch rotation; it must not
	// keep an already-retired app identity unavailable for the lifetime of that
	// mutation. Wait here only while the app plane itself is still draining.
	if sharedWorkerPlanePending(epochPending, managedprotocol.WorkerModeAppHosting) {
		return &sharedWorkerAccessTransition{planes: epochPending, blockers: epochBlockers}
	}
	if err := r.validateManagedWorkerClusterRoles(ctx, device.Namespace, appRole, appAccess,
		appDeviceRole, networkRole, networkLeaseRole, networkAccess, globalReadRole); err != nil {
		return err
	}
	profilePending, profileBlockers, err := r.prepareSharedWorkerAccessTransitions(ctx, device, appRole, appAccess,
		appDeviceRole, networkRole, networkLeaseRole, networkAccess)
	if err != nil {
		return err
	}
	pendingPlanes := appendUniqueStrings(epochPending, profilePending...)
	blockers := append(epochBlockers, profileBlockers...)
	if sharedWorkerPlanePending(pendingPlanes, managedprotocol.WorkerModeAppHosting) {
		return &sharedWorkerAccessTransition{planes: pendingPlanes, blockers: blockers}
	}
	appCRB := vkAccessClusterRoleBindingName(device.Namespace, appSA)
	if appAccess != managedprotocol.WorkerAccessDisabled {
		if err := r.ensureSharedServiceAccount(ctx, device.Namespace, appSA, managedprotocol.WorkerModeAppHosting, appRole); err != nil {
			return err
		}
		if err := r.ensureSharedClusterRoleBinding(ctx, appCRB, device.Namespace, appSA, managedprotocol.WorkerModeAppHosting, appRole); err != nil {
			return err
		}
		if appAccess == managedprotocol.WorkerAccessReadWrite {
			if err := r.ensureSharedRoleBinding(ctx, device.Namespace, appSA, device.Namespace, appSA,
				managedprotocol.WorkerModeAppHosting, appDeviceRole); err != nil {
				return err
			}
		} else if err := r.revokeSharedRoleBinding(ctx,
			types.NamespacedName{Namespace: device.Namespace, Name: appSA}, device.Namespace, appSA,
			managedprotocol.WorkerModeAppHosting); err != nil {
			return err
		}
	} else {
		if err := r.revokeSharedClusterRoleBinding(ctx, appCRB, device.Namespace, appSA,
			managedprotocol.WorkerModeAppHosting); err != nil {
			return err
		}
		if err := r.revokeSharedRoleBinding(ctx,
			types.NamespacedName{Namespace: device.Namespace, Name: appSA}, device.Namespace, appSA,
			managedprotocol.WorkerModeAppHosting); err != nil {
			return err
		}
	}
	if sharedWorkerPlanePending(pendingPlanes, managedprotocol.WorkerModeNetworkManagement) || len(blockers) != 0 {
		// Close the same direct-reader race as the full post-bind audit before
		// exposing the restored app account while the network plane remains
		// fenced. A concrete RBAC finding still quarantines both identities.
		if err := r.inspectManagedWorkerNamespaceRBAC(ctx, device, appSA, networkSA); err != nil {
			return r.quarantineManagedSharedWorkersForRisk(ctx, device, err)
		}
		leaseNamespace := r.LeaseNamespace
		if leaseNamespace == "" {
			leaseNamespace = device.Namespace
		}
		if err := r.auditManagedSharedAppWorkerBindings(ctx, device.Namespace, leaseNamespace,
			appSA, appCRB, appRole, appDeviceRole, appAccess); err != nil {
			return r.quarantineManagedSharedWorkersForRisk(ctx, device, err)
		}
		return &sharedWorkerAccessTransition{planes: pendingPlanes, blockers: blockers}
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled {
		if err := r.ensureSharedServiceAccount(ctx, device.Namespace, networkSA, managedprotocol.WorkerModeNetworkManagement, networkRole); err != nil {
			return err
		}
		if err := r.ensureSharedRoleBinding(ctx, device.Namespace, networkSA, device.Namespace, networkSA,
			managedprotocol.WorkerModeNetworkManagement, networkRole); err != nil {
			return err
		}
	}
	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	leaseRB := ""
	if leaseNamespace != device.Namespace {
		leaseRB = networkLeaseRoleBindingName(device.Namespace, networkSA)
		if networkAccess != managedprotocol.WorkerAccessDisabled {
			if err := r.ensureSharedRoleBinding(ctx, leaseNamespace, leaseRB, device.Namespace, networkSA,
				managedprotocol.WorkerModeNetworkManagement, networkLeaseRole); err != nil {
				return err
			}
		}
	}
	networkCRB := vkAccessClusterRoleBindingName(device.Namespace, networkSA+"-global-read")
	if networkAccess != managedprotocol.WorkerAccessDisabled {
		if err := r.ensureSharedClusterRoleBinding(ctx, networkCRB, device.Namespace, networkSA,
			managedprotocol.WorkerModeNetworkManagement, globalReadRole); err != nil {
			return err
		}
	} else {
		if err := r.revokeSharedRoleBinding(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkSA},
			device.Namespace, networkSA, managedprotocol.WorkerModeNetworkManagement); err != nil {
			return err
		}
		if leaseRB != "" {
			if err := r.revokeSharedRoleBinding(ctx, types.NamespacedName{Namespace: leaseNamespace, Name: leaseRB},
				device.Namespace, networkSA, managedprotocol.WorkerModeNetworkManagement); err != nil {
				return err
			}
		}
		if err := r.revokeSharedClusterRoleBinding(ctx, networkCRB, device.Namespace, networkSA,
			managedprotocol.WorkerModeNetworkManagement); err != nil {
			return err
		}
	}
	if appAccess == managedprotocol.WorkerAccessDisabled {
		appCRB = ""
	}
	if networkAccess == managedprotocol.WorkerAccessDisabled {
		networkCRB, leaseRB = "", ""
	}
	if err := r.auditManagedSharedWorkerBindings(ctx, device.Namespace, leaseNamespace, appSA, networkSA,
		appCRB, networkCRB, leaseRB, appRole, appDeviceRole, networkRole, networkLeaseRole,
		globalReadRole, appAccess, networkAccess); err != nil {
		// Close the pre-audit/create race synchronously. A watch is useful for
		// convergence, but it cannot prevent a newly bound projected token from
		// being used before the next reconcile.
		return r.quarantineManagedSharedWorkersForRisk(ctx, device, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) exactSharedRoleBindingRole(ctx context.Context, key types.NamespacedName,
	accountNamespace, serviceAccount, account string, allowedRoles ...string) (string, bool, error) {
	var binding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, key, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read shared worker RoleBinding %s before access transition: %w", key, err)
	}
	role := binding.RoleRef.Name
	allowed := false
	for _, candidate := range allowedRoles {
		allowed = allowed || role == candidate
	}
	if !allowed || !sharedWorkerMetadataMatches(&binding, accountNamespace, account, role) ||
		binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" ||
		!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(accountNamespace, serviceAccount)) {
		return "", false, fmt.Errorf("refusing shared access transition with malformed shared worker RoleBinding %s", key)
	}
	return role, true, nil
}

func (r *CiscoDeviceReconciler) exactSharedClusterRoleBindingRole(ctx context.Context, name, namespace,
	serviceAccount, account string, allowedRoles ...string) (string, bool, error) {
	var binding rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Name: name}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read shared worker ClusterRoleBinding %s before access transition: %w", name, err)
	}
	role := binding.RoleRef.Name
	allowed := false
	for _, candidate := range allowedRoles {
		allowed = allowed || role == candidate
	}
	if !allowed || !sharedWorkerMetadataMatches(&binding, namespace, account, role) ||
		binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" ||
		!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, serviceAccount)) {
		return "", false, fmt.Errorf("refusing shared access transition with malformed shared worker ClusterRoleBinding %s", name)
	}
	return role, true, nil
}

func (r *CiscoDeviceReconciler) validateManagedWorkerClusterRoles(ctx context.Context, accountNamespace,
	appRole, appAccess, appDeviceRole, networkRole, networkLeaseRole, networkAccess, globalReadRole string) error {
	required := []string{}
	if appAccess != managedprotocol.WorkerAccessDisabled {
		required = append(required, appRole)
		if appAccess == managedprotocol.WorkerAccessReadWrite {
			required = append(required, appDeviceRole)
		}
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled {
		required = append(required, networkRole, globalReadRole)
		leaseNamespace := r.LeaseNamespace
		if leaseNamespace != "" && leaseNamespace != accountNamespace {
			required = append(required, networkLeaseRole)
		}
	}
	seen := map[string]struct{}{}
	for _, name := range required {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		var role rbacv1.ClusterRole
		if err := r.reader().Get(ctx, types.NamespacedName{Name: name}, &role); err != nil {
			return fmt.Errorf("read managed worker ClusterRole %q before binding: %w", name, err)
		}
		if err := managedprotocol.ValidateWorkerClusterRole(&role); err != nil {
			return fmt.Errorf("refuse managed worker ClusterRole %q: %w", name, err)
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) prepareSharedWorkerAccessTransitions(ctx context.Context,
	device *ciskov1.CiscoDevice, appRole, appAccess, appDeviceRole, networkRole, networkLeaseRole,
	networkAccess string) ([]string, []string, error) {
	appSA := r.appHostingServiceAccountName()
	appClusterRole, appClusterFound, err := r.exactSharedClusterRoleBindingRole(ctx,
		vkAccessClusterRoleBindingName(device.Namespace, appSA), device.Namespace, appSA,
		managedprotocol.WorkerModeAppHosting,
		managedprotocol.AppHostingReadOnlyClusterRole, managedprotocol.AppHostingReadWriteClusterRole)
	if err != nil {
		return nil, nil, err
	}
	appDeviceBindingRole, appDeviceBindingFound, err := r.exactSharedRoleBindingRole(ctx,
		types.NamespacedName{Namespace: device.Namespace, Name: appSA}, device.Namespace, appSA,
		managedprotocol.WorkerModeAppHosting, managedprotocol.AppHostingDeviceReadClusterRole)
	if err != nil {
		return nil, nil, err
	}
	appCurrent := false
	switch appAccess {
	case managedprotocol.WorkerAccessReadWrite:
		appCurrent = appClusterFound && appClusterRole == appRole &&
			appDeviceBindingFound && appDeviceBindingRole == appDeviceRole
	case managedprotocol.WorkerAccessReadOnly:
		appCurrent = appClusterFound && appClusterRole == appRole && !appDeviceBindingFound
	case managedprotocol.WorkerAccessDisabled:
		appCurrent = !appClusterFound && !appDeviceBindingFound
	}

	networkSA := r.networkManagementServiceAccountName()
	networkBindingRole, networkBindingFound, err := r.exactSharedRoleBindingRole(ctx,
		types.NamespacedName{Namespace: device.Namespace, Name: networkSA}, device.Namespace, networkSA,
		managedprotocol.WorkerModeNetworkManagement,
		managedprotocol.NetworkManagementReadOnlyClusterRole, managedprotocol.NetworkManagementReadWriteClusterRole)
	if err != nil {
		return nil, nil, err
	}
	networkGlobalRole, networkGlobalFound, err := r.exactSharedClusterRoleBindingRole(ctx,
		vkAccessClusterRoleBindingName(device.Namespace, networkSA+"-global-read"), device.Namespace, networkSA,
		managedprotocol.WorkerModeNetworkManagement, managedprotocol.NetworkManagementGlobalReadClusterRole)
	if err != nil {
		return nil, nil, err
	}
	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	leaseBindingRole, leaseBindingFound := "", false
	if leaseNamespace != device.Namespace {
		leaseBindingRole, leaseBindingFound, err = r.exactSharedRoleBindingRole(ctx, types.NamespacedName{
			Namespace: leaseNamespace,
			Name:      networkLeaseRoleBindingName(device.Namespace, networkSA),
		}, device.Namespace, networkSA, managedprotocol.WorkerModeNetworkManagement,
			managedprotocol.NetworkManagementLeaseReadOnlyClusterRole,
			managedprotocol.NetworkManagementLeaseReadWriteClusterRole)
		if err != nil {
			return nil, nil, err
		}
	}
	networkCurrent := false
	switch networkAccess {
	case managedprotocol.WorkerAccessReadWrite, managedprotocol.WorkerAccessReadOnly:
		networkCurrent = networkBindingFound && networkBindingRole == networkRole &&
			networkGlobalFound && networkGlobalRole == managedprotocol.NetworkManagementGlobalReadClusterRole &&
			(leaseNamespace == device.Namespace || leaseBindingFound && leaseBindingRole == networkLeaseRole)
	case managedprotocol.WorkerAccessDisabled:
		networkCurrent = !networkBindingFound && !networkGlobalFound && !leaseBindingFound
	}

	pending := []string{}
	blockers := []string{}
	appCordonPending := false
	if appAccess != managedprotocol.WorkerAccessReadWrite {
		cordoned, cordonErr := r.ensureManagedAppHostingNodesCordoned(ctx, device.Namespace)
		if cordonErr != nil {
			return nil, nil, cordonErr
		}
		if cordoned {
			pending = append(pending, managedprotocol.WorkerModeAppHosting)
			blockers = append(blockers, "waiting to observe manager-owned Node cordons before checking assigned workloads")
			appCordonPending = true
		}
	}
	if !appCurrent {
		blocked := appCordonPending
		if !blocked && appAccess != managedprotocol.WorkerAccessReadWrite {
			blocker, blockerErr := r.appHostingAssignedWorkloadBlocker(ctx, device.Namespace)
			if blockerErr != nil {
				return nil, nil, blockerErr
			}
			if blocker != "" {
				pending = append(pending, managedprotocol.WorkerModeAppHosting)
				blockers = append(blockers, blocker)
				blocked = true
			}
		}
		if !blocked {
			drained, drainErr := r.drainSharedWorkerPlane(ctx, device.Namespace,
				managedprotocol.WorkerModeAppHosting, appSA)
			if drainErr != nil {
				return nil, nil, drainErr
			}
			if !drained {
				pending = append(pending, managedprotocol.WorkerModeAppHosting)
			}
		}
	}
	if !networkCurrent {
		blocked := false
		networkWriteGranted := networkBindingRole == managedprotocol.NetworkManagementReadWriteClusterRole ||
			leaseBindingRole == managedprotocol.NetworkManagementLeaseReadWriteClusterRole
		if networkWriteGranted && networkAccess != managedprotocol.WorkerAccessReadWrite {
			networkBlockers, blockerErr := r.networkManagementDowngradeBlockers(ctx, device.Namespace)
			if blockerErr != nil {
				return nil, nil, blockerErr
			}
			if len(networkBlockers) != 0 {
				pending = append(pending, managedprotocol.WorkerModeNetworkManagement)
				blockers = append(blockers, networkBlockers...)
				blocked = true
			}
		}
		if !blocked {
			drained, drainErr := r.drainSharedWorkerPlane(ctx, device.Namespace,
				managedprotocol.WorkerModeNetworkManagement, networkSA)
			if drainErr != nil {
				return nil, nil, drainErr
			}
			if !drained {
				pending = append(pending, managedprotocol.WorkerModeNetworkManagement)
			}
		}
	}
	return pending, blockers, nil
}

func (r *CiscoDeviceReconciler) appHostingAssignedWorkloadBlocker(ctx context.Context, namespace string) (string, error) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("list managed devices before app-hosting access downgrade: %w", err)
	}
	seenNodes := map[string]string{}
	for i := range devices.Items {
		consumer := &devices.Items[i]
		identity := consumer.Status.NodeIdentity
		if identity == nil {
			continue
		}
		if identity.DeviceUID != string(consumer.UID) || identity.NodeName == "" || identity.NodeUID == "" {
			return "", fmt.Errorf("refusing app-hosting access downgrade: CiscoDevice %s/%s has an incomplete managed Node identity",
				consumer.Namespace, consumer.Name)
		}
		if priorUID, duplicate := seenNodes[identity.NodeName]; duplicate && priorUID != identity.NodeUID {
			return "", fmt.Errorf("refusing app-hosting access downgrade: managed Node %q has conflicting durable identities", identity.NodeName)
		}
		seenNodes[identity.NodeName] = identity.NodeUID
		var pods corev1.PodList
		if err := r.reader().List(ctx, &pods, client.MatchingFields{podNodeNameIndex: identity.NodeName}); err != nil {
			return "", fmt.Errorf("list workloads assigned to managed Node %q before app-hosting access downgrade: %w",
				identity.NodeName, err)
		}
		if len(pods.Items) != 0 {
			pod := pods.Items[0]
			return fmt.Sprintf("workload Pod %s/%s remains assigned to managed Node %q",
				pod.Namespace, pod.Name, identity.NodeName), nil
		}
	}
	return "", nil
}

func (r *CiscoDeviceReconciler) exactManagedNodeForAccessTransition(ctx context.Context,
	device *ciskov1.CiscoDevice) (*corev1.Node, error) {
	identity := device.Status.NodeIdentity
	if identity == nil || identity.DeviceUID != string(device.UID) || identity.NodeName == "" || identity.NodeUID == "" {
		return nil, fmt.Errorf("CiscoDevice %s/%s has an incomplete managed Node identity", device.Namespace, device.Name)
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: identity.NodeName}, &node); err != nil {
		return nil, fmt.Errorf("read managed Node %q for app-hosting access transition: %w", identity.NodeName, err)
	}
	if string(node.UID) != identity.NodeUID || node.Annotations[managedprotocol.AnnotationNodeUID] != identity.NodeUID ||
		!managedNodeMatchesDevice(&node, device) {
		return nil, fmt.Errorf("refusing app-hosting access transition: Node %q does not match CiscoDevice %s/%s identity",
			identity.NodeName, device.Namespace, device.Name)
	}
	return &node, nil
}

func (r *CiscoDeviceReconciler) ensureManagedAppHostingNodesCordoned(ctx context.Context, namespace string) (bool, error) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list managed devices before app-hosting cordon: %w", err)
	}
	changed := false
	for i := range devices.Items {
		consumer := &devices.Items[i]
		if consumer.Status.NodeIdentity == nil {
			continue
		}
		node, err := r.exactManagedNodeForAccessTransition(ctx, consumer)
		if err != nil {
			return false, err
		}
		marker := strings.TrimSpace(node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID])
		if marker != "" && marker != string(consumer.UID) {
			return false, fmt.Errorf("refusing app-hosting access transition: Node %q has a cordon marker for device UID %q",
				node.Name, marker)
		}
		if node.Spec.Unschedulable {
			// No marker means the operator owned this pre-existing cordon. Leave
			// it untouched now and when app-hosting later returns to read-write.
			continue
		}
		before := node.DeepCopy()
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] = string(consumer.UID)
		node.Spec.Unschedulable = true
		if err := r.Patch(ctx, node,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return false, fmt.Errorf("cordon managed Node %q before app-hosting access downgrade: %w", node.Name, err)
		}
		changed = true
		if r.Recorder != nil {
			r.Recorder.Eventf(consumer, corev1.EventTypeNormal, "AppHostingNodeCordoned",
				"Cordoned managed Node %s before removing app-hosting write access", node.Name)
		}
	}
	return changed, nil
}

func (r *CiscoDeviceReconciler) restoreManagedAppHostingCordon(ctx context.Context,
	device *ciskov1.CiscoDevice) error {
	status := device.Status.WorkerRevision
	if status == nil || status.DesiredRevision == "" || status.ObservedRevision != status.DesiredRevision ||
		status.PodUID == "" || status.ReadyHeartbeatTime == nil {
		return nil
	}
	node, err := r.exactManagedNodeForAccessTransition(ctx, device)
	if err != nil {
		return err
	}
	marker := strings.TrimSpace(node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID])
	if marker == "" {
		return nil
	}
	if marker != string(device.UID) {
		return fmt.Errorf("refusing to restore app-hosting scheduling on Node %q: cordon marker belongs to device UID %q",
			node.Name, marker)
	}
	if node.Annotations[managedprotocol.AnnotationAppWorkerPodUID] != status.PodUID ||
		node.Annotations[managedprotocol.AnnotationAppWorkerPodName] == "" ||
		node.Annotations[managedprotocol.AnnotationAppWorkerUsername] !=
			"system:serviceaccount:"+device.Namespace+":"+r.appHostingServiceAccountName() {
		return nil
	}
	before := node.DeepCopy()
	node.Spec.Unschedulable = false
	delete(node.Annotations, managedprotocol.AnnotationAppHostingCordonDeviceUID)
	if err := r.Patch(ctx, node,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("restore scheduling on manager-cordoned Node %q: %w", node.Name, err)
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(device, corev1.EventTypeNormal, "AppHostingNodeUncordoned",
			"Restored scheduling on managed Node %s after the app-hosting worker became ready", node.Name)
	}
	return nil
}

func (r *CiscoDeviceReconciler) networkManagementDowngradeBlockers(ctx context.Context, namespace string) ([]string, error) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list managed devices before network-management access downgrade: %w", err)
	}
	blockers := []string{}
	for i := range devices.Items {
		consumer := &devices.Items[i]
		if consumer.Status.NodeIdentity == nil {
			continue
		}
		if err := r.ensureManagedDeviceAuthoritiesSettledForAccessTransition(ctx, consumer); err != nil {
			blockers = append(blockers, fmt.Sprintf("CiscoDevice %s/%s has unsettled mutation authority: %v",
				consumer.Namespace, consumer.Name, err))
		}
	}
	return blockers, nil
}

type sharedWorkerDeploymentExpectation struct {
	device *ciskov1.CiscoDevice
	labels map[string]string
}

func (r *CiscoDeviceReconciler) sharedWorkerDeploymentExpectations(ctx context.Context, namespace, plane string) (
	map[string]sharedWorkerDeploymentExpectation, error,
) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list managed devices before shared %s worker drain: %w", plane, err)
	}
	expected := make(map[string]sharedWorkerDeploymentExpectation, len(devices.Items))
	for i := range devices.Items {
		consumer := &devices.Items[i]
		if consumer.Status.NodeIdentity == nil {
			continue
		}
		if consumer.UID == "" || consumer.Status.NodeIdentity.DeviceUID != string(consumer.UID) {
			return nil, fmt.Errorf("refusing shared %s worker drain: CiscoDevice %s/%s lacks a durable identity",
				plane, consumer.Namespace, consumer.Name)
		}
		name := consumer.Name + deploymentSuffix
		labels := perDeviceDeploymentLabels(consumer.Name)
		if plane == managedprotocol.WorkerModeNetworkManagement {
			name = networkDeploymentName(consumer.Name, string(consumer.UID))
			labels = perDeviceNetworkDeploymentLabels(consumer.Name)
		}
		expected[name] = sharedWorkerDeploymentExpectation{device: consumer, labels: labels}
	}
	return expected, nil
}

func sharedWorkerExpectationForLabels(expected map[string]sharedWorkerDeploymentExpectation,
	labels map[string]string) (string, sharedWorkerDeploymentExpectation, bool) {
	for name, candidate := range expected {
		if labelsContain(labels, candidate.labels) {
			return name, candidate, true
		}
	}
	return "", sharedWorkerDeploymentExpectation{}, false
}

func (r *CiscoDeviceReconciler) drainSharedWorkerPlane(ctx context.Context, namespace, plane,
	serviceAccount string) (bool, error) {
	expected, err := r.sharedWorkerDeploymentExpectations(ctx, namespace, plane)
	if err != nil {
		return false, err
	}
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Deployments before shared %s access transition: %w", plane, err)
	}
	matchedDeployments := map[string]*appsv1.Deployment{}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		candidate, found := expected[deployment.Name]
		owner := metav1.GetControllerOf(deployment)
		if !found || deployment.UID == "" || owner == nil || owner.APIVersion != ciskov1.GroupVersion.String() ||
			owner.Kind != "CiscoDevice" || owner.Name != candidate.device.Name || owner.UID != candidate.device.UID ||
			!reflect.DeepEqual(deployment.Spec.Selector, &metav1.LabelSelector{MatchLabels: candidate.labels}) ||
			!labelsContain(deployment.Spec.Template.Labels, candidate.labels) {
			return false, fmt.Errorf("refusing shared %s access transition: Deployment %s/%s lacks exact managed-worker provenance",
				plane, deployment.Namespace, deployment.Name)
		}
		matchedDeployments[deployment.Name] = deployment
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list ReplicaSets before shared %s access transition: %w", plane, err)
	}
	matchedReplicaSets := map[string]*appsv1.ReplicaSet{}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if replicaSet.Spec.Template.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		deploymentName, candidate, found := sharedWorkerExpectationForLabels(expected, replicaSet.Labels)
		owner := metav1.GetControllerOf(replicaSet)
		if !found || replicaSet.UID == "" || !labelsContain(replicaSet.Spec.Template.Labels, candidate.labels) || owner == nil ||
			owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "Deployment" ||
			owner.Name != deploymentName || owner.UID == "" {
			return false, fmt.Errorf("refusing shared %s access transition: ReplicaSet %s/%s lacks exact managed-worker provenance",
				plane, replicaSet.Namespace, replicaSet.Name)
		}
		if deployment, live := matchedDeployments[deploymentName]; live && owner.UID != deployment.UID {
			return false, fmt.Errorf("refusing shared %s access transition: ReplicaSet %s/%s has a stale Deployment owner UID",
				plane, replicaSet.Namespace, replicaSet.Name)
		}
		matchedReplicaSets[replicaSet.Name] = replicaSet
	}

	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Pods before shared %s access transition: %w", plane, err)
	}
	matchedPods := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		_, candidate, found := sharedWorkerExpectationForLabels(expected, pod.Labels)
		owner := metav1.GetControllerOf(pod)
		replicaSet := matchedReplicaSets[ownerName(owner)]
		if !found || owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
			owner.Kind != "ReplicaSet" || owner.UID == "" || replicaSet == nil ||
			owner.UID != replicaSet.UID || !labelsContain(replicaSet.Labels, candidate.labels) {
			return false, fmt.Errorf("refusing shared %s access transition: Pod %s/%s lacks exact managed-worker provenance",
				plane, pod.Namespace, pod.Name)
		}
		matchedPods++
	}

	if len(matchedDeployments) != 0 {
		for _, deployment := range matchedDeployments {
			if err := deleteForegroundWithUIDPrecondition(ctx, r.Client, deployment); err != nil &&
				!apierrors.IsNotFound(err) {
				return false, fmt.Errorf("drain %s/%s before shared %s access transition: %w",
					deployment.Namespace, deployment.Name, plane, err)
			}
		}
		return false, nil
	}
	return len(matchedReplicaSets) == 0 && matchedPods == 0, nil
}

func ownerName(owner *metav1.OwnerReference) string {
	if owner == nil {
		return ""
	}
	return owner.Name
}

// prepareSharedServiceAccountPolicyEpoch provides the one-time migration
// boundary for reusable functional identities created before the reserved
// ServiceAccount admission contract was installed. A signed legacy token may
// remain valid after its mutable ServiceAccount-name annotation is removed, so an
// unstamped account cannot be safely adopted. Revoke its exact bindings,
// rotate the ServiceAccount UID, and quiesce every workload using the old UID
// before allowing the account to be recreated with the verified policy epoch.
func (r *CiscoDeviceReconciler) prepareSharedServiceAccountPolicyEpoch(ctx context.Context,
	device *ciskov1.CiscoDevice, targets []sharedServiceAccountEpochTarget) ([]string, []string, error) {
	epoch := strings.TrimSpace(r.WorkerServiceAccountPolicyEpoch)
	if epoch == "" {
		return nil, nil, fmt.Errorf("shared worker ServiceAccount policy epoch is empty")
	}

	reserved := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		reserved[target.serviceAccount] = struct{}{}
	}
	legacyTokens := make(map[string][]string, len(targets))
	var secrets corev1.SecretList
	if err := r.reader().List(ctx, &secrets, client.InNamespace(device.Namespace)); err != nil {
		return nil, nil, fmt.Errorf("audit legacy shared worker ServiceAccount tokens in namespace %q: %w",
			device.Namespace, err)
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if secret.Type != corev1.SecretTypeServiceAccountToken {
			continue
		}
		serviceAccount := strings.TrimSpace(secret.Annotations[corev1.ServiceAccountNameKey])
		if _, found := reserved[serviceAccount]; found {
			legacyTokens[serviceAccount] = append(legacyTokens[serviceAccount], secret.Name)
		}
	}

	type targetState struct {
		target        sharedServiceAccountEpochTarget
		account       *corev1.ServiceAccount
		rotate        bool
		validationErr error
	}
	states := make([]targetState, 0, len(targets))
	// Validate every reserved-name occupant before mutating any of them. A
	// foreign collision must never be adopted or deleted as part of migration.
	for _, target := range targets {
		key := types.NamespacedName{Namespace: device.Namespace, Name: target.serviceAccount}
		var account corev1.ServiceAccount
		err := r.reader().Get(ctx, key, &account)
		if err != nil && !apierrors.IsNotFound(err) {
			states = append(states, targetState{
				target: target, validationErr: fmt.Errorf(
					"read shared worker ServiceAccount %s before policy-epoch migration: %w", key, err),
			})
			continue
		}
		state := targetState{target: target, rotate: len(legacyTokens[target.serviceAccount]) != 0}
		if err == nil {
			role := account.Annotations[annotationSharedWorkerRole]
			if !sharedWorkerRoleAllowed(target.plane, role) ||
				!sharedWorkerMetadataMatches(&account, device.Namespace, target.plane, role) {
				state.validationErr = fmt.Errorf(
					"shared worker ServiceAccount %s already exists without exact controller provenance", key)
				states = append(states, state)
				continue
			}
			state.account = &account
			state.rotate = state.rotate || account.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] != epoch
		}
		states = append(states, state)
	}
	pending := []string{}
	blockers := []string{}
	var migrationErrors []error
	processed := make([]bool, len(states))
	quarantine := func(state targetState) {
		target := state.target
		if err := r.revokeSharedWorkerForLegacyToken(ctx, device.Namespace, target.serviceAccount); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf(
				"revoke shared worker %s authority before policy-epoch migration: %w", target.plane, err))
		}
		if state.validationErr == nil && state.account != nil {
			if err := deleteWithUIDPrecondition(ctx, r.Client, state.account); err != nil && !apierrors.IsNotFound(err) {
				migrationErrors = append(migrationErrors, fmt.Errorf(
					"rotate shared worker ServiceAccount %s/%s for verified admission generation: %w",
					device.Namespace, target.serviceAccount, err))
			}
			pending = appendUniqueString(pending, target.plane)
		}
		drained, err := r.quarantineWorkerServiceAccountWorkloads(ctx, device.Namespace, target.serviceAccount)
		if err != nil {
			migrationErrors = append(migrationErrors, err)
		}
		if !drained {
			pending = appendUniqueString(pending, target.plane)
		}
		for _, secretName := range legacyTokens[target.serviceAccount] {
			pending = appendUniqueString(pending, target.plane)
			blockers = append(blockers, fmt.Sprintf(
				"legacy ServiceAccount token Secret %s/%s references reserved account %q; delete the long-lived token first",
				device.Namespace, secretName, target.serviceAccount))
		}
		if state.validationErr != nil {
			migrationErrors = append(migrationErrors, state.validationErr)
		}
	}

	// A malformed/foreign reserved-name occupant or attributable legacy token
	// is concrete compromise evidence. Quarantine every such target before an
	// unrelated planned network epoch rotation is allowed to wait on active
	// mutations; otherwise a blocker could pin already-compromised authority.
	for i, state := range states {
		if state.validationErr == nil && len(legacyTokens[state.target.serviceAccount]) == 0 {
			continue
		}
		quarantine(state)
		processed[i] = true
	}
	// Planned app-hosting rotation is independent of network mutation
	// settlement. Retire that old UID now rather than allowing an unrelated
	// active gNOI/config operation to pin it until the network plane is idle.
	for i, state := range states {
		if processed[i] || state.target.plane == managedprotocol.WorkerModeNetworkManagement ||
			state.account != nil && !state.rotate {
			continue
		}
		quarantine(state)
		processed[i] = true
	}

	// An epoch change by itself is a planned credential rotation, not evidence
	// of compromise. Before touching the network identity, fence that plane at
	// the same durable boundary as a write-profile downgrade: every mutation
	// Lease, maintenance session, upgrade admission, and rollout reservation
	// must be settled. Concrete-compromise targets were already quarantined.
	for i, state := range states {
		if processed[i] || state.target.plane != managedprotocol.WorkerModeNetworkManagement || state.account == nil ||
			state.account.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] == epoch {
			continue
		}
		mutationBlockers, err := r.networkManagementDowngradeBlockers(ctx, device.Namespace)
		if err != nil {
			migrationErrors = append(migrationErrors, err)
			return pending, blockers, stderrors.Join(migrationErrors...)
		}
		if len(mutationBlockers) != 0 {
			pending = appendUniqueString(pending, managedprotocol.WorkerModeNetworkManagement)
			for _, blocker := range mutationBlockers {
				blockers = append(blockers, "planned network worker policy-epoch rotation is waiting: "+blocker)
			}
			return pending, blockers, stderrors.Join(migrationErrors...)
		}
	}

	for i, state := range states {
		if processed[i] || state.account != nil && !state.rotate {
			continue
		}
		// An absent account may still have canonical bindings left by a crash,
		// or old Pods using the deterministic name. Revoke/drain before the name
		// becomes authorized again.
		quarantine(state)
	}
	return pending, blockers, stderrors.Join(migrationErrors...)
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, value := range additions {
		values = appendUniqueString(values, value)
	}
	return values
}

func sharedWorkerPlanePending(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func (r *CiscoDeviceReconciler) revokeSharedWorkerForLegacyToken(ctx context.Context, namespace, serviceAccount string) error {
	var errs []error
	switch serviceAccount {
	case r.appHostingServiceAccountName():
		if err := r.quarantineSharedClusterRoleBinding(ctx,
			vkAccessClusterRoleBindingName(namespace, serviceAccount), namespace, serviceAccount,
			managedprotocol.AppHostingReadOnlyClusterRole, managedprotocol.AppHostingReadWriteClusterRole); err != nil {
			errs = append(errs, err)
		}
		if err := r.quarantineSharedRoleBinding(ctx,
			types.NamespacedName{Namespace: namespace, Name: serviceAccount}, namespace, serviceAccount); err != nil {
			errs = append(errs, err)
		}
	case r.networkManagementServiceAccountName():
		// Revoke the cluster-wide inventory grant before any namespaced grant.
		// Each canonical object is evaluated independently so a foreign RB cannot
		// pin an exact CRB during compromise quarantine.
		if err := r.quarantineSharedClusterRoleBinding(ctx,
			vkAccessClusterRoleBindingName(namespace, serviceAccount+"-global-read"), namespace, serviceAccount,
			managedprotocol.NetworkManagementGlobalReadClusterRole); err != nil {
			errs = append(errs, err)
		}
		if err := r.quarantineSharedRoleBinding(ctx,
			types.NamespacedName{Namespace: namespace, Name: serviceAccount}, namespace, serviceAccount); err != nil {
			errs = append(errs, err)
		}
		leaseNamespace := r.LeaseNamespace
		if leaseNamespace == "" {
			leaseNamespace = namespace
		}
		if leaseNamespace != namespace {
			if err := r.quarantineSharedRoleBinding(ctx, types.NamespacedName{
				Namespace: leaseNamespace,
				Name:      networkLeaseRoleBindingName(namespace, serviceAccount),
			}, namespace, serviceAccount); err != nil {
				errs = append(errs, err)
			}
		}
	default:
		return fmt.Errorf("unknown reserved shared ServiceAccount %s/%s", namespace, serviceAccount)
	}
	// Canonical names are not a sufficient quarantine boundary: a namespace
	// actor may bind the reusable identity from any RoleBinding name. Remove
	// every exact-subject grant in the device and explicit lease namespaces, and
	// prove absence through the direct reader before this account can reappear.
	if err := r.quarantineSharedWorkerRoleBindings(ctx, namespace, serviceAccount); err != nil {
		errs = append(errs, err)
	}
	return stderrors.Join(errs...)
}

func (r *CiscoDeviceReconciler) quarantineSharedWorkerRoleBindings(ctx context.Context,
	accountNamespace, serviceAccount string) error {
	namespaces := []string{accountNamespace}
	leaseNamespace := strings.TrimSpace(r.LeaseNamespace)
	if leaseNamespace != "" && leaseNamespace != accountNamespace {
		namespaces = append(namespaces, leaseNamespace)
	}
	var errs []error
	for _, namespace := range namespaces {
		var bindings rbacv1.RoleBindingList
		if err := r.reader().List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
			errs = append(errs, fmt.Errorf("list shared worker RoleBindings in namespace %q during quarantine: %w", namespace, err))
			continue
		}
		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if !hasWorkerSubject(binding.Subjects, accountNamespace, serviceAccount) {
				continue
			}
			if err := deleteWithUIDPrecondition(ctx, r.Client, binding); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("quarantine shared worker RoleBinding %s: %w",
					client.ObjectKeyFromObject(binding), err))
			}
		}
	}
	// Close the list/delete race through the uncached reader. Any surviving or
	// concurrently-created exact-subject grant keeps quarantine fail closed.
	for _, namespace := range namespaces {
		var remaining rbacv1.RoleBindingList
		if err := r.reader().List(ctx, &remaining, client.InNamespace(namespace)); err != nil {
			errs = append(errs, fmt.Errorf("verify shared worker RoleBindings in namespace %q after quarantine: %w", namespace, err))
			continue
		}
		for i := range remaining.Items {
			binding := &remaining.Items[i]
			if hasWorkerSubject(binding.Subjects, accountNamespace, serviceAccount) {
				errs = append(errs, fmt.Errorf("shared worker RoleBinding %s remains after quarantine",
					client.ObjectKeyFromObject(binding)))
			}
		}
	}
	return stderrors.Join(errs...)
}

func (r *CiscoDeviceReconciler) quarantineExactSharedServiceAccount(ctx context.Context,
	namespace, serviceAccount, account string) error {
	key := types.NamespacedName{Namespace: namespace, Name: serviceAccount}
	var existing corev1.ServiceAccount
	if err := r.reader().Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read shared worker ServiceAccount %s during quarantine: %w", key, err)
	}
	role := existing.Annotations[annotationSharedWorkerRole]
	if !sharedWorkerRoleAllowed(account, role) ||
		!sharedWorkerMetadataMatches(&existing, namespace, account, role) {
		return fmt.Errorf("retain foreign shared worker ServiceAccount %s during quarantine", key)
	}
	if err := deleteWithUIDPrecondition(ctx, r.Client, &existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("rotate shared worker ServiceAccount %s during quarantine: %w", key, err)
	}
	var remaining corev1.ServiceAccount
	if err := r.reader().Get(ctx, key, &remaining); err == nil {
		return fmt.Errorf("shared worker ServiceAccount %s remains after quarantine", key)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("verify shared worker ServiceAccount %s after quarantine: %w", key, err)
	}
	return nil
}

// quarantineSharedRoleBinding removes a canonical reserved-account grant when
// its expected ServiceAccount subject is present. The namespaced roleRef,
// additive subjects, and mutable metadata are attacker-controlled after a
// delete/recreate; none may pin compromised authority. A genuinely foreign
// binding with no reserved subject is retained.
func (r *CiscoDeviceReconciler) quarantineSharedRoleBinding(ctx context.Context, key types.NamespacedName,
	accountNamespace, serviceAccount string) error {
	var binding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, key, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read shared worker RoleBinding %s during quarantine: %w", key, err)
	}
	if !hasWorkerSubject(binding.Subjects, accountNamespace, serviceAccount) {
		return fmt.Errorf("retain foreign shared worker RoleBinding %s during quarantine", key)
	}
	if err := deleteWithUIDPrecondition(ctx, r.Client, &binding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("quarantine shared worker RoleBinding %s: %w", key, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) quarantineSharedClusterRoleBinding(ctx context.Context, name, namespace,
	serviceAccount string, allowedRoles ...string) error {
	var binding rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Name: name}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read shared worker ClusterRoleBinding %s during quarantine: %w", name, err)
	}
	if binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" ||
		!stringInSlice(binding.RoleRef.Name, allowedRoles) ||
		!hasWorkerSubject(binding.Subjects, namespace, serviceAccount) {
		return fmt.Errorf("retain foreign shared worker ClusterRoleBinding %s during quarantine", name)
	}
	if err := deleteWithUIDPrecondition(ctx, r.Client, &binding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("quarantine shared worker ClusterRoleBinding %s: %w", name, err)
	}
	return nil
}

func stringInSlice(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func (r *CiscoDeviceReconciler) ensureSharedServiceAccount(ctx context.Context, namespace, name, account, role string) error {
	if strings.TrimSpace(r.WorkerServiceAccountPolicyEpoch) == "" {
		return fmt.Errorf("shared worker ServiceAccount policy epoch is empty")
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	var existing corev1.ServiceAccount
	if err := r.reader().Get(ctx, key, &existing); err == nil {
		existingRole := existing.Annotations[annotationSharedWorkerRole]
		if !sharedWorkerRoleAllowed(account, existingRole) ||
			!sharedWorkerMetadataMatches(&existing, namespace, account, existingRole) {
			return fmt.Errorf("shared worker ServiceAccount %s already exists without exact controller provenance", key)
		}
		if existing.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] != r.WorkerServiceAccountPolicyEpoch {
			return fmt.Errorf("shared worker ServiceAccount %s predates the verified reserved-account admission generation", key)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read shared worker ServiceAccount %s: %w", key, err)
	}
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if serviceAccountHasIdentity(sa) {
			existingRole := sa.Annotations[annotationSharedWorkerRole]
			if !sharedWorkerRoleAllowed(account, existingRole) ||
				!sharedWorkerMetadataMatches(sa, namespace, account, existingRole) {
				return fmt.Errorf("shared worker ServiceAccount %s is not an exact reusable account", key)
			}
			if sa.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] != r.WorkerServiceAccountPolicyEpoch {
				return fmt.Errorf("shared worker ServiceAccount %s requires UID rotation", key)
			}
		}
		applySharedWorkerAnnotations(sa, namespace, account, role)
		sa.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] = r.WorkerServiceAccountPolicyEpoch
		return nil
	})
	if err != nil {
		return fmt.Errorf("shared worker ServiceAccount %s: %w", key, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) ensureSharedRoleBinding(ctx context.Context, bindingNamespace, bindingName,
	accountNamespace, serviceAccount, account, role string) error {
	key := types.NamespacedName{Namespace: bindingNamespace, Name: bindingName}
	var existing rbacv1.RoleBinding
	if err := r.Get(ctx, key, &existing); err == nil {
		if existing.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}) {
			if !sharedWorkerRoleAllowed(account, existing.RoleRef.Name) ||
				!sharedWorkerMetadataMatches(&existing, accountNamespace, account, existing.RoleRef.Name) ||
				!reflect.DeepEqual(existing.Subjects, sharedWorkerSubject(accountNamespace, serviceAccount)) {
				return fmt.Errorf("shared worker RoleBinding %s has immutable unexpected roleRef", key)
			}
			if err := deleteWithUIDPrecondition(ctx, r.Client, &existing); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("replace shared worker RoleBinding %s for access-mode change: %w", key, err)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read shared worker RoleBinding %s: %w", key, err)
	}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: bindingNamespace, Name: bindingName}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		if objectMetaHasIdentity(&binding.ObjectMeta) && (len(binding.OwnerReferences) != 0 ||
			!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(accountNamespace, serviceAccount))) {
			return fmt.Errorf("shared worker RoleBinding %s is not an exact reusable-account binding", key)
		}
		binding.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}
		binding.Subjects = sharedWorkerSubject(accountNamespace, serviceAccount)
		applySharedWorkerAnnotations(binding, accountNamespace, account, role)
		return nil
	})
	if err != nil {
		return fmt.Errorf("shared worker RoleBinding %s: %w", key, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) ensureSharedClusterRoleBinding(ctx context.Context, name, namespace, serviceAccount, account, role string) error {
	var existing rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &existing); err == nil {
		if existing.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}) {
			if !sharedWorkerRoleAllowed(account, existing.RoleRef.Name) ||
				!sharedWorkerMetadataMatches(&existing, namespace, account, existing.RoleRef.Name) ||
				!reflect.DeepEqual(existing.Subjects, sharedWorkerSubject(namespace, serviceAccount)) {
				return fmt.Errorf("shared worker ClusterRoleBinding %s has immutable unexpected roleRef", name)
			}
			if err := deleteWithUIDPrecondition(ctx, r.Client, &existing); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("replace shared worker ClusterRoleBinding %s for access-mode change: %w", name, err)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read shared worker ClusterRoleBinding %s: %w", name, err)
	}
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		if objectMetaHasIdentity(&binding.ObjectMeta) && (len(binding.OwnerReferences) != 0 ||
			!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, serviceAccount))) {
			return fmt.Errorf("shared worker ClusterRoleBinding %s is not an exact reusable-account binding", name)
		}
		binding.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}
		binding.Subjects = sharedWorkerSubject(namespace, serviceAccount)
		applySharedWorkerAnnotations(binding, namespace, account, role)
		return nil
	})
	if err != nil {
		return fmt.Errorf("shared worker ClusterRoleBinding %s: %w", name, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) revokeSharedRoleBinding(ctx context.Context, key types.NamespacedName,
	accountNamespace, serviceAccount, account string) error {
	var binding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, key, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read disabled shared worker RoleBinding %s: %w", key, err)
	}
	if !sharedWorkerRoleAllowed(account, binding.RoleRef.Name) ||
		!sharedWorkerMetadataMatches(&binding, accountNamespace, account, binding.RoleRef.Name) ||
		!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(accountNamespace, serviceAccount)) {
		return fmt.Errorf("refusing to revoke unexpected shared worker RoleBinding %s", key)
	}
	if err := deleteWithUIDPrecondition(ctx, r.Client, &binding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("revoke disabled shared worker RoleBinding %s: %w", key, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) revokeSharedClusterRoleBinding(ctx context.Context, name, namespace,
	serviceAccount, account string) error {
	var binding rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Name: name}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read disabled shared worker ClusterRoleBinding %s: %w", name, err)
	}
	if !sharedWorkerRoleAllowed(account, binding.RoleRef.Name) ||
		!sharedWorkerMetadataMatches(&binding, namespace, account, binding.RoleRef.Name) ||
		!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, serviceAccount)) {
		return fmt.Errorf("refusing to revoke unexpected shared worker ClusterRoleBinding %s", name)
	}
	if err := deleteWithUIDPrecondition(ctx, r.Client, &binding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("revoke disabled shared worker ClusterRoleBinding %s: %w", name, err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) auditManagedSharedWorkerBindings(ctx context.Context, accountNamespace, leaseNamespace,
	appSA, networkSA, appCRB, networkCRB, leaseRB, appRole, appDeviceRole, networkRole, networkLeaseRole,
	globalReadRole, appAccess, networkAccess string) error {
	allowedRB := map[types.NamespacedName]string{}
	if appAccess == managedprotocol.WorkerAccessReadWrite {
		allowedRB[types.NamespacedName{Namespace: accountNamespace, Name: appSA}] = appDeviceRole
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled {
		allowedRB[types.NamespacedName{Namespace: accountNamespace, Name: networkSA}] = networkRole
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled && leaseRB != "" {
		allowedRB[types.NamespacedName{Namespace: leaseNamespace, Name: leaseRB}] = networkLeaseRole
	}
	allowedCRB := map[string]string{}
	if appAccess != managedprotocol.WorkerAccessDisabled {
		allowedCRB[appCRB] = appRole
	}
	if networkAccess != managedprotocol.WorkerAccessDisabled {
		allowedCRB[networkCRB] = globalReadRole
	}
	bindingNamespaces := []string{accountNamespace}
	if leaseNamespace != accountNamespace {
		bindingNamespaces = append(bindingNamespaces, leaseNamespace)
	}
	return r.auditSharedWorkerBindingSet(ctx, accountNamespace, bindingNamespaces,
		map[string]string{
			appSA:     managedprotocol.WorkerModeAppHosting,
			networkSA: managedprotocol.WorkerModeNetworkManagement,
		}, allowedRB, allowedCRB)
}

// auditManagedSharedAppWorkerBindings closes the app-plane post-bind race when
// a planned network transition remains fenced. The network identity is
// intentionally outside this expected set because it may still carry the
// prior, mutation-protected profile until its own transition can proceed.
func (r *CiscoDeviceReconciler) auditManagedSharedAppWorkerBindings(ctx context.Context,
	accountNamespace, leaseNamespace, appSA, appCRB, appRole, appDeviceRole, appAccess string) error {
	allowedRB := map[types.NamespacedName]string{}
	if appAccess == managedprotocol.WorkerAccessReadWrite {
		allowedRB[types.NamespacedName{Namespace: accountNamespace, Name: appSA}] = appDeviceRole
	}
	allowedCRB := map[string]string{}
	if appAccess != managedprotocol.WorkerAccessDisabled {
		allowedCRB[appCRB] = appRole
	}
	bindingNamespaces := []string{accountNamespace}
	if leaseNamespace != accountNamespace {
		bindingNamespaces = append(bindingNamespaces, leaseNamespace)
	}
	return r.auditSharedWorkerBindingSet(ctx, accountNamespace, bindingNamespaces,
		map[string]string{appSA: managedprotocol.WorkerModeAppHosting}, allowedRB, allowedCRB)
}

func (r *CiscoDeviceReconciler) auditSharedWorkerBindingSet(ctx context.Context, accountNamespace string,
	bindingNamespaces []string, accounts map[string]string, allowedRB map[types.NamespacedName]string,
	allowedCRB map[string]string) error {
	roleBindings := make([]rbacv1.RoleBinding, 0, len(allowedRB))
	for _, namespace := range bindingNamespaces {
		var namespaced rbacv1.RoleBindingList
		if err := r.reader().List(ctx, &namespaced, client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("audit shared worker RoleBindings in namespace %q: %w", namespace, err)
		}
		roleBindings = append(roleBindings, namespaced.Items...)
	}
	for i := range roleBindings {
		binding := &roleBindings[i]
		for sa, account := range accounts {
			if !hasWorkerSubject(binding.Subjects, accountNamespace, sa) {
				continue
			}
			key := client.ObjectKeyFromObject(binding)
			role, ok := allowedRB[key]
			if !ok || binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}) ||
				!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(accountNamespace, sa)) ||
				!sharedWorkerMetadataMatches(binding, accountNamespace, account, role) {
				return fmt.Errorf("shared worker ServiceAccount %s/%s has unexpected additive RoleBinding %s", accountNamespace, sa, key)
			}
		}
	}
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	// This is the post-bind race-closure read. It must bypass the informer cache
	// so an additive grant created concurrently with this reconcile cannot be
	// hidden until a later watch event.
	if err := r.reader().List(ctx, &clusterRoleBindings); err != nil {
		return fmt.Errorf("audit shared worker ClusterRoleBindings: %w", err)
	}
	for i := range clusterRoleBindings.Items {
		binding := &clusterRoleBindings.Items[i]
		for sa, account := range accounts {
			if !hasWorkerSubject(binding.Subjects, accountNamespace, sa) {
				continue
			}
			role, ok := allowedCRB[binding.Name]
			if !ok || binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}) ||
				!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(accountNamespace, sa)) ||
				!sharedWorkerMetadataMatches(binding, accountNamespace, account, role) {
				return fmt.Errorf("shared worker ServiceAccount %s/%s has unexpected additive ClusterRoleBinding %s", accountNamespace, sa, binding.Name)
			}
		}
	}
	return nil
}

// cleanupManagedSharedWorkerAccess removes namespace-shared grants only after
// the final durable managed consumer has left and every shared worker object
// is absent. CiscoDevices that never enrolled or completed reverse handoff do
// not retain the functional identities.
func (r *CiscoDeviceReconciler) cleanupManagedSharedWorkerAccess(ctx context.Context, device *ciskov1.CiscoDevice) error {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list CiscoDevices before shared worker access cleanup: %w", err)
	}
	for i := range devices.Items {
		other := &devices.Items[i]
		if other.Name == device.Name {
			continue
		}
		// NodeIdentity is the durable proof that this peer still consumes the
		// managed shared-worker contract. A never-managed device or a peer that
		// completed reverse handoff must not leak namespace-shared identities.
		if other.Status.NodeIdentity == nil {
			continue
		}
		if other.DeletionTimestamp.IsZero() {
			return nil
		}
		// A deletion timestamp alone does not prove the peer's projected
		// ServiceAccount token is no longer in use. Concurrent deletion must
		// retain namespace-shared grants until every peer worker has stopped.
		stopped, err := r.managedWriterWorkloadsStopped(ctx, other)
		if err != nil {
			return fmt.Errorf("verify deleting peer %s/%s before shared worker access cleanup: %w",
				other.Namespace, other.Name, err)
		}
		if !stopped {
			return nil
		}
	}
	appSA, networkSA := r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()
	inUse, err := r.sharedWorkerAccountsInUse(ctx, device.Namespace, appSA, networkSA)
	if err != nil {
		return fmt.Errorf("verify shared worker workloads before access cleanup: %w", err)
	}
	if inUse {
		return nil
	}
	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	objects := []client.Object{
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: vkAccessClusterRoleBindingName(device.Namespace, appSA)}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: vkAccessClusterRoleBindingName(device.Namespace, networkSA+"-global-read")}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: appSA}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: networkSA}},
	}
	accounts := []string{managedprotocol.WorkerModeAppHosting, managedprotocol.WorkerModeNetworkManagement,
		managedprotocol.WorkerModeAppHosting, managedprotocol.WorkerModeNetworkManagement}
	if leaseNamespace != device.Namespace {
		objects = append(objects, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: leaseNamespace, Name: networkLeaseRoleBindingName(device.Namespace, networkSA),
		}})
		accounts = append(accounts, managedprotocol.WorkerModeNetworkManagement)
	}
	for i, object := range objects {
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(object), object); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		role := ""
		var subjects []rbacv1.Subject
		var roleRef rbacv1.RoleRef
		switch binding := object.(type) {
		case *rbacv1.RoleBinding:
			role = binding.RoleRef.Name
			roleRef = binding.RoleRef
			subjects = binding.Subjects
		case *rbacv1.ClusterRoleBinding:
			role = binding.RoleRef.Name
			roleRef = binding.RoleRef
			subjects = binding.Subjects
		}
		serviceAccount := networkSA
		if accounts[i] == managedprotocol.WorkerModeAppHosting {
			serviceAccount = appSA
		}
		if !sharedWorkerRoleAllowed(accounts[i], role) ||
			!sharedWorkerMetadataMatches(object, device.Namespace, accounts[i], role) ||
			roleRef.APIGroup != rbacv1.GroupName || roleRef.Kind != "ClusterRole" ||
			!reflect.DeepEqual(subjects, sharedWorkerSubject(device.Namespace, serviceAccount)) {
			return fmt.Errorf("refusing to delete shared worker binding %s: metadata does not match", client.ObjectKeyFromObject(object))
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, object); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	for _, identity := range []struct{ name, account string }{
		{appSA, managedprotocol.WorkerModeAppHosting},
		{networkSA, managedprotocol.WorkerModeNetworkManagement},
	} {
		var sa corev1.ServiceAccount
		key := types.NamespacedName{Namespace: device.Namespace, Name: identity.name}
		if err := r.reader().Get(ctx, key, &sa); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		role := sa.Annotations[annotationSharedWorkerRole]
		if !sharedWorkerRoleAllowed(identity.account, role) ||
			!sharedWorkerMetadataMatches(&sa, device.Namespace, identity.account, role) {
			return fmt.Errorf("refusing to delete shared worker ServiceAccount %s: metadata does not match", key)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, &sa); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) sharedWorkerAccountsInUse(ctx context.Context, namespace string,
	serviceAccounts ...string) (bool, error) {
	accounts := make(map[string]struct{}, len(serviceAccounts))
	for _, serviceAccount := range serviceAccounts {
		accounts[serviceAccount] = struct{}{}
	}
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Deployments using shared worker accounts: %w", err)
	}
	for i := range deployments.Items {
		if _, found := accounts[deployments.Items[i].Spec.Template.Spec.ServiceAccountName]; found {
			return true, nil
		}
	}
	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list ReplicaSets using shared worker accounts: %w", err)
	}
	for i := range replicaSets.Items {
		if _, found := accounts[replicaSets.Items[i].Spec.Template.Spec.ServiceAccountName]; found {
			return true, nil
		}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Pods using shared worker accounts: %w", err)
	}
	for i := range pods.Items {
		if _, found := accounts[pods.Items[i].Spec.ServiceAccountName]; found {
			return true, nil
		}
	}
	return false, nil
}

func (r *CiscoDeviceReconciler) cleanupDisabledSharedWorkerAccounts(ctx context.Context,
	device *ciskov1.CiscoDevice, appAccess, networkAccess string) error {
	for _, plane := range []struct {
		access  string
		name    string
		account string
	}{
		{appAccess, r.appHostingServiceAccountName(), managedprotocol.WorkerModeAppHosting},
		{networkAccess, r.networkManagementServiceAccountName(), managedprotocol.WorkerModeNetworkManagement},
	} {
		if plane.access != managedprotocol.WorkerAccessDisabled {
			continue
		}
		inUse, err := r.sharedWorkerAccountsInUse(ctx, device.Namespace, plane.name)
		if err != nil {
			return err
		}
		if inUse {
			continue
		}
		var sa corev1.ServiceAccount
		key := types.NamespacedName{Namespace: device.Namespace, Name: plane.name}
		if err := r.reader().Get(ctx, key, &sa); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		role := sa.Annotations[annotationSharedWorkerRole]
		if !sharedWorkerRoleAllowed(plane.account, role) ||
			!sharedWorkerMetadataMatches(&sa, device.Namespace, plane.account, role) {
			return fmt.Errorf("refusing to delete disabled shared worker ServiceAccount %s", key)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, &sa); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete disabled shared worker ServiceAccount %s: %w", key, err)
		}
	}
	return nil
}
