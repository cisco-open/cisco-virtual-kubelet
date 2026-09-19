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
	"fmt"
	"reflect"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func managedWorkerClusterRoleContracts() []rbacv1.ClusterRole {
	read := func() []string { return []string{"get", "list", "watch"} }
	return []rbacv1.ClusterRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.ManagedWorkerClusterRole},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get"}},
				{APIGroups: []string{""}, Resources: []string{"nodes/status"}, Verbs: []string{"get", "update", "patch"}},
				{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: read()},
				{APIGroups: []string{""}, Resources: []string{"pods/status"}, Verbs: []string{"get", "update", "patch"}},
				{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: read()},
				{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: read()},
				{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: read()},
				{APIGroups: []string{""}, Resources: []string{"events"}, Verbs: []string{"create", "patch"}},
				{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"get", "list", "watch", "update", "patch"}},
				{APIGroups: []string{"cisco.vk"}, Resources: []string{"ciscodevices"}, Verbs: read()},
				{APIGroups: []string{"config.cisco.vk"}, Resources: []string{"iosxeconfigdefaults"}, Verbs: read()},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.ManagedWorkerPodDeleteClusterRole},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"delete"},
			}},
		},
	}
}

// verifyManagedWorkerClusterRoles attests the complete live authority which a
// generated managed worker receives. A retained role may outlive Helm, so
// admission compatibility alone is insufficient: any rule drift must prevent
// the controller from creating or repairing per-device bindings.
func verifyManagedWorkerClusterRoles(ctx context.Context, reader client.Reader) error {
	if reader == nil {
		return fmt.Errorf("managed worker RBAC reader is nil")
	}
	for _, expected := range managedWorkerClusterRoleContracts() {
		var actual rbacv1.ClusterRole
		key := types.NamespacedName{Name: expected.Name}
		if err := reader.Get(ctx, key, &actual); err != nil {
			return fmt.Errorf("read managed worker ClusterRole %s: %w", expected.Name, err)
		}
		if !actual.DeletionTimestamp.IsZero() {
			return fmt.Errorf("managed worker ClusterRole %s is terminating", expected.Name)
		}
		if actual.AggregationRule != nil {
			return fmt.Errorf("managed worker ClusterRole %s must not use rule aggregation", expected.Name)
		}
		if !reflect.DeepEqual(actual.Rules, expected.Rules) {
			return fmt.Errorf("managed worker ClusterRole %s rules do not match the compiled contract", expected.Name)
		}
	}
	return nil
}
