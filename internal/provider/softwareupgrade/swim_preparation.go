// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package softwareupgrade

import (
	"context"
	"crypto/sha256"

	"encoding/hex"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"

	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/google/uuid"

	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"time"
)

// Only this device worker can execute cleanup, under the existing canonical
// maintenance reservation. The controller worker receives completion evidence.
func (r *Reconciler) runSWIMPreparation(ctx context.Context, up *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) (reconcile.Result, error) {
	wait := reconcile.Result{RequeueAfter: 15 * time.Second}
	source := up.Spec.ImageSource.CatalystCenter
	if source == nil || source.Preparation == nil || up.Status.ControllerHandoff == nil {
		return wait, errors.New("preparation requires its pinned policy and handoff")
	}
	stage := h.Status.ReadinessFor
	if stage != "ReadyToDistribute" && stage != "ReadyToActivate" {
		return wait, errors.New("invalid device preparation stage")
	}
	p, err := controllerhandoff.ReadPreparationPolicy(ctx, r.apiReader(), up.Namespace, source.Preparation)
	if err != nil {
		return wait, err
	}
	p, err = p.ForStage(stage)
	if err != nil {
		return wait, err
	}
	access, ok := r.Lifecycle.(lifecycle.RetiredArchiveAccess)
	if !ok {
		return wait, errors.New("retired archive capability unavailable")
	}
	observer, ok := r.Lifecycle.(lifecycle.SWIMPreparationObserver)
	if !ok {
		return wait, errors.New("native SWIM preparation observer unsupported")
	}
	index := -1
	for n, receipt := range up.Status.ControllerHandoff.Preparation {
		if receipt.Stage == stage {
			index = n
			if receipt.Phase == "Complete" {
				return wait, nil
			}
			if receipt.Phase == "OutcomeUnknown" {
				return wait, errors.New("cleanup outcome unknown; mutation fence retained")
			}
		}
	}
	gc, err := r.gnoiClient(ctx)
	if err != nil {
		return wait, err
	}
	running, err := verifyOS(ctx, gc)
	if err != nil {
		return wait, err
	}
	if running.IndividualSupervisorInstall || running.ActivationFailMessage != "" || running.Version != up.Status.PreviousVersion {
		return wait, errors.New("cleanup requires the unchanged committed baseline")
	}
	request := lifecycle.SWIMFlashRequest{RunningVersion: running.Version}
	if stage == "ReadyToActivate" {
		if h.Status.DistributionTask == "" || h.Status.DistributionClaim == "" || h.Status.DistributionNotBefore == nil || h.Status.Phase != "Preparing" {
			return wait, errors.New("staged target requires completed controller distribution")
		}
		request.StagedTargetVersion = source.ImageVersion
		request.DistributionStartedAt = h.Status.DistributionNotBefore.Time
	}
	snapshot, err := observer.ObserveSWIMFlash(ctx, request)
	if err != nil {
		return wait, err
	}
	if index < 0 {
		if len(up.Status.ControllerHandoff.Preparation) >= 2 {
			return wait, errors.New("preparation budget exhausted")
		}
		plan, receipts, err := r.planSWIMPreparation(ctx, up, p, snapshot, running.Version)
		if err != nil {
			return wait, err
		}
		entry := ops.SWIMDevicePreparation{ID: uuid.NewString(), Stage: stage, Phase: "Planned", PolicySHA256: source.Preparation.SHA256, PlanSHA256: plan.InputSHA256, FreeBytesBefore: int64(snapshot.FreeBytes), ObservedAt: metav1.NewTime(r.now()), Files: []ops.SWIMPreparationFile{}}
		for _, f := range plan.Candidates {
			entry.Files = append(entry.Files, ops.SWIMPreparationFile{Path: f.Path, SHA256: f.SHA256, Size: int64(f.Size), ReceiptUID: receipts[f.Path]})
		}
		cur := up.DeepCopy()
		cur.Status.ControllerHandoff.Preparation = append(cur.Status.ControllerHandoff.Preparation, entry)
		return wait, r.Client.Status().Update(ctx, cur)
	}
	entry := up.Status.ControllerHandoff.Preparation[index]
	if entry.PolicySHA256 != source.Preparation.SHA256 {
		return wait, errors.New("cleanup policy changed")
	}
	for n, file := range entry.Files {
		if file.RemovedAt != nil {
			continue
		}
		if file.ClaimedAt != nil {
			// A durable claim is never replayed. Absence can settle a lost response;
			// presence is ambiguous and must retain the fence for investigation.
			cur := up.DeepCopy()
			e := &cur.Status.ControllerHandoff.Preparation[index]
			if _, present := snapshot.Files[file.Path]; present {
				if r.now().Sub(file.ClaimedAt.Time) < 90*time.Second {
					return wait, nil
				}
				e.Phase = "OutcomeUnknown"
			} else {
				now := metav1.NewTime(r.now())
				e.Files[n].RemovedAt = &now
			}
			return wait, r.Client.Status().Update(ctx, cur)
		}
		if err := r.validateSWIMCandidate(ctx, up, snapshot, running.Version, file); err != nil {
			return wait, err
		}
		// Hash the exact bytes over the existing authenticated device transport,
		// bounded by the immutable receipt size and a total wall-clock deadline.
		getctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = verifySWIMArchive(getctx, access, file)
		cancel()
		if err != nil {
			return wait, err
		}
		// Protect boot manifest contents as well as the native boot variable.
		var boot string
		bctx, bcancel := context.WithTimeout(ctx, 30*time.Second)
		boot, err = access.ObserveSWIMBootManifest(bctx)
		bcancel()
		if err != nil || len(boot) == 0 || strings.Contains(boot, strings.TrimPrefix(file.Path, "flash:")) || bootReferencesRetiredVersion(boot, file.Path) {
			return wait, errors.New("boot manifest protection could not be verified")
		}
		// Repeat native and Kubernetes evidence after the potentially long transfer.
		snapshot, err = observer.ObserveSWIMFlash(ctx, request)
		if err != nil {
			return wait, err
		}
		if err = r.validateSWIMCandidate(ctx, up, snapshot, running.Version, file); err != nil {
			return wait, err
		}
		fresh := &ops.IOSXESoftwareUpgrade{}
		if err = r.apiReader().Get(ctx, client.ObjectKeyFromObject(up), fresh); err != nil {
			return wait, err
		}
		if fresh.UID != up.UID || !reflect.DeepEqual(fresh.Status.ControllerHandoff, up.Status.ControllerHandoff) {
			return wait, errors.New("preparation journal changed")
		}
		if _, err = controllerhandoff.ReadPreparationPolicy(ctx, r.apiReader(), up.Namespace, source.Preparation); err != nil {
			return wait, err
		}
		owned, _, err := r.ensureMutationLease(ctx, fresh, r.now())
		if err != nil || !owned {
			return wait, err
		}
		ready, err := r.prepareManagedMutation(ctx, fresh, fresh.DeepCopy(), ops.UpgradeManagedMutationStaging, r.now(), false)
		if err != nil || !ready {
			return wait, err
		}
		now := metav1.NewTime(r.now())
		fresh.Status.ControllerHandoff.Preparation[index].Files[n].ClaimedAt = &now
		fresh.Status.ControllerHandoff.Preparation[index].Phase = "Removing"
		if err = r.Client.Status().Update(ctx, fresh); err != nil {
			return wait, err
		}
		// Re-read admission after the write-ahead marker. A revoke or worker
		// replacement here leaves the claim held and never replays removal.
		claimed := &ops.IOSXESoftwareUpgrade{}
		if err = r.apiReader().Get(ctx, client.ObjectKeyFromObject(fresh), claimed); err != nil {
			return wait, err
		}
		if claimed.UID != fresh.UID || !reflect.DeepEqual(claimed.Status.ControllerHandoff, fresh.Status.ControllerHandoff) || !claimed.DeletionTimestamp.IsZero() {
			return wait, errors.New("cleanup claim changed before dispatch")
		}
		ready, err = r.prepareManagedMutation(ctx, claimed, claimed.DeepCopy(), ops.UpgradeManagedMutationStaging, r.now(), false)
		if err != nil || !ready {
			return wait, err
		}
		// Admission/CAS succeeded. At most one request; the next reconciliation
		// confirms absence and space from native inventory, even after a lost reply.
		removectx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return wait, access.RemoveRetiredArchive(removectx, file.Path)
	}
	if snapshot.FreeBytes < p.RequiredFreeBytes+p.HeadroomBytes {
		return wait, errors.New("cleanup completed but observed free space is insufficient")
	}
	cur := up.DeepCopy()
	done := &cur.Status.ControllerHandoff.Preparation[index]
	now := metav1.NewTime(r.now())
	done.Phase = "Complete"
	done.FreeBytesAfter = int64(snapshot.FreeBytes)
	done.CompletedAt = &now
	return wait, r.Client.Status().Update(ctx, cur)
}

func (r *Reconciler) planSWIMPreparation(ctx context.Context, up *ops.IOSXESoftwareUpgrade, p controllerhandoff.PreparationPolicy, s lifecycle.SWIMFlashSnapshot, running string) (lifecycle.PreparationPlan, map[string]string, error) {
	source := up.Spec.ImageSource.CatalystCenter
	if r.DrainDevicePodLister == nil {
		return lifecycle.PreparationPlan{}, nil, errors.New("strict application observer unavailable")
	}
	apps, err := r.DrainDevicePodLister(ctx)
	if err != nil {
		return lifecycle.PreparationPlan{}, nil, err
	}
	if len(apps) != 0 {
		return lifecycle.PreparationPlan{}, nil, errors.New("native applications still present")
	}
	o := lifecycle.PreparationObservation{DeviceUID: r.DeviceUID, PhysicalIdentity: up.Status.ManagerAdmission.PhysicalIdentity, ControllerUID: source.ControllerUID, TargetImageID: source.ImageID, PolicyUID: source.Preparation.UID, PolicyGeneration: 1, ObservedAt: r.now(), Revision: s.EvidenceHash, Volume: "flash:", FreeBytes: s.FreeBytes, RequiredFreeBytes: p.RequiredFreeBytes, Quiescent: true, Complete: map[string]bool{"filesystem": true, "install": true, "boot": true, "target": true, "rollback": true, "preparations": true, "applications": true}}
	// The committed package set and boot manifest remain the rollback baseline.
	o.Files = []lifecycle.PreparationFile{{Path: "flash:packages.conf", Size: s.Files["flash:packages.conf"], Kind: "BootManifest", References: []string{"boot", "committed"}, RetainedRollbackImage: running}}
	candidates, err := r.swimRetiredCandidates(ctx, up)
	if err != nil {
		return lifecycle.PreparationPlan{}, nil, err
	}
	receipts := map[string]string{}
	for _, f := range candidates {
		if r.validateSWIMCandidate(ctx, up, s, running, f) != nil {
			continue
		}
		o.Files = append(o.Files, lifecycle.PreparationFile{Path: f.Path, Size: uint64(f.Size), SHA256: f.SHA256, Kind: "ImageArchive"})
		receipts[f.Path] = f.ReceiptUID
	}
	maxFiles, maxBytes := p.MaxFiles, p.MaxBytes
	for _, e := range up.Status.ControllerHandoff.Preparation {
		for _, f := range e.Files {
			maxFiles--
			if f.Size < 1 || uint64(f.Size) > maxBytes {
				return lifecycle.PreparationPlan{}, nil, errors.New("invalid cumulative cleanup budget")
			}
			maxBytes -= uint64(f.Size)
		}
	}
	// With exhausted deletion budget, successful no-op preparation is still
	// allowed if space is already sufficient; no candidates may be selected.
	if maxFiles <= 0 || maxBytes == 0 {
		if s.FreeBytes < p.RequiredFreeBytes+p.HeadroomBytes {
			return lifecycle.PreparationPlan{}, nil, errors.New("cumulative cleanup budget exhausted")
		}
		o.Files = o.Files[:1]
		maxFiles = 1
		maxBytes = 1
	}
	plan := lifecycle.PlanPreparation(lifecycle.PreparationPlanInput{Observation: o, Limits: lifecycle.PreparationLimits{MaxFiles: maxFiles, MaxBytes: maxBytes, HeadroomBytes: p.HeadroomBytes, PreserveRollbackImages: 1, MaxAgeSeconds: 120}}, r.now())
	if !plan.SpaceSatisfied || len(plan.Blockers) > 0 {
		return plan, nil, fmt.Errorf("automatic preparation blocked: %v", plan.Blockers)
	}
	return plan, receipts, nil
}
func (r *Reconciler) swimRetiredCandidates(ctx context.Context, up *ops.IOSXESoftwareUpgrade) ([]ops.SWIMPreparationFile, error) {
	var list ops.IOSXESoftwareUpgradeList
	if err := r.apiReader().List(ctx, &list, client.InNamespace(up.Namespace)); err != nil {
		return nil, err
	}
	candidates := []ops.SWIMPreparationFile{}
	protected := map[string]bool{}
	seen := map[string]bool{}
	for _, other := range list.Items {
		if other.Spec.DeviceRef.Name != up.Spec.DeviceRef.Name {
			continue
		}
		receipt := other.Status.PreparedReceipt
		if receipt == nil {
			continue
		}
		if receipt.DeviceUID != r.DeviceUID || ValidatePreparedReceipt(receipt) != nil {
			return nil, errors.New("preparation inventory contains an unbound receipt")
		}
		path := "flash:gNOI_iosxe_" + receipt.ValidatedVersion + ".bin"
		if !PreparedReceiptInvalidated(&other) {
			protected[path] = true
			continue
		}
		if seen[path] {
			return nil, errors.New("ambiguous retired archive receipts")
		}
		seen[path] = true
		candidates = append(candidates, ops.SWIMPreparationFile{Path: path, SHA256: strings.TrimPrefix(receipt.SourceDigest, "sha256:"), Size: receipt.SourceSize, ReceiptUID: string(other.UID)})
	}
	result := candidates[:0]
	for _, f := range candidates {
		if !protected[f.Path] {
			result = append(result, f)
		}
	}
	return result, nil
}
func (r *Reconciler) validateSWIMCandidate(ctx context.Context, up *ops.IOSXESoftwareUpgrade, s lifecycle.SWIMFlashSnapshot, running string, f ops.SWIMPreparationFile) error {
	if r.DrainDevicePodLister == nil {
		return errors.New("strict native application inventory unavailable")
	}
	apps, err := r.DrainDevicePodLister(ctx)
	if err != nil {
		return err
	}
	if len(apps) != 0 {
		return errors.New("automatic cleanup requires empty native application inventory")
	}
	version := strings.TrimSuffix(strings.TrimPrefix(f.Path, "flash:gNOI_iosxe_"), ".bin")
	if lifecycle.ValidateTargetVersion(version) != nil || f.Path != "flash:gNOI_iosxe_"+version+".bin" || versionMatches(version, up.Spec.TargetVersion) || versionMatches(version, running) || f.Size <= 0 || s.Files[f.Path] != uint64(f.Size) || strings.Contains(s.NativeReferences, strings.TrimPrefix(f.Path, "flash:")) {
		return errors.New("archive is missing, changed, or protected by native/target references")
	}
	image, inspectErr := r.Lifecycle.Inspect(ctx, version)
	if !errors.Is(inspectErr, lifecycle.ErrTargetNotFound) {
		sourceName := strings.TrimPrefix(f.Path, "flash:")
		exactSource := image.SourcePath == f.Path || image.SourcePath == "/mnt/sd3/user/"+sourceName
		if inspectErr != nil || image.State != lifecycle.InventoryStatePresent || image.Version != version || !exactSource || s.ArchiveOnlyVersions[f.Path] != version {
			return errors.New("retired archive version still has native install references")
		}
	}
	candidates, err := r.swimRetiredCandidates(ctx, up)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.Path == f.Path && candidate.SHA256 == f.SHA256 && candidate.Size == f.Size && candidate.ReceiptUID == f.ReceiptUID {
			return nil
		}
	}
	return errors.New("archive no longer has an exact invalidated preparation receipt")
}

type boundedSWIMWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedSWIMWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("device file exceeds pinned size")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func verifySWIMArchive(ctx context.Context, access lifecycle.RetiredArchiveAccess, f ops.SWIMPreparationFile) error {
	h := sha256.New()
	w := &boundedSWIMWriter{writer: h, remaining: f.Size}
	if err := access.ReadRetiredArchive(ctx, f.Path, f.Size, w); err != nil {
		return err
	}
	if w.remaining != 0 || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errors.New("retired archive content/size changed")
	}
	return nil
}

func bootReferencesRetiredVersion(boot, path string) bool {
	version := strings.TrimSuffix(strings.TrimPrefix(path, "flash:gNOI_iosxe_"), ".bin")
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return true
	}
	return strings.Contains(boot, strings.Join(parts[:3], "."))
}
