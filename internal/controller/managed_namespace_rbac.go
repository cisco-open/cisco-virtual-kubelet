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
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

// namespaceRBACRisk describes a namespaced delegation that can cross the
// shared-worker identity boundary. ClusterRoleBindings intentionally are not
// included: cluster-wide delegation remains an explicit cluster-admin trust
// boundary, while namespace administrators commonly receive RoleBindings to
// Kubernetes' built-in edit/admin roles.
type namespaceRBACRisk struct {
	binding    types.NamespacedName
	roleRef    rbacv1.RoleRef
	rule       int
	capability string
}

func (e *namespaceRBACRisk) Error() string {
	return fmt.Sprintf("unsafe RoleBinding %s resolves to %s %q rule %d, which grants %s",
		e.binding, e.roleRef.Kind, e.roleRef.Name, e.rule, e.capability)
}

type managedWorkerNameScope struct {
	deployments         map[string]struct{}
	podPrefixes         []string
	serviceAccounts     map[string]struct{}
	plaintextCredential bool
}

func newManagedWorkerNameScope(devices []ciskov1.CiscoDevice, current *ciskov1.CiscoDevice) managedWorkerNameScope {
	names := make(map[string]struct{}, 2*(len(devices)+1))
	serviceAccounts := make(map[string]struct{}, 2*(len(devices)+1))
	scopePlaintext := false
	add := func(device *ciskov1.CiscoDevice) {
		if device == nil || device.Name == "" {
			return
		}
		names[device.Name+deploymentSuffix] = struct{}{}
		names[networkDeploymentName(string(device.UID))] = struct{}{}
		// Keep exact-name delegation checks covering the previous naming
		// contract until policy-epoch rotation has retired its workers.
		names[fmt.Sprintf("n%d-%s-u%s%s", len(device.Name), device.Name, device.UID, networkDeploymentSuffix)] = struct{}{}
		if device.UID != "" {
			serviceAccounts[managedWorkerServiceAccountName(device)] = struct{}{}
			serviceAccounts[topologyLegacyWorkerServiceAccountName(device)] = struct{}{}
		}
	}
	for i := range devices {
		add(&devices[i])
		if strings.TrimSpace(devices[i].Spec.Password) != "" {
			scopePlaintext = true
		}
	}
	if current != nil {
		add(current)
		if strings.TrimSpace(current.Spec.Password) != "" {
			scopePlaintext = true
		}
	}
	prefixes := make([]string, 0, len(names))
	for name := range names {
		prefixes = append(prefixes, name+"-")
		// Native generated Pod names keep at most 58 prefix bytes. Auditing
		// only the untruncated Deployment name misses exact-name grants.
		if len(name)+1 > 58 {
			prefixes = append(prefixes, (name+"-")[:58])
		}
	}
	sort.Strings(prefixes)
	return managedWorkerNameScope{
		deployments: names, podPrefixes: prefixes, serviceAccounts: serviceAccounts,
		plaintextCredential: scopePlaintext,
	}
}

func (s managedWorkerNameScope) includes(resource, name string) bool {
	if name == "*" { // Not an RBAC wildcard, but fail closed on ambiguous intent.
		return true
	}
	switch resource {
	case "serviceaccounts":
		_, found := s.serviceAccounts[name]
		return found
	case "deployments", "deployments/scale", "deployments/status":
		_, found := s.deployments[name]
		return found
	case "replicasets", "replicasets/scale", "replicasets/status", "pods", "pods/status", "pods/exec", "pods/attach", "pods/portforward", "pods/proxy", "pods/log", "pods/eviction", "pods/binding", "bindings":
		for _, prefix := range s.podPrefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func policyValueMatches(values []string, expected string) bool {
	for _, value := range values {
		if value == "*" || value == expected {
			return true
		}
	}
	return false
}

func policyResourceMatches(values []string, expected string) bool {
	for _, value := range values {
		if value == "*" || value == expected {
			return true
		}
		// Kubernetes currently documents only the whole-resource wildcard,
		// but treat a future/implementation-specific subresource wildcard as
		// a match rather than silently weakening this safety gate.
		if strings.HasSuffix(value, "/*") && strings.HasPrefix(expected, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

func policyVerbMatches(values []string, expected ...string) bool {
	for _, value := range values {
		if value == "*" {
			return true
		}
		for _, candidate := range expected {
			if value == candidate {
				return true
			}
		}
	}
	return false
}

func ruleAppliesToManagedNames(rule rbacv1.PolicyRule, resource string, scope managedWorkerNameScope) bool {
	if len(rule.ResourceNames) == 0 {
		return true
	}
	for _, name := range rule.ResourceNames {
		if scope.includes(resource, name) {
			return true
		}
	}
	return false
}

func ruleAllowsManagedDeletion(rule rbacv1.PolicyRule, resource string, scope managedWorkerNameScope) bool {
	// resourceNames can constrain an individual DELETE, but Kubernetes cannot
	// apply them to DELETECOLLECTION. Model those authorization attributes
	// separately to avoid both gaps and false positives.
	if policyVerbMatches(rule.Verbs, "delete") && ruleAppliesToManagedNames(rule, resource, scope) {
		return true
	}
	return len(rule.ResourceNames) == 0 && policyVerbMatches(rule.Verbs, "deletecollection")
}

func readsSensitiveConfigResource(rule rbacv1.PolicyRule) bool {
	for _, resource := range []string{
		"iosxeconfigapplylogs", "iosxeconfigbundles", "iosxeconfigrevisions", "iosxeconfigs",
		"iosxedevicegroupconfigs", "iosxediagnostics", "iosxeinterfacegroupconfigs", "iosxetelemetries",
		"iosxetemplates", "networkcontrollerconfigs", "nxosconfigs",
	} {
		if policyResourceMatches(rule.Resources, resource) || policyResourceMatches(rule.Resources, resource+"/status") {
			return true
		}
	}
	return false
}

func trustedSharedWorkerRoleBinding(binding *rbacv1.RoleBinding, namespace, leaseNamespace, appSA, networkSA string) bool {
	if binding == nil ||
		binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" {
		return false
	}
	type identity struct {
		serviceAccount string
		account        string
		roles          []string
	}
	identities := []identity{
		{
			serviceAccount: appSA,
			account:        managedprotocol.WorkerModeAppHosting,
			roles:          []string{managedprotocol.AppHostingDeviceReadClusterRole},
		},
		{
			serviceAccount: networkSA,
			account:        managedprotocol.WorkerModeNetworkManagement,
			roles: []string{
				managedprotocol.NetworkManagementReadOnlyClusterRole,
				managedprotocol.NetworkManagementReadWriteClusterRole,
			},
		},
	}
	for _, candidate := range identities {
		expectedName := candidate.serviceAccount
		expectedNamespace := namespace
		roles := candidate.roles
		if candidate.account == managedprotocol.WorkerModeNetworkManagement && leaseNamespace != "" &&
			leaseNamespace != namespace && binding.Namespace == leaseNamespace {
			expectedName = networkLeaseRoleBindingName(namespace, candidate.serviceAccount)
			expectedNamespace = leaseNamespace
			roles = []string{
				managedprotocol.NetworkManagementLeaseReadOnlyClusterRole,
				managedprotocol.NetworkManagementLeaseReadWriteClusterRole,
			}
		}
		if binding.Namespace != expectedNamespace || binding.Name != expectedName ||
			!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, candidate.serviceAccount)) {
			continue
		}
		for _, role := range roles {
			if binding.RoleRef.Name == role &&
				sharedWorkerMetadataMatches(binding, namespace, candidate.account, role) {
				return true
			}
		}
	}
	return false
}

// trustedGeneratedWorkerRoleBinding identifies only the canonical, exact
// per-device RoleBinding. It is allowed to resolve to the credential-reading
// device role without causing the namespace audit to quarantine itself; every
// other binding to a generated identity remains untrusted and is independently
// rejected by auditGeneratedWorkerBindings.
func trustedGeneratedWorkerRoleBinding(binding *rbacv1.RoleBinding,
	devices []ciskov1.CiscoDevice, current *ciskov1.CiscoDevice) bool {
	if binding == nil {
		return false
	}
	candidates := make([]*ciskov1.CiscoDevice, 0, len(devices)+1)
	for i := range devices {
		candidates = append(candidates, &devices[i])
	}
	if current != nil {
		candidates = append(candidates, current)
	}
	for _, device := range candidates {
		if device == nil || device.UID == "" || binding.Namespace != device.Namespace {
			continue
		}
		for _, candidate := range []struct {
			name    string
			managed bool
		}{
			{name: managedWorkerServiceAccountName(device), managed: true},
			{name: topologyLegacyWorkerServiceAccountName(device), managed: false},
		} {
			if binding.Name != candidate.name ||
				validateGeneratedRoleBinding(binding, device, candidate.name) != nil ||
				!workerAnnotationsMatch(binding.Annotations, workerServiceAccountAnnotations(device, candidate.managed)) ||
				!managedServiceAccountOwnedByDeviceMeta(&binding.ObjectMeta, device) {
				continue
			}
			return true
		}
	}
	return false
}

// trustedLegacySharedWorkerRoleBinding covers only the exact pre-topology
// compatibility binding that phase-zero migration is replacing. Treating its
// required config/Secret reads as an operator delegation would deadlock that
// migration, but any extra subject, owner, role, or bridge marker fails closed.
func trustedLegacySharedWorkerRoleBinding(binding *rbacv1.RoleBinding, namespace, serviceAccount string) bool {
	if binding == nil || binding.Namespace != namespace || len(binding.OwnerReferences) != 0 ||
		binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}) ||
		!reflect.DeepEqual(binding.Subjects, exactWorkerSubject(namespace, serviceAccount)) {
		return false
	}
	if binding.Name == serviceAccount {
		return true
	}
	return binding.Name == serviceAccount+"-device" &&
		binding.Annotations[sharedWorkerRetirementAnnotation] == sharedWorkerRetirementVersion
}

func unsafeWorkerServiceAccountAuthority(rule rbacv1.PolicyRule, appSA, networkSA string,
	scope managedWorkerNameScope) string {
	// Kubernetes 1.36 introduces constrained ServiceAccount impersonation
	// under authentication.k8s.io. Reject both the base capability and every
	// namespace/account-qualified derivative so enabling that beta feature
	// cannot reopen a boundary this controller audited under v1.35 semantics.
	if policyValueMatches(rule.APIGroups, "authentication.k8s.io") &&
		policyResourceMatches(rule.Resources, "serviceaccounts") {
		for _, verb := range rule.Verbs {
			if verb == "*" || verb == "impersonate" || verb == "impersonate:serviceaccount" {
				return "constrained impersonation of ServiceAccounts in the managed device namespace"
			}
		}
	}
	if !policyValueMatches(rule.APIGroups, "") || !policyResourceMatches(rule.Resources, "serviceaccounts") {
		return ""
	}
	if policyVerbMatches(rule.Verbs, "impersonate") {
		if len(rule.ResourceNames) == 0 {
			return "impersonation of reserved or generated worker ServiceAccounts"
		}
		for _, name := range rule.ResourceNames {
			if name == appSA || name == networkSA {
				return fmt.Sprintf("impersonation of reserved ServiceAccount %q", name)
			}
			if name == "*" || scope.includes("serviceaccounts", name) {
				return fmt.Sprintf("impersonation of reserved or generated worker ServiceAccount %q", name)
			}
		}
	}
	// DELETECOLLECTION requests carry no resourceName, so any rule which grants
	// the collection verb can bypass a name-based ServiceAccount admission
	// policy and remove every reserved/generated account in this namespace.
	if len(rule.ResourceNames) == 0 && policyVerbMatches(rule.Verbs, "deletecollection") {
		return "collection deletion of ServiceAccounts in the managed device namespace"
	}
	return ""
}

func unsafeManagedNamespaceRule(rule rbacv1.PolicyRule, appSA, networkSA string,
	scope managedWorkerNameScope, allowTrustedReads bool) string {
	// Constrained impersonation action grants live on the target API resource,
	// not authentication.k8s.io/serviceaccounts. Any such namespaced grant can
	// be paired with an identity grant elsewhere to act as a reserved worker.
	for _, verb := range rule.Verbs {
		if strings.HasPrefix(verb, "impersonate-on:serviceaccount:") {
			return "constrained ServiceAccount impersonation action in the managed device namespace"
		}
	}
	// Kubernetes' normal privilege-escalation checks allow a subject which has
	// the special bind/escalate verbs to delegate permissions it does not
	// otherwise hold. Such a grant can synthesize an exec-capable RoleBinding
	// immediately after a clean audit, before the controller watch can revoke
	// worker authority, so no namespaced delegation of these verbs is safe.
	if policyValueMatches(rule.APIGroups, rbacv1.GroupName) &&
		(policyResourceMatches(rule.Resources, "roles") || policyResourceMatches(rule.Resources, "clusterroles")) &&
		policyVerbMatches(rule.Verbs, "bind", "escalate") {
		return "RBAC bind or escalate authority in the managed device namespace"
	}
	if !allowTrustedReads && policyVerbMatches(rule.Verbs, "get", "list", "watch") {
		switch {
		case policyValueMatches(rule.APIGroups, "") && policyResourceMatches(rule.Resources, "secrets"):
			return "read access to Secrets in the managed device namespace"
		case policyValueMatches(rule.APIGroups, "cisco.vk") &&
			(policyResourceMatches(rule.Resources, "ciscodevices") || policyResourceMatches(rule.Resources, "ciscodevices/status")):
			return "read access to CiscoDevice specs, which may contain device credentials"
		case policyValueMatches(rule.APIGroups, "config.cisco.vk") && readsSensitiveConfigResource(rule):
			return "read access to network configuration objects"
		}
		// Backward-compatible inline passwords are injected into generated Pod
		// specs. Restrict reads of those workload objects until every device in
		// the namespace uses a Secret reference instead.
		if scope.plaintextCredential {
			for _, resource := range []string{"pods", "pods/status"} {
				if policyValueMatches(rule.APIGroups, "") && policyResourceMatches(rule.Resources, resource) &&
					ruleAppliesToManagedNames(rule, resource, scope) {
					return "read access to managed worker Pod specs containing an inline device password"
				}
			}
			for _, resource := range []string{"deployments", "deployments/status", "replicasets", "replicasets/status"} {
				if policyValueMatches(rule.APIGroups, "apps") && policyResourceMatches(rule.Resources, resource) &&
					ruleAppliesToManagedNames(rule, resource, scope) {
					return fmt.Sprintf("read access to managed worker %s containing an inline device password", resource)
				}
			}
		}
	}

	if capability := unsafeWorkerServiceAccountAuthority(rule, appSA, networkSA, scope); capability != "" {
		return capability
	}

	if policyValueMatches(rule.APIGroups, "") {
		for _, resource := range []string{"pods/binding", "bindings"} {
			if policyResourceMatches(rule.Resources, resource) &&
				policyVerbMatches(rule.Verbs, "create") &&
				ruleAppliesToManagedNames(rule, resource, scope) {
				return fmt.Sprintf("binding of managed worker Pods through %s", resource)
			}
		}
		connections := []struct {
			resource string
			verbs    []string
		}{
			{"pods/exec", []string{"get", "create", "connect"}},
			{"pods/attach", []string{"get", "create", "connect"}},
			{"pods/portforward", []string{"get", "create", "connect"}},
			{"pods/proxy", []string{"get", "list", "watch", "create", "connect", "update", "patch", "delete"}},
			{"pods/log", []string{"get", "list", "watch"}},
		}
		for _, connection := range connections {
			if policyResourceMatches(rule.Resources, connection.resource) &&
				policyVerbMatches(rule.Verbs, connection.verbs...) &&
				ruleAppliesToManagedNames(rule, connection.resource, scope) {
				return fmt.Sprintf("worker %s access", connection.resource)
			}
		}
		if policyResourceMatches(rule.Resources, "pods/eviction") &&
			policyVerbMatches(rule.Verbs, "create") &&
			ruleAppliesToManagedNames(rule, "pods/eviction", scope) {
			return "eviction of managed worker Pods"
		}
		if policyResourceMatches(rule.Resources, "pods") && ruleAllowsManagedDeletion(rule, "pods", scope) {
			return "deletion of managed worker Pods"
		}
	}

	if policyValueMatches(rule.APIGroups, "apps") {
		// Base Pod, ReplicaSet, and Deployment CREATE/UPDATE are object-aware
		// operations covered by the fail-closed shared-worker admission policies.
		// RBAC cannot condition those verbs on serviceAccountName, so this audit
		// covers the scale and deletion subresources that can bypass that check.
		for _, resource := range []string{"deployments/scale", "replicasets/scale"} {
			if policyResourceMatches(rule.Resources, resource) &&
				policyVerbMatches(rule.Verbs, "update", "patch") &&
				ruleAppliesToManagedNames(rule, resource, scope) {
				return fmt.Sprintf("update or patch of managed worker %s", resource)
			}
		}
		for _, resource := range []string{"deployments/status", "replicasets/status"} {
			if policyResourceMatches(rule.Resources, resource) &&
				policyVerbMatches(rule.Verbs, "update", "patch") &&
				ruleAppliesToManagedNames(rule, resource, scope) {
				return fmt.Sprintf("update or patch of managed worker %s", resource)
			}
		}
		for _, resource := range []string{"deployments", "replicasets"} {
			if policyResourceMatches(rule.Resources, resource) && ruleAllowsManagedDeletion(rule, resource, scope) {
				return fmt.Sprintf("deletion of managed worker %s", resource)
			}
		}
	}
	return ""
}

func (r *CiscoDeviceReconciler) resolveNamespacedRoleBindingRules(ctx context.Context,
	binding *rbacv1.RoleBinding) ([]rbacv1.PolicyRule, *rbacv1.ClusterRole, bool, error) {
	if binding.RoleRef.APIGroup != rbacv1.GroupName {
		return nil, nil, false, fmt.Errorf("RoleBinding %s/%s has unsupported roleRef apiGroup %q",
			binding.Namespace, binding.Name, binding.RoleRef.APIGroup)
	}
	switch binding.RoleRef.Kind {
	case "Role":
		var role rbacv1.Role
		err := r.reader().Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: binding.RoleRef.Name}, &role)
		if apierrors.IsNotFound(err) {
			return nil, nil, false, nil
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("resolve RoleBinding %s/%s Role %q: %w",
				binding.Namespace, binding.Name, binding.RoleRef.Name, err)
		}
		return role.Rules, nil, true, nil
	case "ClusterRole":
		var role rbacv1.ClusterRole
		err := r.reader().Get(ctx, types.NamespacedName{Name: binding.RoleRef.Name}, &role)
		if apierrors.IsNotFound(err) {
			return nil, nil, false, nil
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("resolve RoleBinding %s/%s ClusterRole %q: %w",
				binding.Namespace, binding.Name, binding.RoleRef.Name, err)
		}
		return role.Rules, &role, true, nil
	default:
		return nil, nil, false, fmt.Errorf("RoleBinding %s/%s has unsupported roleRef kind %q",
			binding.Namespace, binding.Name, binding.RoleRef.Kind)
	}
}

func (r *CiscoDeviceReconciler) inspectManagedWorkerNamespaceRBAC(ctx context.Context,
	device *ciskov1.CiscoDevice, appSA, networkSA string) error {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list CiscoDevices for managed worker RBAC name scope in namespace %q: %w", device.Namespace, err)
	}
	scope := newManagedWorkerNameScope(devices.Items, device)
	leaseNamespace := strings.TrimSpace(r.LeaseNamespace)
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	namespaces := []string{device.Namespace}
	if leaseNamespace != device.Namespace {
		namespaces = append(namespaces, leaseNamespace)
	}
	for _, namespace := range namespaces {
		var bindings rbacv1.RoleBindingList
		if err := r.reader().List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("list RoleBindings in managed worker namespace %q: %w", namespace, err)
		}
		sort.Slice(bindings.Items, func(i, j int) bool { return bindings.Items[i].Name < bindings.Items[j].Name })
		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if len(binding.Subjects) == 0 {
				continue
			}
			sharedSubject := hasWorkerSubject(binding.Subjects, device.Namespace, appSA) ||
				hasWorkerSubject(binding.Subjects, device.Namespace, networkSA)
			trustedShared := trustedSharedWorkerRoleBinding(binding, device.Namespace, leaseNamespace, appSA, networkSA)
			// Only the exact controller binding may delegate a reserved reusable
			// identity. Reject even a currently dangling additive binding: otherwise
			// its Role can appear immediately after a clean audit and authorize an old
			// projected token before the watch-driven quarantine completes.
			if sharedSubject && !trustedShared {
				return fmt.Errorf("unexpected RoleBinding %s grants a reserved shared worker ServiceAccount",
					client.ObjectKeyFromObject(binding))
			}
			// A distinct coordination namespace is not otherwise part of the device
			// trust boundary. Its exact shared-account subjects are audited above;
			// unrelated local RBAC must not quarantine device workers.
			if namespace != device.Namespace && !sharedSubject {
				continue
			}
			rules, clusterRole, found, err := r.resolveNamespacedRoleBindingRules(ctx, binding)
			if err != nil {
				return err
			}
			// A dangling ordinary RoleBinding grants nothing. Role and ClusterRole
			// watches re-run this audit as soon as its role appears.
			if !found {
				continue
			}
			trustedGenerated := trustedGeneratedWorkerRoleBinding(binding, devices.Items, device)
			trustedLegacyShared := trustedLegacySharedWorkerRoleBinding(binding, device.Namespace, r.vkServiceAccountName())
			if trustedShared {
				if clusterRole == nil {
					return fmt.Errorf("trusted shared worker RoleBinding %s does not resolve to a ClusterRole", client.ObjectKeyFromObject(binding))
				}
				if err := managedprotocol.ValidateWorkerClusterRole(clusterRole); err != nil {
					return fmt.Errorf("trusted shared worker RoleBinding %s resolves to an invalid fixed role: %w",
						client.ObjectKeyFromObject(binding), err)
				}
			}
			for ruleIndex, rule := range rules {
				if capability := unsafeManagedNamespaceRule(rule, appSA, networkSA, scope,
					trustedShared || trustedGenerated || trustedLegacyShared); capability != "" {
					return &namespaceRBACRisk{
						binding: client.ObjectKeyFromObject(binding), roleRef: binding.RoleRef,
						rule: ruleIndex + 1, capability: capability,
					}
				}
			}
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) quarantineManagedSharedWorkers(ctx context.Context,
	device *ciskov1.CiscoDevice) (bool, error) {
	var errs []error
	namespace := device.Namespace
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		if err := r.revokeSharedWorkerForLegacyToken(ctx, namespace, account); err != nil {
			errs = append(errs, err)
		}
	}

	// Rotate every exact-proven reserved account after its grants are revoked.
	// This invalidates already-minted tokens; a foreign object occupying a
	// reserved name is retained and reported rather than adopted or destroyed.
	for _, plane := range []struct {
		name           string
		serviceAccount string
	}{
		{managedprotocol.WorkerModeAppHosting, r.appHostingServiceAccountName()},
		{managedprotocol.WorkerModeNetworkManagement, r.networkManagementServiceAccountName()},
	} {
		if err := r.quarantineExactSharedServiceAccount(ctx, namespace, plane.serviceAccount, plane.name); err != nil {
			errs = append(errs, err)
		}
	}

	// Revoking API authorization is not enough after an exec-capable binding
	// has existed: an injected process already holds device credentials and can
	// continue talking directly to the switch. In quarantine, exact use of the
	// reserved ServiceAccount is sufficient scope; a malicious workload must
	// not survive merely because it forged or omitted normal provenance.
	drained := len(errs) == 0
	for _, plane := range []struct {
		name           string
		serviceAccount string
	}{
		{managedprotocol.WorkerModeAppHosting, r.appHostingServiceAccountName()},
		{managedprotocol.WorkerModeNetworkManagement, r.networkManagementServiceAccountName()},
	} {
		planeDrained, err := r.quarantineWorkerServiceAccountWorkloads(ctx, namespace, plane.serviceAccount)
		if err != nil {
			errs = append(errs, err)
			drained = false
			continue
		}
		if !planeDrained {
			drained = false
		}
	}
	return drained, stderrors.Join(errs...)
}

// quarantineWorkerServiceAccountWorkloads terminates every namespaced
// workload using one exact reserved ServiceAccount. This is intentionally
// broader than normal lifecycle drain, which retains strict provenance:
// quarantine runs only after authority has been revoked and must eliminate an
// attacker-controlled process even when its labels or owner chain are forged.
// Controllers are deleted before their dependents, every production delete is
// UID-preconditioned, and a direct read-back must observe complete absence
// before the caller may regrant the account name.
func (r *CiscoDeviceReconciler) quarantineWorkerServiceAccountWorkloads(ctx context.Context,
	namespace, serviceAccount string) (bool, error) {
	type workloadSet struct {
		deployments []appsv1.Deployment
		replicaSets []appsv1.ReplicaSet
		pods        []corev1.Pod
	}
	read := func() (workloadSet, error) {
		var workloads workloadSet
		var deployments appsv1.DeploymentList
		if err := r.reader().List(ctx, &deployments, client.InNamespace(namespace)); err != nil {
			return workloads, fmt.Errorf("list Deployments during worker-account quarantine: %w", err)
		}
		for i := range deployments.Items {
			if deployments.Items[i].Spec.Template.Spec.ServiceAccountName == serviceAccount {
				workloads.deployments = append(workloads.deployments, *deployments.Items[i].DeepCopy())
			}
		}
		var replicaSets appsv1.ReplicaSetList
		if err := r.reader().List(ctx, &replicaSets, client.InNamespace(namespace)); err != nil {
			return workloads, fmt.Errorf("list ReplicaSets during worker-account quarantine: %w", err)
		}
		for i := range replicaSets.Items {
			if replicaSets.Items[i].Spec.Template.Spec.ServiceAccountName == serviceAccount {
				workloads.replicaSets = append(workloads.replicaSets, *replicaSets.Items[i].DeepCopy())
			}
		}
		var pods corev1.PodList
		if err := r.reader().List(ctx, &pods, client.InNamespace(namespace)); err != nil {
			return workloads, fmt.Errorf("list Pods during worker-account quarantine: %w", err)
		}
		for i := range pods.Items {
			if pods.Items[i].Spec.ServiceAccountName == serviceAccount {
				workloads.pods = append(workloads.pods, *pods.Items[i].DeepCopy())
			}
		}
		return workloads, nil
	}

	workloads, err := read()
	if err != nil {
		return false, err
	}
	var errs []error
	foreground := metav1.DeletePropagationForeground
	zero := int64(0)
	deleteOne := func(object client.Object, propagation *metav1.DeletionPropagation, grace *int64) {
		if object.GetUID() == "" {
			errs = append(errs, fmt.Errorf("refusing unpreconditioned quarantine delete of %T %s", object, client.ObjectKeyFromObject(object)))
			return
		}
		uid := object.GetUID()
		options := &client.DeleteOptions{
			Preconditions:      &metav1.Preconditions{UID: &uid},
			PropagationPolicy:  propagation,
			GracePeriodSeconds: grace,
		}
		if err := r.Client.Delete(ctx, object, options); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("quarantine delete %T %s: %w", object, client.ObjectKeyFromObject(object), err))
		}
	}
	for i := range workloads.deployments {
		deleteOne(&workloads.deployments[i], &foreground, nil)
	}
	for i := range workloads.replicaSets {
		deleteOne(&workloads.replicaSets[i], &foreground, nil)
	}
	for i := range workloads.pods {
		deleteOne(&workloads.pods[i], nil, &zero)
	}
	remaining, readErr := read()
	if readErr != nil {
		errs = append(errs, readErr)
	}
	drained := len(remaining.deployments) == 0 && len(remaining.replicaSets) == 0 && len(remaining.pods) == 0
	return drained, stderrors.Join(errs...)
}

func (r *CiscoDeviceReconciler) enforceManagedWorkerNamespaceRBAC(ctx context.Context,
	device *ciskov1.CiscoDevice, appSA, networkSA string) error {
	err := r.inspectManagedWorkerNamespaceRBAC(ctx, device, appSA, networkSA)
	if err == nil {
		return nil
	}
	return r.quarantineManagedSharedWorkersForRisk(ctx, device, err)
}

func (r *CiscoDeviceReconciler) quarantineManagedSharedWorkersForRisk(ctx context.Context,
	device *ciskov1.CiscoDevice, risk error) error {
	drained, quarantineErr := r.quarantineManagedSharedWorkers(ctx, device)
	if quarantineErr != nil {
		return fmt.Errorf("%w; additionally failed to quarantine existing shared worker bindings: %v", risk, quarantineErr)
	}
	if !drained {
		return fmt.Errorf("%w; shared bindings were revoked, shared account UIDs were rotated, and existing worker processes are draining", risk)
	}
	return fmt.Errorf("%w; shared bindings were revoked, shared account UIDs were rotated, and worker processes were quiesced", risk)
}

func (r *CiscoDeviceReconciler) inspectGeneratedWorkerLegacyTokens(ctx context.Context,
	device *ciskov1.CiscoDevice, serviceAccount string) error {
	var secrets corev1.SecretList
	if err := r.reader().List(ctx, &secrets, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("audit generated worker ServiceAccount tokens in namespace %q: %w", device.Namespace, err)
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if secret.Type == corev1.SecretTypeServiceAccountToken &&
			strings.TrimSpace(secret.Annotations[corev1.ServiceAccountNameKey]) == serviceAccount {
			return fmt.Errorf("long-lived ServiceAccount token Secret %s/%s references generated worker account %q; delete the token before granting worker access",
				secret.Namespace, secret.Name, serviceAccount)
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) inspectGeneratedWorkerWorkloads(ctx context.Context,
	device *ciskov1.CiscoDevice, serviceAccount string) (bool, error) {
	expectedName := device.Name + deploymentSuffix
	expectedLabels := perDeviceDeploymentLabels(device.Name)
	present := false
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("audit generated worker Deployments: %w", err)
	}
	matchedDeployments := map[string]*appsv1.Deployment{}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		present = true
		owner := metav1.GetControllerOf(deployment)
		if deployment.Name != expectedName || deployment.UID == "" || owner == nil ||
			owner.APIVersion != ciskov1.GroupVersion.String() || owner.Kind != "CiscoDevice" ||
			owner.Name != device.Name || owner.UID != device.UID ||
			!reflect.DeepEqual(deployment.Spec.Selector, &metav1.LabelSelector{MatchLabels: expectedLabels}) ||
			!labelsContain(deployment.Spec.Template.Labels, expectedLabels) {
			return true, fmt.Errorf("pre-existing Deployment %s/%s using generated worker account %q lacks exact CiscoDevice provenance",
				deployment.Namespace, deployment.Name, serviceAccount)
		}
		matchedDeployments[deployment.Name] = deployment
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return present, fmt.Errorf("audit generated worker ReplicaSets: %w", err)
	}
	matchedReplicaSets := map[string]*appsv1.ReplicaSet{}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if replicaSet.Spec.Template.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		present = true
		owner := metav1.GetControllerOf(replicaSet)
		deployment := matchedDeployments[expectedName]
		if replicaSet.UID == "" || !labelsContain(replicaSet.Labels, expectedLabels) ||
			!labelsContain(replicaSet.Spec.Template.Labels, expectedLabels) || owner == nil ||
			owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "Deployment" ||
			owner.Name != expectedName || owner.UID == "" || deployment == nil || owner.UID != deployment.UID {
			return true, fmt.Errorf("pre-existing ReplicaSet %s/%s using generated worker account %q lacks exact Deployment provenance",
				replicaSet.Namespace, replicaSet.Name, serviceAccount)
		}
		matchedReplicaSets[replicaSet.Name] = replicaSet
	}

	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return present, fmt.Errorf("audit generated worker Pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.ServiceAccountName != serviceAccount {
			continue
		}
		present = true
		owner := metav1.GetControllerOf(pod)
		replicaSet := matchedReplicaSets[ownerName(owner)]
		if !labelsContain(pod.Labels, expectedLabels) || owner == nil ||
			owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "ReplicaSet" ||
			owner.UID == "" || replicaSet == nil || owner.UID != replicaSet.UID {
			return true, fmt.Errorf("pre-existing Pod %s/%s using generated worker account %q lacks exact ReplicaSet provenance",
				pod.Namespace, pod.Name, serviceAccount)
		}
	}
	return present, nil
}

// quarantineGeneratedWorkerAccess is deliberately best-effort and
// authority-first. An attacker-controlled additive binding must not prevent
// revocation of each independently exact canonical object. During concrete
// compromise it also removes every namespaced grant to the generated identity;
// ordinary lifecycle cleanup retains additive objects for operator review.
// Foreign/drifted cluster-wide objects are always retained at the explicit
// cluster-admin trust boundary.
func (r *CiscoDeviceReconciler) quarantineGeneratedWorkerAccess(ctx context.Context,
	device *ciskov1.CiscoDevice, serviceAccount string, managed, compromise bool) error {
	expectedName := topologyLegacyWorkerServiceAccountName(device)
	workerRole := vkSharedClusterRole
	if managed {
		expectedName = managedWorkerServiceAccountName(device)
		workerRole = managedprotocol.ManagedWorkerClusterRole
	}
	if serviceAccount != expectedName {
		return fmt.Errorf("generated worker ServiceAccount name %q does not match bound identity %q", serviceAccount, expectedName)
	}
	expectedAnnotations := workerServiceAccountAnnotations(device, managed)
	var errs []error

	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, serviceAccount)}
	var crb rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, crbKey, &crb); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("read canonical generated ClusterRoleBinding %s: %w", crbKey, err))
		}
	} else if err := validateGeneratedClusterRoleBinding(&crb, device, serviceAccount, workerRole); err != nil ||
		!workerAnnotationsMatch(crb.Annotations, expectedAnnotations) {
		if err == nil {
			err = fmt.Errorf("binding metadata is not exactly incarnation-bound")
		}
		errs = append(errs, fmt.Errorf("retain drifted canonical generated ClusterRoleBinding %s: %w", crbKey, err))
	} else if err := deleteWithUIDPrecondition(ctx, r.Client, &crb); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete canonical generated ClusterRoleBinding %s: %w", crbKey, err))
	}

	if compromise {
		if err := r.quarantineGeneratedWorkerRoleBindings(ctx, device.Namespace, serviceAccount); err != nil {
			errs = append(errs, err)
		}
	} else {
		rbKey := types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}
		var rb rbacv1.RoleBinding
		if err := r.reader().Get(ctx, rbKey, &rb); err != nil {
			if !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("read canonical generated RoleBinding %s: %w", rbKey, err))
			}
		} else if err := validateGeneratedRoleBinding(&rb, device, serviceAccount); err != nil ||
			!workerAnnotationsMatch(rb.Annotations, expectedAnnotations) ||
			!managedServiceAccountOwnedByDeviceMeta(&rb.ObjectMeta, device) {
			if err == nil {
				err = fmt.Errorf("binding metadata is not exactly incarnation-bound")
			}
			errs = append(errs, fmt.Errorf("retain drifted canonical generated RoleBinding %s: %w", rbKey, err))
		} else if err := deleteWithUIDPrecondition(ctx, r.Client, &rb); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete canonical generated RoleBinding %s: %w", rbKey, err))
		}
	}

	saKey := types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}
	var sa corev1.ServiceAccount
	if err := r.reader().Get(ctx, saKey, &sa); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("read canonical generated ServiceAccount %s: %w", saKey, err))
		}
	} else if !managedServiceAccountOwnedByDevice(&sa, device) ||
		!workerAnnotationsMatch(sa.Annotations, expectedAnnotations) {
		errs = append(errs, fmt.Errorf("retain foreign or drifted canonical generated ServiceAccount %s", saKey))
	} else if err := deleteWithUIDPrecondition(ctx, r.Client, &sa); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete canonical generated ServiceAccount %s: %w", saKey, err))
	}
	return stderrors.Join(errs...)
}

func (r *CiscoDeviceReconciler) quarantineGeneratedWorkerRoleBindings(ctx context.Context,
	namespace, serviceAccount string) error {
	var bindings rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("list generated worker RoleBindings in namespace %q during quarantine: %w", namespace, err)
	}
	var errs []error
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if !hasWorkerSubject(binding.Subjects, namespace, serviceAccount) {
			continue
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, binding); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("quarantine generated worker RoleBinding %s: %w",
				client.ObjectKeyFromObject(binding), err))
		}
	}
	var remaining rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &remaining, client.InNamespace(namespace)); err != nil {
		errs = append(errs, fmt.Errorf("verify generated worker RoleBindings in namespace %q after quarantine: %w",
			namespace, err))
	} else {
		for i := range remaining.Items {
			binding := &remaining.Items[i]
			if hasWorkerSubject(binding.Subjects, namespace, serviceAccount) {
				errs = append(errs, fmt.Errorf("generated worker RoleBinding %s remains after quarantine",
					client.ObjectKeyFromObject(binding)))
			}
		}
	}
	return stderrors.Join(errs...)
}

// enforceGeneratedWorkerNamespaceSafety runs before and after generated access
// is bound. Namespace RBAC can otherwise impersonate the UID-derived account
// (or collection-delete it), while an upgrade may retain a long-lived token
// Secret created before admission reserved these names. On any risk, revoke
// exact RBAC first and quiesce the exact device-owned worker plane because an
// exec-capable identity may already have exposed device credentials.
func (r *CiscoDeviceReconciler) enforceGeneratedWorkerNamespaceSafety(ctx context.Context,
	device *ciskov1.CiscoDevice, serviceAccount string, managed bool) error {
	var concreteFindings []error
	inventory, inventoryErr := r.generatedWorkerAccessInventory(ctx, device, serviceAccount, managed)
	accessComplete := inventoryErr == nil && inventory.count() == 3 && !inventory.deleting()
	if inventoryErr != nil {
		concreteFindings = append(concreteFindings, inventoryErr)
	}
	epochMismatch := false
	if strings.TrimSpace(r.WorkerServiceAccountPolicyEpoch) == "" {
		concreteFindings = append(concreteFindings, fmt.Errorf("generated worker ServiceAccount policy epoch is empty"))
	} else if inventory.serviceAccount != nil &&
		inventory.serviceAccount.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy] != r.WorkerServiceAccountPolicyEpoch {
		epochMismatch = true
		accessComplete = false
	}
	if err := r.inspectManagedWorkerNamespaceRBAC(ctx, device,
		r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()); err != nil {
		concreteFindings = append(concreteFindings, err)
	}
	if err := r.inspectGeneratedWorkerLegacyTokens(ctx, device, serviceAccount); err != nil {
		concreteFindings = append(concreteFindings, err)
	}
	workloadsPresent, workloadErr := r.inspectGeneratedWorkerWorkloads(ctx, device, serviceAccount)
	if workloadErr != nil {
		concreteFindings = append(concreteFindings, workloadErr)
	} else if workloadsPresent && !accessComplete {
		// An old admission epoch is a planned rotation rather than compromise;
		// its otherwise-canonical workload is handled below after mutation
		// settlement. Partial/deleting access is concrete risk and must be
		// quarantined immediately.
		if !epochMismatch || inventoryErr != nil || inventory.count() != 3 || inventory.deleting() {
			concreteFindings = append(concreteFindings,
				fmt.Errorf("generated worker workloads remain while canonical generated access is absent, partial, or deleting"))
		}
	}
	quarantine := func(risk error) error {
		var quarantineErrors []error
		if err := r.quarantineGeneratedWorkerAccess(ctx, device, serviceAccount, managed, true); err != nil {
			quarantineErrors = append(quarantineErrors, fmt.Errorf("revoke generated worker access: %w", err))
		}
		drained, err := r.quarantineWorkerServiceAccountWorkloads(ctx, device.Namespace, serviceAccount)
		if err != nil {
			quarantineErrors = append(quarantineErrors, fmt.Errorf("quiesce generated worker processes: %w", err))
			drained = false
		}
		if quarantineErr := stderrors.Join(quarantineErrors...); quarantineErr != nil {
			return fmt.Errorf("%w; additionally failed to quarantine generated worker authority: %v", risk, quarantineErr)
		}
		if !drained {
			return fmt.Errorf("%w; generated bindings were revoked and worker processes are draining", risk)
		}
		return fmt.Errorf("%w; generated bindings were revoked and worker processes were quiesced", risk)
	}

	if risk := stderrors.Join(concreteFindings...); risk != nil {
		// Unsafe RBAC, attributable legacy tokens, malformed access, or
		// unproven workloads are compromise evidence. Revoke immediately even
		// when an epoch mismatch also exists; a planned-rotation fence must not
		// preserve compromised authority.
		if epochMismatch {
			risk = stderrors.Join(risk, fmt.Errorf(
				"generated worker ServiceAccount predates the verified reserved-account admission generation and requires UID rotation"))
		}
		return quarantine(risk)
	}
	if !epochMismatch {
		return nil
	}

	// Epoch-only rotation is planned maintenance. Never interrupt an active
	// gNOI/config transaction merely because the chart's verified admission
	// generation changed. A phase-zero legacy worker has no durable NodeIdentity
	// from which the full managed authority proof can be derived, so require its
	// workload to be quiesced explicitly before rotating the generated UID.
	if err := r.ensureManagedDeviceAuthoritiesSettledForAccessTransition(ctx, device); err != nil {
		return fmt.Errorf("planned generated worker policy-epoch rotation is blocked until mutation authority settles: %w", err)
	}
	if device.Status.NodeIdentity == nil && workloadsPresent {
		return fmt.Errorf("planned generated worker policy-epoch rotation is blocked: phase-zero legacy worker %s/%s has no durable Node identity; verify device operations are idle and remove its worker workload before retrying",
			device.Namespace, device.Name)
	}
	return quarantine(fmt.Errorf(
		"generated worker ServiceAccount predates the verified reserved-account admission generation and requires UID rotation"))
}

func (r *CiscoDeviceReconciler) mapNamespacedRBACToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	if obj == nil || obj.GetNamespace() == "" {
		return nil
	}
	namespaces := map[string]struct{}{obj.GetNamespace(): {}}
	switch object := obj.(type) {
	case *rbacv1.RoleBinding:
		for _, subject := range object.Subjects {
			if reservedSharedWorkerSubject(subject, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()) {
				namespaces[subject.Namespace] = struct{}{}
			}
		}
	case *rbacv1.Role:
		var bindings rbacv1.RoleBindingList
		if err := r.reader().List(ctx, &bindings, client.InNamespace(object.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "list RoleBindings for Role mapping", "role", client.ObjectKeyFromObject(object))
			break
		}
		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "Role" ||
				binding.RoleRef.Name != object.Name {
				continue
			}
			for _, subject := range binding.Subjects {
				if reservedSharedWorkerSubject(subject, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()) {
					namespaces[subject.Namespace] = struct{}{}
				}
			}
		}
	}
	return r.mapRBACNamespacesToCiscoDevices(ctx, namespaces, "namespaced RBAC")
}

func (r *CiscoDeviceReconciler) mapClusterRoleToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	role, ok := obj.(*rbacv1.ClusterRole)
	if !ok || role.Name == "" {
		return nil
	}
	var bindings rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &bindings); err != nil {
		log.FromContext(ctx).Error(err, "list RoleBindings for ClusterRole mapping", "clusterRole", role.Name)
		return nil
	}
	namespaces := map[string]struct{}{}
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if binding.RoleRef.APIGroup == rbacv1.GroupName && binding.RoleRef.Kind == "ClusterRole" &&
			binding.RoleRef.Name == role.Name && len(binding.Subjects) != 0 {
			namespaces[binding.Namespace] = struct{}{}
			for _, subject := range binding.Subjects {
				if reservedSharedWorkerSubject(subject, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()) {
					namespaces[subject.Namespace] = struct{}{}
				}
			}
		}
	}
	var clusterBindings rbacv1.ClusterRoleBindingList
	if err := r.reader().List(ctx, &clusterBindings); err != nil {
		log.FromContext(ctx).Error(err, "list ClusterRoleBindings for ClusterRole mapping", "clusterRole", role.Name)
		return nil
	}
	for i := range clusterBindings.Items {
		binding := &clusterBindings.Items[i]
		if binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" ||
			binding.RoleRef.Name != role.Name {
			continue
		}
		for _, subject := range binding.Subjects {
			if reservedSharedWorkerSubject(subject, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()) {
				namespaces[subject.Namespace] = struct{}{}
			}
		}
	}
	return r.mapRBACNamespacesToCiscoDevices(ctx, namespaces, "ClusterRole")
}

func reservedSharedWorkerSubject(subject rbacv1.Subject, appSA, networkSA string) bool {
	return subject.Kind == rbacv1.ServiceAccountKind && subject.APIGroup == "" && subject.Namespace != "" &&
		(subject.Name == appSA || subject.Name == networkSA)
}

func (r *CiscoDeviceReconciler) mapClusterRoleBindingToCiscoDevices(ctx context.Context,
	obj client.Object) []ctrl.Request {
	binding, ok := obj.(*rbacv1.ClusterRoleBinding)
	if !ok {
		return nil
	}
	namespaces := map[string]struct{}{}
	for _, subject := range binding.Subjects {
		if reservedSharedWorkerSubject(subject, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()) {
			namespaces[subject.Namespace] = struct{}{}
		}
	}
	return r.mapRBACNamespacesToCiscoDevices(ctx, namespaces, "ClusterRoleBinding")
}

func (r *CiscoDeviceReconciler) mapRBACNamespacesToCiscoDevices(ctx context.Context,
	namespaces map[string]struct{}, source string) []ctrl.Request {
	ordered := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		ordered = append(ordered, namespace)
	}
	sort.Strings(ordered)
	requests := []ctrl.Request{}
	for _, namespace := range ordered {
		requests = append(requests, r.mapNamespaceToCiscoDevices(ctx, namespace, source)...)
	}
	return requests
}

func (r *CiscoDeviceReconciler) mapNamespaceToCiscoDevices(ctx context.Context, namespace, source string) []ctrl.Request {
	var devices ciskov1.CiscoDeviceList
	if err := r.List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list CiscoDevices for RBAC mapping", "namespace", namespace, "source", source)
		return nil
	}
	sort.Slice(devices.Items, func(i, j int) bool { return devices.Items[i].Name < devices.Items[j].Name })
	requests := make([]ctrl.Request, 0, len(devices.Items))
	for i := range devices.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&devices.Items[i])})
	}
	return requests
}
