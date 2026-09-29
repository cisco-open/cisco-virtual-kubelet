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

package workloaddrain

import (
	"slices"
	"testing"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestEligibilityHashCoversSnapshotAndIgnoresProgress(t *testing.T) {
	pod := &opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "edge", UID: "pod-uid",
		Controller: opsv1alpha1.UpgradeDrainObjectReference{
			APIVersion: "apps/v1", Kind: "ReplicaSet", Namespace: "apps", Name: "edge-rs", UID: "rs-uid", Generation: 2,
		},
		PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{
			UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
				APIVersion: "policy/v1", Kind: "PodDisruptionBudget", Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 3,
			},
			ObservedGeneration: 3, DisruptionsAllowed: 1, CurrentHealthy: 2, DesiredHealthy: 1, ExpectedPods: 2,
		}},
		TerminationGracePeriodSeconds: 30, Phase: opsv1alpha1.UpgradeDrainPodSelected,
	}
	digest, err := EligibilityHash(pod)
	if err != nil {
		t.Fatal(err)
	}
	pod.EligibilityHash = digest
	if err := VerifyEligibilityHash(pod); err != nil {
		t.Fatal(err)
	}
	progress := pod.DeepCopy()
	progress.Phase = opsv1alpha1.UpgradeDrainPodProtected
	progress.DeletionObservedInventoryRevision = 17
	if got, err := EligibilityHash(progress); err != nil || got != digest {
		t.Fatalf("dynamic progress or inventory baseline changed eligibility hash: got=%q err=%v", got, err)
	}
	changed := pod.DeepCopy()
	changed.PDBs = slices.Clone(changed.PDBs)
	changed.PDBs[0].DisruptionsAllowed++
	if err := VerifyEligibilityHash(changed); err == nil {
		t.Fatal("changed frozen PDB evidence retained eligibility authority")
	}
}
