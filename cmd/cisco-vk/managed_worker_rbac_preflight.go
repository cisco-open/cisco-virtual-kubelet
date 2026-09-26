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
	"slices"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func managedWorkerClusterRoleContracts() []rbacv1.ClusterRole {
	contracts := managedprotocol.WorkerClusterRoleContracts()
	names := make([]string, 0, len(contracts))
	for name := range contracts {
		names = append(names, name)
	}
	slices.Sort(names)
	roles := make([]rbacv1.ClusterRole, 0, len(names))
	for _, name := range names {
		roles = append(roles, rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: contracts[name]})
	}
	return roles
}

// verifyManagedWorkerClusterRoles attests the complete live authority which a
// shared functional worker receives. A retained role may outlive Helm, so
// admission compatibility alone is insufficient: any rule drift must prevent
// the controller from creating or repairing functional worker bindings.
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
		if err := managedprotocol.ValidateWorkerClusterRole(&actual); err != nil {
			return fmt.Errorf("managed worker ClusterRole %s rules do not match the compiled contract", expected.Name)
		}
	}
	return nil
}
