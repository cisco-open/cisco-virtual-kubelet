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
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestWorkerClusterRoleContracts(t *testing.T) {
	contracts := WorkerClusterRoleContracts()
	if len(contracts) != 8 {
		t.Fatalf("contract role count = %d, want 8", len(contracts))
	}
	for name, rules := range contracts {
		role := &rbacv1.ClusterRole{Rules: rules}
		role.Name = name
		if err := ValidateWorkerClusterRole(role); err != nil {
			t.Fatalf("valid role %s: %v", name, err)
		}
	}
}

func TestValidateWorkerClusterRoleRejectsBroaderOrAggregatedRole(t *testing.T) {
	rules := WorkerClusterRoleContracts()[AppHostingReadOnlyClusterRole]
	role := &rbacv1.ClusterRole{Rules: rules}
	role.Name = AppHostingReadOnlyClusterRole
	role.Rules[0].Verbs = append(role.Rules[0].Verbs, "update")
	if err := ValidateWorkerClusterRole(role); err == nil {
		t.Fatal("broadened role was accepted")
	}

	role.Rules = WorkerClusterRoleContracts()[AppHostingReadOnlyClusterRole]
	role.AggregationRule = &rbacv1.AggregationRule{}
	if err := ValidateWorkerClusterRole(role); err == nil {
		t.Fatal("aggregated role was accepted")
	}
}

func TestValidateWorkerClusterRoleIgnoresRuleAndTupleOrdering(t *testing.T) {
	rules := WorkerClusterRoleContracts()[AppHostingReadOnlyClusterRole]
	rules[0], rules[1] = rules[1], rules[0]
	rules[0].Verbs[0], rules[0].Verbs[2] = rules[0].Verbs[2], rules[0].Verbs[0]
	role := &rbacv1.ClusterRole{Rules: rules}
	role.Name = AppHostingReadOnlyClusterRole
	if err := ValidateWorkerClusterRole(role); err != nil {
		t.Fatalf("semantically identical role ordering: %v", err)
	}
}
