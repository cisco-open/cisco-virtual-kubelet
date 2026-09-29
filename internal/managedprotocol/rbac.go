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

package managedprotocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	rbacv1 "k8s.io/api/rbac/v1"
)

// WorkerClusterRoleContracts is the binary-side authorization contract for
// the four selectable profiles and their implementation-only support roles.
// A manager must never bind one of these stable names after a broader role was
// substituted beneath it.
func WorkerClusterRoleContracts() map[string][]rbacv1.PolicyRule {
	return map[string][]rbacv1.PolicyRule{
		AppHostingReadOnlyClusterRole: {
			policyRule([]string{""}, []string{"nodes"}, "get", "list", "watch"),
			policyRule([]string{""}, []string{"pods"}, "get", "list", "watch"),
		},
		AppHostingReadWriteClusterRole: {
			policyRule([]string{""}, []string{"nodes"}, "get", "list", "watch"),
			policyRule([]string{""}, []string{"nodes/status"}, "get", "update", "patch"),
			policyRule([]string{""}, []string{"pods"}, "get", "list", "watch", "delete"),
			policyRule([]string{""}, []string{"pods/status"}, "get", "update", "patch"),
			policyRule([]string{""}, []string{"configmaps", "secrets", "services"}, "get", "list", "watch"),
			policyRule([]string{""}, []string{"events"}, "create", "patch"),
			policyRule([]string{"coordination.k8s.io"}, []string{"leases"}, "get", "list", "watch", "update", "patch"),
			policyRule([]string{"authentication.k8s.io"}, []string{"selfsubjectreviews"}, "create"),
		},
		AppHostingDeviceReadClusterRole: {
			policyRule([]string{"cisco.vk"}, []string{"ciscodevices"}, "get"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxesoftwareupgrades", "iosxeoperationalactions"}, "list"),
		},
		NetworkManagementGlobalReadClusterRole: {
			policyRule([]string{""}, []string{"nodes"}, "get"),
			policyRule([]string{""}, []string{"pods"}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigdefaults"}, "get", "list", "watch"),
			policyRule([]string{"authentication.k8s.io"}, []string{"selfsubjectreviews"}, "create"),
		},
		NetworkManagementLeaseReadOnlyClusterRole: {
			policyRule([]string{"coordination.k8s.io"}, []string{"leases"}, "get", "list", "watch"),
		},
		NetworkManagementLeaseReadWriteClusterRole: {
			policyRule([]string{"coordination.k8s.io"}, []string{"leases"}, "get", "list", "watch", "update", "patch"),
		},
		NetworkManagementReadOnlyClusterRole: {
			policyRule([]string{"cisco.vk"}, []string{"ciscodevices"}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{
				"iosxedevicegroupconfigs", "iosxeinterfacegroupconfigs", "iosxetemplates", "iosxeconfigs",
				"nxosconfigs", "iosxeconfigapplylogs", "iosxeconfigrevisions",
			}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxetelemetries"}, "get", "list", "watch", "update", "patch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxediagnostics"}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxetelemetries/status", "iosxediagnostics/status"}, "get", "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"deviceoperations"}, "get", "list", "watch", "create", "delete"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxesoftwareupgrades", "iosxeoperationalactions"}, "get", "list", "watch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"deviceoperations/status"}, "get", "update", "patch"),
			policyRule([]string{""}, []string{"configmaps"}, "get", "list", "watch", "create", "update", "patch", "delete"),
			policyRule([]string{""}, []string{"events"}, "create", "patch"),
			policyRule([]string{"coordination.k8s.io"}, []string{"leases"}, "get", "list", "watch"),
		},
		NetworkManagementReadWriteClusterRole: {
			policyRule([]string{"cisco.vk"}, []string{"ciscodevices"}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxedevicegroupconfigs", "iosxeinterfacegroupconfigs", "iosxetemplates"}, "get", "list", "watch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigs", "nxosconfigs", "iosxetelemetries", "iosxediagnostics"}, "get", "list", "watch", "update", "patch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigs/status", "nxosconfigs/status", "iosxetelemetries/status", "iosxediagnostics/status"}, "get", "update", "patch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigapplylogs"}, "get", "list", "watch", "create", "update", "patch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigapplylogs/status"}, "get", "update", "patch"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigrevisions"}, "get", "list", "watch", "create", "update", "patch", "delete"),
			policyRule([]string{"config.cisco.vk"}, []string{"iosxeconfigrevisions/status"}, "get", "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"deviceoperations"}, "get", "list", "watch", "create", "delete"),
			policyRule([]string{"ops.cisco.vk"}, []string{"deviceoperations/status"}, "get", "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxesoftwareupgrades", "iosxeoperationalactions"}, "get", "list", "watch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxesoftwareupgrades"}, "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxesoftwareupgrades/status"}, "get", "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxeoperationalactions"}, "update", "patch"),
			policyRule([]string{"ops.cisco.vk"}, []string{"iosxeoperationalactions/status"}, "get", "update", "patch"),
			policyRule([]string{""}, []string{"configmaps"}, "get", "list", "watch", "create", "update", "patch", "delete"),
			policyRule([]string{""}, []string{"secrets"}, "get", "list", "watch"),
			policyRule([]string{""}, []string{"events"}, "create", "patch"),
			policyRule([]string{"coordination.k8s.io"}, []string{"leases"}, "get", "list", "watch", "update", "patch"),
		},
	}
}

func policyRule(apiGroups, resources []string, verbs ...string) rbacv1.PolicyRule {
	return rbacv1.PolicyRule{APIGroups: apiGroups, Resources: resources, Verbs: verbs}
}

// ValidateWorkerClusterRole checks the effective rule shape, rather than a
// mutable annotation, so a same-name role cannot be broadened beneath the
// manager's narrowly delegated bind permission.
func ValidateWorkerClusterRole(role *rbacv1.ClusterRole) error {
	if role == nil {
		return fmt.Errorf("worker ClusterRole is nil")
	}
	expected, ok := WorkerClusterRoleContracts()[role.Name]
	if !ok {
		return fmt.Errorf("ClusterRole %q is not a managed worker contract role", role.Name)
	}
	if role.AggregationRule != nil {
		return fmt.Errorf("ClusterRole %q must not use aggregation", role.Name)
	}
	if !reflect.DeepEqual(canonicalPolicyRules(role.Rules), canonicalPolicyRules(expected)) {
		return fmt.Errorf("ClusterRole %q rules differ from the compiled managed worker contract", role.Name)
	}
	return nil
}

func canonicalPolicyRules(rules []rbacv1.PolicyRule) []string {
	canonical := make([]string, 0, len(rules))
	for _, rule := range rules {
		rule.APIGroups = append([]string(nil), rule.APIGroups...)
		rule.Resources = append([]string(nil), rule.Resources...)
		rule.ResourceNames = append([]string(nil), rule.ResourceNames...)
		rule.NonResourceURLs = append([]string(nil), rule.NonResourceURLs...)
		rule.Verbs = append([]string(nil), rule.Verbs...)
		sort.Strings(rule.APIGroups)
		sort.Strings(rule.Resources)
		sort.Strings(rule.ResourceNames)
		sort.Strings(rule.NonResourceURLs)
		sort.Strings(rule.Verbs)
		encoded, _ := json.Marshal(rule)
		canonical = append(canonical, string(encoded))
	}
	sort.Strings(canonical)
	return canonical
}
