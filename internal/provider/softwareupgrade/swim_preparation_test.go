// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package softwareupgrade

import (
	"context"
	"crypto/sha256"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"io"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

type testArchiveAccess struct{ data string }

func (a testArchiveAccess) ReadRetiredArchive(_ context.Context, _ string, _ int64, w io.Writer) error {
	_, e := io.WriteString(w, a.data)
	return e
}
func (testArchiveAccess) RemoveRetiredArchive(context.Context, string) error { return nil }
func (testArchiveAccess) ObserveSWIMBootManifest(context.Context) (string, error) {
	return "packages", nil
}
func TestSWIMArchiveDigestAndSize(t *testing.T) {
	expected := ops.SWIMPreparationFile{Path: "flash:gNOI_iosxe_17.18.02.bin", Size: 3, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("abc")))}
	for _, body := range []string{"abc", "abd", "ab", "abcd"} {
		err := verifySWIMArchive(context.Background(), testArchiveAccess{body}, expected)
		if (err == nil) != (body == "abc") {
			t.Fatalf("unexpected digest result for %q: %v", body, err)
		}
	}
}
func TestSWIMBootProtectsPackageReferences(t *testing.T) {
	path := "flash:gNOI_iosxe_17.18.02.0.4112.1766116039.bin"
	if !bootReferencesRetiredVersion("rpboot cat9k-rpboot.17.18.02.SPA.pkg", path) {
		t.Fatal("boot package reference did not protect its archive")
	}
	if bootReferencesRetiredVersion("rpboot cat9k-rpboot.17.18.04.SPA.pkg", path) {
		t.Fatal("unrelated version protected")
	}
}

func TestSWIMPlannerRetainsReferencesAndCumulativeBudget(t *testing.T) {
	for _, kind := range []string{"eligible", "target", "native-reference", "installed", "live-receipt", "budget", "applications"} {
		t.Run(kind, func(t *testing.T) {
			retired := invalidationTestLeaf(t)
			retired.Status.Phase = ops.UpgradePhasePreparedInvalidated
			retired.Status.PreparedInvalidation = &ops.UpgradePreparedInvalidationStatus{RequestHash: PreparedInvalidationRequestHash(retired.Status.ManagerInvalidation), NativeEvidenceHash: "sha256:" + strings.Repeat("e", 64), ObservedAt: metav1.NewTime(managedTestTime), WorkerRevision: managedTestWorkerRevision, WorkerPodUID: managedTestWorkerPodUID, RunningVersion: "17.18.03", TargetState: "Installed"}
			up := managedTestLeaf("automatic")
			up.Spec.TargetVersion = "17.18.04"
			up.Spec.ImageSource = ops.UpgradeImageSource{CatalystCenter: &ops.CatalystCenterImageSource{ControllerUID: "controller", ImageID: "image", Preparation: &ops.SWIMPreparationPolicyRef{UID: "policy"}}}
			up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{}
			if kind == "live-receipt" {
				retired.Status.Phase = ops.UpgradePhasePrepared
			}
			r := newManagedTestReconciler(t, up, nil, retired)
			r.DrainDevicePodLister = func(context.Context) ([]*core.Pod, error) { return nil, nil }
			r.Lifecycle = &fakeLifecycle{inspectErr: lifecycle.ErrTargetNotFound}
			snapshot := lifecycle.SWIMFlashSnapshot{FreeBytes: 100, Files: map[string]uint64{"flash:packages.conf": 100, "flash:gNOI_iosxe_17.18.02.bin": 1024}, EvidenceHash: "evidence"}
			p := controllerhandoff.PreparationPolicy{MaxFiles: 1, MaxBytes: 2048, RequiredFreeBytes: 1000}
			switch kind {
			case "target":
				up.Spec.TargetVersion = "17.18.02"
			case "native-reference":
				snapshot.NativeReferences = "flash:gNOI_iosxe_17.18.02.bin"
			case "installed":
				r.Lifecycle = &fakeLifecycle{}
			case "budget":
				up.Status.ControllerHandoff.Preparation = []ops.SWIMDevicePreparation{{Files: []ops.SWIMPreparationFile{{Size: 1024}}}}
			case "applications":
				r.DrainDevicePodLister = func(context.Context) ([]*core.Pod, error) { return []*core.Pod{{}}, nil }
			}
			plan, _, err := r.planSWIMPreparation(context.Background(), up, p, snapshot, "17.18.03")
			if kind == "eligible" {
				if err != nil || len(plan.Candidates) != 1 {
					t.Fatalf("eligible receipt rejected: %v %+v", err, plan)
				}
			} else if err == nil {
				t.Fatalf("unsafe %s selected", kind)
			}
		})
	}
}

type swimPreparationBackendStub struct {
	fakeLifecycle
	testArchiveAccess
	snapshot lifecycle.SWIMFlashSnapshot
	removals int
}

func (s *swimPreparationBackendStub) ObserveSWIMFlash(context.Context, lifecycle.SWIMFlashRequest) (lifecycle.SWIMFlashSnapshot, error) {
	return s.snapshot, nil
}
func (s *swimPreparationBackendStub) RemoveRetiredArchive(context.Context, string) error {
	s.removals++
	return nil
}
func TestSWIMCleanupClaimIsNeverReplayedAfterRestart(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			up := managedTestLeaf("cleanup-restart")
			up.Status.Phase = ops.UpgradePhaseStaging
			up.Status.PreviousVersion = "17.15.01a"
			raw := `{"profile":"RetiredCVKImageArchivesV1","requiredFreeBytes":100,"maxFiles":1,"maxBytes":2000}`
			sum := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
			yes := true
			policy := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-policy", Namespace: up.Namespace, UID: "policy-uid", Labels: map[string]string{"ops.cisco.vk/swim-preparation-policy": "true"}}, Immutable: &yes, Data: map[string]string{"policy.json": raw}}
			up.Spec.ImageSource = ops.UpgradeImageSource{CatalystCenter: &ops.CatalystCenterImageSource{Preparation: &ops.SWIMPreparationPolicyRef{Name: policy.Name, UID: string(policy.UID), SHA256: sum}}}
			claimed := metav1.NewTime(managedTestTime.Add(-2 * time.Minute))
			file := ops.SWIMPreparationFile{Path: "flash:gNOI_iosxe_17.18.02.bin", Size: 1024, ClaimedAt: &claimed}
			up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Preparation: []ops.SWIMDevicePreparation{{ID: "receipt", Stage: "ReadyToDistribute", Phase: "Removing", PolicySHA256: sum, Files: []ops.SWIMPreparationFile{file}}}}
			r := newManagedTestReconciler(t, up, nil, policy)
			rig := newRig(t)
			r.GNOI = &staticGNOI{c: rig.client}
			b := &swimPreparationBackendStub{snapshot: lifecycle.SWIMFlashSnapshot{FreeBytes: 5000, Files: map[string]uint64{}}}
			if present {
				b.snapshot.Files[file.Path] = 1024
			}
			r.Lifecycle = b
			h := &ops.CatalystCenterSWIMHandoff{Status: ops.CatalystCenterSWIMHandoffStatus{ReadinessFor: "ReadyToDistribute"}}
			if _, err := r.runSWIMPreparation(context.Background(), up, h); err != nil {
				t.Fatal(err)
			}
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), up); err != nil {
				t.Fatal(err)
			}
			receipt := up.Status.ControllerHandoff.Preparation[0]
			if present {
				if receipt.Phase != "OutcomeUnknown" {
					t.Fatal("present claimed file allowed replay")
				}
			} else if receipt.Files[0].RemovedAt == nil {
				t.Fatal("verified absence not recovered")
			}
			_, _ = r.runSWIMPreparation(context.Background(), up, h)
			if b.removals != 0 {
				t.Fatal("removal replayed after durable claim")
			}
		})
	}
}
