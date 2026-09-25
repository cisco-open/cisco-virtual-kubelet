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

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	plaintextCredential bool
}

func newManagedWorkerNameScope(devices []ciskov1.CiscoDevice, current *ciskov1.CiscoDevice) managedWorkerNameScope {
	names := make(map[string]struct{}, 2*(len(devices)+1))
	scopePlaintext := false
	add := func(name, uid string) {
		if name == "" {
			return
		}
		names[name+deploymentSuffix] = struct{}{}
		names[networkDeploymentName(name, uid)] = struct{}{}
	}
	for i := range devices {
		add(devices[i].Name, string(devices[i].UID))
		if strings.TrimSpace(devices[i].Spec.Password) != "" {
			scopePlaintext = true
		}
	}
	if current != nil {
		add(current.Name, string(current.UID))
		if strings.TrimSpace(current.Spec.Password) != "" {
			scopePlaintext = true
		}
	}
	prefixes := make([]string, 0, len(names))
	for name := range names {
		prefixes = append(prefixes, name+"-")
	}
	sort.Strings(prefixes)
	return managedWorkerNameScope{deployments: names, podPrefixes: prefixes, plaintextCredential: scopePlaintext}
}

func (s managedWorkerNameScope) includes(resource, name string) bool {
	if name == "*" { // Not an RBAC wildcard, but fail closed on ambiguous intent.
		return true
	}
	switch resource {
	case "deployments", "deployments/scale", "deployments/status":
		_, found := s.deployments[name]
		return found
	case "replicasets", "replicasets/scale", "replicasets/status", "pods", "pods/status", "pods/exec", "pods/attach", "pods/portforward", "pods/proxy", "pods/log", "pods/eviction":
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

func trustedSharedWorkerRoleBinding(binding *rbacv1.RoleBinding, namespace, appSA, networkSA string) bool {
	if binding == nil || binding.Namespace != namespace ||
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
		if binding.Name != candidate.serviceAccount ||
			!reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, candidate.serviceAccount)) {
			continue
		}
		for _, role := range candidate.roles {
			if binding.RoleRef.Name == role &&
				sharedWorkerMetadataMatches(binding, namespace, candidate.account, role) {
				return true
			}
		}
	}
	return false
}

func unsafeManagedNamespaceRule(rule rbacv1.PolicyRule, appSA, networkSA string,
	scope managedWorkerNameScope, allowTrustedReads bool) string {
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

	if policyValueMatches(rule.APIGroups, "") && policyResourceMatches(rule.Resources, "serviceaccounts") &&
		policyVerbMatches(rule.Verbs, "impersonate") {
		if len(rule.ResourceNames) == 0 {
			return fmt.Sprintf("impersonation of reserved ServiceAccounts %q or %q", appSA, networkSA)
		}
		for _, name := range rule.ResourceNames {
			if name == "*" || name == appSA || name == networkSA {
				return fmt.Sprintf("impersonation of reserved ServiceAccount %q", name)
			}
		}
	}

	if policyValueMatches(rule.APIGroups, "") {
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
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: binding.RoleRef.Name}, &role)
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
		err := r.Client.Get(ctx, types.NamespacedName{Name: binding.RoleRef.Name}, &role)
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
	var bindings rbacv1.RoleBindingList
	if err := r.Client.List(ctx, &bindings, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list RoleBindings in managed device namespace %q: %w", device.Namespace, err)
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.Client.List(ctx, &devices, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list CiscoDevices for managed worker RBAC name scope in namespace %q: %w", device.Namespace, err)
	}
	scope := newManagedWorkerNameScope(devices.Items, device)
	sort.Slice(bindings.Items, func(i, j int) bool { return bindings.Items[i].Name < bindings.Items[j].Name })
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if len(binding.Subjects) == 0 {
			continue
		}
		rules, clusterRole, found, err := r.resolveNamespacedRoleBindingRules(ctx, binding)
		if err != nil {
			return err
		}
		// A dangling RoleBinding grants nothing. Role and ClusterRole watches
		// re-run this audit as soon as its role appears.
		if !found {
			continue
		}
		trusted := trustedSharedWorkerRoleBinding(binding, device.Namespace, appSA, networkSA)
		if trusted {
			if clusterRole == nil {
				return fmt.Errorf("trusted shared worker RoleBinding %s does not resolve to a ClusterRole", client.ObjectKeyFromObject(binding))
			}
			if err := managedprotocol.ValidateWorkerClusterRole(clusterRole); err != nil {
				return fmt.Errorf("trusted shared worker RoleBinding %s resolves to an invalid fixed role: %w",
					client.ObjectKeyFromObject(binding), err)
			}
		}
		for ruleIndex, rule := range rules {
			if capability := unsafeManagedNamespaceRule(rule, appSA, networkSA, scope, trusted); capability != "" {
				return &namespaceRBACRisk{
					binding: client.ObjectKeyFromObject(binding), roleRef: binding.RoleRef,
					rule: ruleIndex + 1, capability: capability,
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

	// Revoking API authorization is not enough after an exec-capable binding
	// has existed: an injected process already holds device credentials and can
	// continue talking directly to the switch. Foreground-delete both exact,
	// device-owned worker planes and do not consider quarantine complete until
	// every Deployment, ReplicaSet, and Pod using either shared identity is gone.
	drained := len(errs) == 0
	for _, plane := range []struct {
		name           string
		serviceAccount string
	}{
		{managedprotocol.WorkerModeAppHosting, r.appHostingServiceAccountName()},
		{managedprotocol.WorkerModeNetworkManagement, r.networkManagementServiceAccountName()},
	} {
		planeDrained, err := r.drainSharedWorkerPlane(ctx, namespace, plane.name, plane.serviceAccount)
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

func (r *CiscoDeviceReconciler) enforceManagedWorkerNamespaceRBAC(ctx context.Context,
	device *ciskov1.CiscoDevice, appSA, networkSA string) error {
	err := r.inspectManagedWorkerNamespaceRBAC(ctx, device, appSA, networkSA)
	if err == nil {
		return nil
	}
	drained, quarantineErr := r.quarantineManagedSharedWorkers(ctx, device)
	if quarantineErr != nil {
		return fmt.Errorf("%w; additionally failed to quarantine existing shared worker bindings: %v", err, quarantineErr)
	}
	if !drained {
		return fmt.Errorf("%w; shared bindings were revoked and existing worker processes are draining", err)
	}
	return fmt.Errorf("%w; shared bindings were revoked and worker processes were quiesced", err)
}

func (r *CiscoDeviceReconciler) mapNamespacedRBACToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	if obj == nil || obj.GetNamespace() == "" {
		return nil
	}
	return r.mapNamespaceToCiscoDevices(ctx, obj.GetNamespace(), "namespaced RBAC")
}

func (r *CiscoDeviceReconciler) mapClusterRoleToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	role, ok := obj.(*rbacv1.ClusterRole)
	if !ok || role.Name == "" {
		return nil
	}
	var bindings rbacv1.RoleBindingList
	if err := r.List(ctx, &bindings); err != nil {
		log.FromContext(ctx).Error(err, "list RoleBindings for ClusterRole mapping", "clusterRole", role.Name)
		return nil
	}
	namespaces := map[string]struct{}{}
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if binding.RoleRef.APIGroup == rbacv1.GroupName && binding.RoleRef.Kind == "ClusterRole" &&
			binding.RoleRef.Name == role.Name && len(binding.Subjects) != 0 {
			namespaces[binding.Namespace] = struct{}{}
		}
	}
	var clusterBindings rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &clusterBindings); err != nil {
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
	ordered := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		ordered = append(ordered, namespace)
	}
	sort.Strings(ordered)
	requests := []ctrl.Request{}
	for _, namespace := range ordered {
		requests = append(requests, r.mapNamespaceToCiscoDevices(ctx, namespace, "ClusterRole")...)
	}
	return requests
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
	ordered := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		ordered = append(ordered, namespace)
	}
	sort.Strings(ordered)
	requests := []ctrl.Request{}
	for _, namespace := range ordered {
		requests = append(requests, r.mapNamespaceToCiscoDevices(ctx, namespace, "ClusterRoleBinding")...)
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
