// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The controller holds no device credentials and cannot acquire or release the
// device lease. The XE worker remains responsible for renewal and recovery.
type handoffBridge struct {
	a   *adapter
	key kclient.ObjectKey
	uid string
}
type handoffReconciler struct{ a *adapter }

func (r *handoffReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var h ops.CatalystCenterSWIMHandoff
	if err := r.a.statusReader.Get(ctx, req.NamespacedName, &h); err != nil {
		return ctrl.Result{}, kclient.IgnoreNotFound(err)
	}
	if h.Namespace != r.a.key.Namespace || h.Spec.Source.ControllerName != r.a.key.Name || h.Spec.Source.ControllerUID != string(r.a.uid) {
		return ctrl.Result{}, nil
	}
	b := &handoffBridge{a: r.a, key: req.NamespacedName, uid: string(h.UID)}
	if h.Status.Phase == "PreparationCancelled" {
		return ctrl.Result{}, nil
	}
	intent := handoffIntent(&h)
	if up, _, bindErr := b.bound(ctx, intent); bindErr == nil && controllerhandoff.PreparationCancellationSettled(up, &h) {
		if err := b.Hold(ctx, intent); err != nil {
			return ctrl.Result{}, err
		}
		// Readiness is read-only, but require its exact task to have ended before
		// publishing the terminal dispatch fence. Warnings do not become passes.
		body, err := r.a.client.GetTask(ctx, h.Status.ReadinessTask)
		if err != nil {
			return ctrl.Result{}, err
		}
		var task struct {
			Response struct {
				ID    string `json:"id"`
				Start int64  `json:"startTime"`
				End   int64  `json:"endTime"`
			} `json:"response"`
		}
		if json.Unmarshal(body, &task) != nil || task.Response.ID != h.Status.ReadinessTask || task.Response.Start < h.Status.ReadinessNotBefore.UnixMilli() || task.Response.End < task.Response.Start || task.Response.End > time.Now().UnixMilli() {
			return ctrl.Result{}, errors.New("cancellation requires a completed bound readiness task")
		}
		// ResourceVersion CAS races safely with every dispatch claim. The
		// cancellation control is terminal and cannot authorize later work.
		h.Status.Phase = "PreparationCancelled"
		return ctrl.Result{}, r.a.statusWriter.Status().Update(ctx, &h)
	}
	e, err := newSWIMExecutor(intent.Execution, b, b, b, r.a.client)
	if err == nil {
		err = e.Step(ctx, intent)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}
func handoffIntent(h *ops.CatalystCenterSWIMHandoff) swimIntent {
	s := h.Spec
	policySHA := ""
	if s.Source.Preparation != nil {
		policySHA = s.Source.Preparation.SHA256
	}
	return swimIntent{StandardReloadProfile: s.Source.StandardReloadProfile, PreparationPolicySHA256: policySHA, OperationUID: string(h.UID), Execution: softwarelifecycle.UpgradeExecution{Method: softwarelifecycle.UpgradeCatalystCenter, ControllerNamespace: h.Namespace, ControllerName: s.Source.ControllerName, ControllerUID: s.Source.ControllerUID}, DeviceNamespace: h.Namespace, DeviceName: s.DeviceName, DeviceUID: s.DeviceUID, Serial: s.Serial, ControllerDeviceID: s.Source.DeviceID, ImageID: s.Source.ImageID, TargetVersion: s.TargetVersion, APIContract: swimModernContract, ControllerImageVersion: s.Source.ImageVersion, TransferFallbackAddress: s.Source.TransferFallbackAddress}
}
func (b *handoffBridge) object(ctx context.Context) (*ops.CatalystCenterSWIMHandoff, error) {
	var h ops.CatalystCenterSWIMHandoff
	if err := b.a.statusReader.Get(ctx, b.key, &h); err != nil {
		return nil, err
	}
	if string(h.UID) != b.uid || h.Spec.Source.ControllerUID != string(b.a.uid) || h.Spec.Source.ControllerName != b.a.key.Name || h.Namespace != b.a.key.Namespace || h.Spec.ControllerGeneration != b.a.generation {
		return nil, errors.New("SWIM controller/handoff incarnation changed")
	}
	return &h, nil
}
func timeValue(t *metav1.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}
func timePointer(t time.Time) *metav1.Time {
	if t.IsZero() {
		return nil
	}
	v := metav1.NewTime(t)
	return &v
}
func recordFor(h *ops.CatalystCenterSWIMHandoff) swimRecord {
	s := h.Status
	return swimRecord{InventorySyncs: s.InventorySyncs, ReadinessHistory: s.ReadinessHistory, Revision: h.ResourceVersion, Intent: handoffIntent(h), Phase: swimPhase(s.Phase), DistributionClaim: s.DistributionClaim, DistributionTask: s.DistributionTask, ActivationClaim: s.ActivationClaim, ActivationTask: s.ActivationTask, ReadinessClaim: s.ReadinessClaim, ReadinessTask: s.ReadinessTask, ReadinessFor: swimPhase(s.ReadinessFor), DistributionNotBefore: timeValue(s.DistributionNotBefore), ActivationNotBefore: timeValue(s.ActivationNotBefore), ReadinessNotBefore: timeValue(s.ReadinessNotBefore)}
}
func (b *handoffBridge) Load(ctx context.Context, uid string) (swimRecord, error) {
	h, err := b.object(ctx)
	if err != nil {
		return swimRecord{}, err
	}
	if uid != b.uid {
		return swimRecord{}, errors.New("SWIM UID mismatch")
	}
	if h.Status.Phase == "" {
		return swimRecord{}, errSWIMRecordNotFound
	}
	return recordFor(h), nil
}
func (b *handoffBridge) Create(ctx context.Context, r swimRecord) error {
	h, err := b.object(ctx)
	if err != nil {
		return err
	}
	if h.Status.Phase != "" || r.Phase != swimPending || r.Intent != handoffIntent(h) {
		return errors.New("SWIM journal already initialized or mismatched")
	}
	h.Status.Phase = string(swimPending)
	return b.a.statusWriter.Status().Update(ctx, h)
}
func (b *handoffBridge) Replace(ctx context.Context, before, after swimRecord) error {
	h, err := b.object(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(recordFor(h), before) || before.Intent != after.Intent {
		return errors.New("SWIM journal compare-and-swap conflict")
	}
	h.Status = ops.CatalystCenterSWIMHandoffStatus{InventorySyncs: after.InventorySyncs, ReadinessHistory: after.ReadinessHistory, Phase: string(after.Phase), DistributionClaim: after.DistributionClaim, DistributionTask: after.DistributionTask, ActivationClaim: after.ActivationClaim, ActivationTask: after.ActivationTask, ReadinessClaim: after.ReadinessClaim, ReadinessTask: after.ReadinessTask, ReadinessFor: string(after.ReadinessFor), DistributionNotBefore: timePointer(after.DistributionNotBefore), ActivationNotBefore: timePointer(after.ActivationNotBefore), ReadinessNotBefore: timePointer(after.ReadinessNotBefore)}
	return b.a.statusWriter.Status().Update(ctx, h)
}

// Read uncached objects at each boundary. A journal is evidence, never authority.
func (b *handoffBridge) bound(ctx context.Context, i swimIntent) (*ops.IOSXESoftwareUpgrade, *cvk.CiscoDevice, error) {
	h, err := b.object(ctx)
	if err != nil {
		return nil, nil, err
	}
	if handoffIntent(h) != i {
		return nil, nil, errors.New("SWIM intent changed")
	}
	var up ops.IOSXESoftwareUpgrade
	if err = b.a.statusReader.Get(ctx, kclient.ObjectKey{Namespace: h.Namespace, Name: h.Spec.UpgradeName}, &up); err != nil {
		return nil, nil, err
	}
	g := up.Status.ControllerHandoff
	if string(up.UID) != h.Spec.UpgradeUID || up.Spec.DeviceRef.Name != h.Spec.DeviceName || !reflect.DeepEqual(up.Spec.ImageSource.CatalystCenter, &h.Spec.Source) || up.Spec.TargetVersion != h.Spec.TargetVersion || !controllerhandoff.KnownExecution(&up) || up.Annotations[managedprotocol.AnnotationManaged] != "true" || up.Annotations[managedprotocol.AnnotationWorkerUsername] != h.Spec.DeviceWorkerUsername || g == nil || g.Name != h.Name || g.UID != string(h.UID) || g.WorkerPodUID == "" || (g.WorkerPodUID != up.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] && g.WorkerPodUID != up.Annotations[managedprotocol.AnnotationAppWorkerPodUID]) {
		return nil, nil, errors.New("SWIM parent/worker binding mismatch")
	}
	var d cvk.CiscoDevice
	if err = b.a.statusReader.Get(ctx, kclient.ObjectKey{Namespace: h.Namespace, Name: h.Spec.DeviceName}, &d); err != nil {
		return nil, nil, err
	}
	if string(d.UID) != h.Spec.DeviceUID || d.Spec.PhysicalIdentity != h.Spec.Serial || d.Spec.Driver != cvk.DeviceDriverXE {
		return nil, nil, errors.New("SWIM physical device binding mismatch")
	}
	return &up, &d, nil
}
func (b *handoffBridge) Hold(ctx context.Context, i swimIntent) error {
	up, d, err := b.bound(ctx, i)
	if err != nil {
		return err
	}
	s := d.Status.MaintenanceSession
	if s == nil || s.Operation.Namespace != up.Namespace || s.Operation.Name != up.Name || s.Operation.UID != string(up.UID) || s.DeviceUID != string(d.UID) || (s.Phase != cvk.DeviceMaintenanceSessionActive && s.Phase != cvk.DeviceMaintenanceSessionAcknowledged) || s.AcknowledgedAt == nil || (s.Purpose != "" && s.Purpose != cvk.DeviceMaintenancePurposeSoftwareMutation) {
		return errors.New("SWIM requires the parent maintenance acknowledgement")
	}
	if s.Lease.Name != engine.LeaseName(devicecoordination.DeviceKey(d.Namespace, d.Name), devicecoordination.MutationLeaseFamily) || s.Lease.Holder != "software-upgrade/"+string(up.UID) {
		return errors.New("SWIM canonical lease binding mismatch")
	}
	var lease coordination.Lease
	if err = b.a.statusReader.Get(ctx, kclient.ObjectKey{Namespace: s.Lease.Namespace, Name: s.Lease.Name}, &lease); err != nil {
		return err
	}
	if string(lease.UID) != s.Lease.UID || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != s.Lease.Holder || lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil || !lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second).After(time.Now()) {
		return errors.New("SWIM parent lease is not current")
	}
	if up.Status.ManagerAdmission == nil || up.Status.ManagerAdmission.NodeUID != s.NodeUID {
		return errors.New("SWIM maintenance node binding mismatch")
	}
	return nil
}
func (b *handoffBridge) Claim(ctx context.Context, i swimIntent, p swimPhase) (string, error) {
	up, _, err := b.bound(ctx, i)
	if err != nil {
		return "", err
	}
	token := up.Status.ControllerHandoff.Token
	return token, b.validateDispatch(ctx, i, p, token, false)
}
func (b *handoffBridge) Check(ctx context.Context, i swimIntent, p swimPhase, token string) error {
	return b.validateDispatch(ctx, i, p, token, true)
}

func (b *handoffBridge) validateDispatch(ctx context.Context, i swimIntent, p swimPhase, token string, inspectInventory bool) error {
	if err := b.Hold(ctx, i); err != nil {
		return err
	}
	up, d, err := b.bound(ctx, i)
	if err != nil {
		return err
	}
	nc, err := b.a.currentController(ctx)
	if err != nil {
		return err
	}
	h, err := b.object(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Worker == nil || h.Spec.ControllerUsername != "system:serviceaccount:"+nc.Namespace+":"+nc.Status.Worker.Name || !h.DeletionTimestamp.IsZero() || !up.DeletionTimestamp.IsZero() || !d.DeletionTimestamp.IsZero() {
		return errors.New("SWIM controller/parent is unavailable")
	}
	physicalIdentity, err := topologyidentity.CanonicalPhysicalIdentity(d.Spec.PhysicalIdentity)
	if err != nil {
		return err
	}
	a, g, c := up.Status.ManagerAdmission, up.Status.ControllerHandoff, up.Status.ManagerControl
	if a == nil || a.State != ops.UpgradeManagerAdmissionGranted || !ops.ManagedUpgradeProtocolMatches(up) || a.DeviceUID != string(d.UID) || a.PhysicalIdentity != physicalIdentity || a.LeafUID != string(up.UID) || a.ControlRevision == nil || g.PolicyEpoch != a.PolicyEpoch || g.ControlRevision != *a.ControlRevision || c == nil || c.Revision != g.ControlRevision || c.Pause || c.Cancel || token == "" || token != g.Token || !g.ExpiresAt.After(time.Now()) {
		return errors.New("SWIM dispatch grant is absent, stale, or revoked")
	}
	if w := up.Spec.MaintenanceWindow; w != nil && ((w.NotBefore != nil && time.Now().Before(w.NotBefore.Time)) || (w.NotAfter != nil && !time.Now().Before(w.NotAfter.Time))) {
		return errors.New("SWIM maintenance window is closed")
	}
	if up.Spec.RequireNetworkEvidence && (a.NetworkEvidenceNotAfter == nil || !a.NetworkEvidenceNotAfter.After(time.Now())) {
		return errors.New("SWIM network evidence expired")
	}
	stage := "Readiness"
	if p == swimDistributionClaimed {
		stage = string(ops.UpgradeManagedMutationStaging)
	} else if p == swimActivationClaimed {
		stage = string(ops.UpgradeManagedMutationPrimaryActivation)
	} else if p != swimReadinessClaimed && p != swimInventorySyncClaimed {
		return errors.New("invalid SWIM dispatch stage")
	}
	if g.Stage != stage {
		return errors.New("SWIM is waiting for device-worker stage grant")
	}
	if stage != "Readiness" {
		found := false
		for _, claim := range up.Status.ManagedMutationClaims {
			if string(claim.Stage) == stage && claim.PolicyEpoch == g.PolicyEpoch && claim.ControlRevision >= 0 && claim.ControlRevision <= g.ControlRevision && claim.ReservationID == a.ReservationID {
				found = true
			}
		}
		if !found {
			return errors.New("SWIM parent mutation claim is absent")
		}
	}
	if !inspectInventory {
		return nil
	}
	inventory, err := b.a.client.ListDevices(ctx)
	if err != nil {
		return err
	}
	device, err := resolveSWIMTarget(d, inventory)
	if err != nil {
		return err
	}
	if device.ID != i.ControllerDeviceID {
		return errors.New("SWIM inventory UUID changed")
	}
	images, err := b.a.client.ListImages(ctx)
	if err != nil {
		return err
	}
	img, err := resolveSWIMImage(i.ImageID, images)
	if err != nil {
		return err
	}
	if img.Version != i.ControllerImageVersion || !swimVersionMatches(img.Version, i.TargetVersion) {
		return errors.New("SWIM inventory image version changed")
	}
	details, err := b.a.client.GetDeviceImageDetails(ctx, i.ControllerDeviceID)
	if err != nil {
		return err
	}
	if err = validateImageProduct(img, device, details); err != nil {
		return err
	}
	// The qualified device readiness endpoint evaluates its selected golden
	// SYSTEM image. Never apply those results to a different requested image.
	golden := 0
	for _, image := range details.GoldenImages {
		if image.ImageType != "SYSTEM" {
			continue
		}
		golden++
		if image.ID != i.ImageID || image.Version != i.ControllerImageVersion {
			return errors.New("SWIM readiness golden image differs from the requested target")
		}
	}
	if golden != 1 {
		return errors.New("SWIM requires exactly one pinned golden SYSTEM image")
	}

	// Inventory calls can be slow: repeat the grant after them, before POST.
	fresh, _, err := b.bound(ctx, i)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh.Status.ManagerAdmission, a) || !reflect.DeepEqual(fresh.Status.ManagerControl, c) || fresh.Status.ControllerHandoff.Token != token || fresh.Status.ControllerHandoff.Stage != stage || !fresh.Status.ControllerHandoff.ExpiresAt.After(time.Now()) || !fresh.DeletionTimestamp.IsZero() {
		return errors.New("SWIM grant changed during validation")
	}
	return b.Hold(ctx, i)
}
func (b *handoffBridge) Release(ctx context.Context, i swimIntent) error {
	// Only the parent XE worker releases its lease after journal success.
	_, err := b.Verify(ctx, i)
	return err
}
func (b *handoffBridge) Verify(ctx context.Context, i swimIntent) (swimVerification, error) {
	up, d, err := b.bound(ctx, i)
	if err != nil {
		return swimVerification{}, err
	}
	g := up.Status.ControllerHandoff
	if g.VerifiedAt == nil || !swimVersionMatches(g.VerifiedVersion, i.TargetVersion) || !swimVersionMatches(g.VerifiedVersion, i.ControllerImageVersion) {
		return swimVerification{}, errors.New("SWIM awaiting native committed-image verification")
	}
	return swimVerification{DeviceUID: string(d.UID), Serial: d.Spec.PhysicalIdentity, RunningVersion: g.VerifiedVersion, ObservedAt: g.VerifiedAt.Time}, nil
}
func swimVersionMatches(actual, target string) bool {
	return target != "" && (actual == target || strings.HasPrefix(actual, target+"."))
}
