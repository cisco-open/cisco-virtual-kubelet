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

// Package workloaddrain contains the small, platform-neutral evidence contract
// shared by the rollout manager and per-device worker. It deliberately owns no
// Kubernetes clients or device operations.
package workloaddrain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

const eligibilityDomain = "cisco.vk/workload-drain-eligibility/v1"

type eligibilityEvidence struct {
	Domain                        string                                   `json:"domain"`
	Namespace                     string                                   `json:"namespace"`
	Name                          string                                   `json:"name"`
	UID                           string                                   `json:"uid"`
	Controller                    opsv1alpha1.UpgradeDrainObjectReference  `json:"controller"`
	WorkloadController            *opsv1alpha1.UpgradeDrainObjectReference `json:"workloadController,omitempty"`
	PDBs                          []opsv1alpha1.UpgradeDrainPDBStatus      `json:"pdbs"`
	TerminationGracePeriodSeconds int64                                    `json:"terminationGracePeriodSeconds"`
}

// EligibilityHash returns the canonical digest of every immutable input that
// authorizes eviction and device-side teardown. Dynamic phase and timestamp
// evidence is intentionally excluded.
func EligibilityHash(pod *opsv1alpha1.UpgradeDrainPodStatus) (string, error) {
	if pod == nil {
		return "", fmt.Errorf("drain Pod evidence is nil")
	}
	pdbs := append([]opsv1alpha1.UpgradeDrainPDBStatus(nil), pod.PDBs...)
	sort.Slice(pdbs, func(i, j int) bool {
		if pdbs[i].UID != pdbs[j].UID {
			return pdbs[i].UID < pdbs[j].UID
		}
		if pdbs[i].Namespace != pdbs[j].Namespace {
			return pdbs[i].Namespace < pdbs[j].Namespace
		}
		return pdbs[i].Name < pdbs[j].Name
	})
	evidence := eligibilityEvidence{
		Domain: eligibilityDomain, Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID,
		Controller: pod.Controller, WorkloadController: pod.WorkloadController, PDBs: pdbs,
		TerminationGracePeriodSeconds: pod.TerminationGracePeriodSeconds,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", fmt.Errorf("encode drain Pod eligibility evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// VerifyEligibilityHash rejects a snapshot whose nested evidence no longer
// matches the immutable digest published by the manager.
func VerifyEligibilityHash(pod *opsv1alpha1.UpgradeDrainPodStatus) error {
	actual, err := EligibilityHash(pod)
	if err != nil {
		return err
	}
	if len(pod.EligibilityHash) != len(actual) ||
		subtle.ConstantTimeCompare([]byte(pod.EligibilityHash), []byte(actual)) != 1 {
		return fmt.Errorf("drain Pod eligibility hash does not match its snapshot")
	}
	return nil
}
