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

package topologyrollout

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

func TestParseAdminPolicyProducesUIDBoundAdmissionPolicy(t *testing.T) {
	cfg := validAdminPolicyConfig()
	data, err := CanonicalPolicyJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cvk-system", Name: "topology-policy", UID: types.UID("policy-uid"), ResourceVersion: "41",
			Annotations: map[string]string{
				PolicyManagedAnnotation: "true", LedgerUIDAnnotation: "ledger-uid",
				AdmissionPrefixAnnotation: "cvk",
			},
		},
		Data: map[string]string{PolicyDataKey: data},
	}
	parsed, err := ParseAdminPolicy(cm)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Selector.Matches(labels.Set{"topology.cisco.vk/managed": "true"}) {
		t.Fatal("fleet selector did not match managed device")
	}
	now := time.Now().UTC()
	admission := parsed.AdmissionPolicy(now)
	if admission.UID != "policy-uid" || admission.Version != "41" || admission.RequiredHealthFreshBy != now.Add(-2*time.Minute) {
		t.Fatalf("AdmissionPolicy() = %+v", admission)
	}
}

func TestAdminPolicyHashesSeparateSemanticAndStructuralChanges(t *testing.T) {
	base := validAdminPolicyConfig()
	semantic, structural, err := AdminPolicyHashes(base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := base
	reordered.RequiredTopologyKeys = append([]string(nil), base.RequiredTopologyKeys...)
	reordered.ProjectedTopologyKeys = append([]string(nil), base.ProjectedTopologyKeys...)
	for left, right := 0, len(reordered.RequiredTopologyKeys)-1; left < right; left, right = left+1, right-1 {
		reordered.RequiredTopologyKeys[left], reordered.RequiredTopologyKeys[right] = reordered.RequiredTopologyKeys[right], reordered.RequiredTopologyKeys[left]
	}
	semanticReordered, structuralReordered, err := AdminPolicyHashes(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if semanticReordered != semantic || structuralReordered != structural {
		t.Fatal("order-only collection rewrite changed canonical policy hashes")
	}
	tightened := base
	tightened.GlobalMaxConcurrentTransfers--
	semanticTight, structuralTight, err := AdminPolicyHashes(tightened)
	if err != nil {
		t.Fatal(err)
	}
	if semanticTight == semantic || structuralTight != structural {
		t.Fatal("numeric policy edit did not change only the semantic hash")
	}
	changedStructure := base
	changedStructure.ProjectedTopologyKeys = nil
	_, structuralChanged, err := AdminPolicyHashes(changedStructure)
	if err != nil {
		t.Fatal(err)
	}
	if structuralChanged == structural {
		t.Fatal("projected topology change did not change the structural hash")
	}
}

func TestAdminPolicyValidationFailsClosed(t *testing.T) {
	tests := map[string]func(*AdminPolicyConfig){
		"empty fleet selector": func(cfg *AdminPolicyConfig) { cfg.FleetSelector = metav1.LabelSelector{} },
		"unprotected fleet match label": func(cfg *AdminPolicyConfig) {
			cfg.FleetSelector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "router"}}
		},
		"unprotected fleet expression": func(cfg *AdminPolicyConfig) {
			cfg.FleetSelector = metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key: "operations.cisco.vk/ring", Operator: metav1.LabelSelectorOpExists,
			}}}
		},
		"no required keys": func(cfg *AdminPolicyConfig) { cfg.RequiredTopologyKeys = nil },
		"unknown key": func(cfg *AdminPolicyConfig) {
			cfg.RequiredTopologyKeys = append(cfg.RequiredTopologyKeys, "example.com/not-topology")
		},
		"projected is not required": func(cfg *AdminPolicyConfig) {
			cfg.ProjectedTopologyKeys = append(cfg.ProjectedTopologyKeys, "topology.cisco.vk/rack")
		},
		"unprotected budget": func(cfg *AdminPolicyConfig) {
			cfg.DomainMaxUnavailable["topology.cisco.vk/rack"] = 1
		},
		"zero budget": func(cfg *AdminPolicyConfig) {
			cfg.DomainMaxUnavailable["topology.cisco.vk/site"] = 0
		},
		"oversize ledger": func(cfg *AdminPolicyConfig) { cfg.MaxLedgerBytes = DefaultMaxSerializedBytes + 1 },
		"oversize global budget": func(cfg *AdminPolicyConfig) {
			cfg.GlobalMaxUnavailable = DefaultMaxCampaignTargets + 1
		},
		"oversize domain budget": func(cfg *AdminPolicyConfig) {
			cfg.DomainMaxConcurrentTransfers["topology.cisco.vk/site"] = DefaultMaxCampaignTargets + 1
		},
		"operational key projected": func(cfg *AdminPolicyConfig) {
			cfg.RequiredTopologyKeys = append(cfg.RequiredTopologyKeys, "operations.cisco.vk/upgrade-ring")
			cfg.ProjectedTopologyKeys = append(cfg.ProjectedTopologyKeys, "operations.cisco.vk/upgrade-ring")
		},
		"oversize fleet selector": func(cfg *AdminPolicyConfig) {
			cfg.FleetSelector.MatchLabels = map[string]string{}
			for i := 0; i < 33; i++ {
				cfg.FleetSelector.MatchLabels["example.com/key-"+strconv.Itoa(i)] = "value"
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validAdminPolicyConfig()
			mutate(&cfg)
			if _, err := CanonicalPolicyJSON(cfg); err == nil {
				t.Fatal("CanonicalPolicyJSON() accepted invalid policy")
			}
		})
	}
}

func TestAdminPolicyRejectsUnknownJSONFields(t *testing.T) {
	data, err := CanonicalPolicyJSON(validAdminPolicyConfig())
	if err != nil {
		t.Fatal(err)
	}
	data = strings.TrimSuffix(data, "}") + `,"globalMaxUnvailable":3}`
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cvk-system", Name: "topology-policy", UID: "policy-uid", ResourceVersion: "1",
			Annotations: map[string]string{PolicyManagedAnnotation: "true", AdmissionPrefixAnnotation: "cvk"},
		},
		Data: map[string]string{PolicyDataKey: data},
	}
	if _, err := inspectAdminPolicy(cm); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("inspectAdminPolicy() error = %v, want unknown-field rejection", err)
	}
}

func TestParseAdminPolicyRequiresIndependentLedgerBinding(t *testing.T) {
	cfg := validAdminPolicyConfig()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{UID: "policy-uid", ResourceVersion: "1", Annotations: map[string]string{
			PolicyManagedAnnotation: "true", AdmissionPrefixAnnotation: "cvk",
		}},
		Data: map[string]string{PolicyDataKey: string(data)},
	}
	if _, err := ParseAdminPolicy(cm); err == nil || !strings.Contains(err.Error(), "ledger UID") {
		t.Fatalf("ParseAdminPolicy() error = %v", err)
	}
}

func validAdminPolicyConfig() AdminPolicyConfig {
	return AdminPolicyConfig{
		Version:                      PolicyVersion,
		FleetSelector:                metav1.LabelSelector{MatchLabels: map[string]string{"topology.cisco.vk/managed": "true"}},
		RequiredTopologyKeys:         []string{"topology.cisco.vk/site", "topology.cisco.vk/redundancy-group"},
		ProjectedTopologyKeys:        []string{"topology.cisco.vk/site", "topology.cisco.vk/redundancy-group"},
		GlobalMaxConcurrentTransfers: 3,
		GlobalMaxUnavailable:         3,
		DomainMaxConcurrentTransfers: map[string]int{"topology.cisco.vk/site": 2, "topology.cisco.vk/redundancy-group": 1},
		DomainMaxUnavailable:         map[string]int{"topology.cisco.vk/site": 2, "topology.cisco.vk/redundancy-group": 1},
		HealthFreshnessSeconds:       120,
		MaxCampaignTargets:           100,
		MaxActiveReservations:        256,
		MaxLedgerBytes:               256 * 1024,
		LedgerName:                   "cvk-rollout-ledger",
	}
}
