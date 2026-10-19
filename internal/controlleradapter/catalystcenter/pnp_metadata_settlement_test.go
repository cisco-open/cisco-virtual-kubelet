// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	admission "k8s.io/api/admissionregistration/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataSettlementFault struct {
	ctrlclient.Client
	mode                       string
	updates, deletes, statuses int
	fired                      bool
	editKey                    ctrlclient.ObjectKey
}

func (s *metadataSettlementFault) editController(ctx context.Context) error {
	if s.fired {
		return nil
	}
	var n cvk.NetworkController
	if err := s.Client.Get(ctx, s.editKey, &n); err != nil {
		return err
	}
	n.Spec.Paused = !n.Spec.Paused
	n.Generation++
	if err := s.Client.Update(ctx, &n); err != nil {
		return err
	}
	s.fired = true
	return nil
}

func (s *metadataSettlementFault) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := s.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if l, ok := obj.(*coordination.Lease); ok && (s.mode == "settle-replacement" || s.mode == "settle-late-replacement" && s.updates > 0) {
		l.UID = "replacement"
	}
	return nil
}
func (s *metadataSettlementFault) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
	if _, ok := obj.(*coordination.Lease); !ok {
		return errors.New("settlement updated unexpected object")
	}
	s.updates++
	if err := s.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if s.mode == "settle-lost-marker" && !s.fired {
		s.fired = true
		return errors.New("marker acknowledgement lost")
	}
	return nil
}
func (s *metadataSettlementFault) Delete(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.DeleteOption) error {
	if _, ok := obj.(*coordination.Lease); !ok {
		return errors.New("settlement deleted unexpected object")
	}
	s.deletes++
	options := &ctrlclient.DeleteOptions{}
	for _, opt := range opts {
		opt.ApplyToDelete(options)
	}
	if options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil || *options.Preconditions.UID != obj.GetUID() || *options.Preconditions.ResourceVersion != obj.GetResourceVersion() {
		return errors.New("settlement omitted original UID/RV delete preconditions")
	}
	if err := s.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if s.mode == "settle-edit-delete" && !s.fired {
		if err := s.editController(ctx); err != nil {
			return err
		}
	}
	if s.mode == "settle-lost-delete" && !s.fired {
		s.fired = true
		return errors.New("delete acknowledgement lost")
	}
	return nil
}
func (s *metadataSettlementFault) Status() ctrlclient.SubResourceWriter {
	return metadataSettlementStatus{s.Client.Status(), s}
}

type metadataSettlementStatus struct {
	ctrlclient.SubResourceWriter
	store *metadataSettlementFault
}

func (w metadataSettlementStatus) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	w.store.statuses++
	n := obj.(*cvk.NetworkController)
	h := n.Status.Configuration.MetadataHandoff
	intent := h != nil && h.SettlementDigest != "" && h.ReleasedAt == nil
	ack := h != nil && h.ReleasedAt != nil
	if w.store.mode == "settle-pruned-intent" && intent {
		n = n.DeepCopy()
		n.Status.Configuration.MetadataHandoff.SettlementDigest = ""
		obj = n
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if !w.store.fired && ((intent && w.store.mode == "settle-edit-marker") || (ack && w.store.mode == "settle-edit-release")) {
		if err := w.store.editController(ctx); err != nil {
			return err
		}
	}
	if !w.store.fired && (intent && w.store.mode == "settle-lost-intent" || ack && w.store.mode == "settle-lost-ack" || h == nil && w.store.mode == "settle-lost-retire") {
		w.store.fired = true
		return errors.New("settlement status acknowledgement lost")
	}
	return nil
}

func qualifyPnPMetadataSettlement(t *testing.T, ctx context.Context, a *adapter, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, manager ctrlclient.Client, installation contract.BootstrapFenceInstallation, policy, mode string) {
	t.Helper()
	if mode == "settle-edit-before" {
		nc.Spec.Paused = true
		nc.Generation++
		if err := manager.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := manager.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), nc); err != nil {
			t.Fatal(err)
		}
	}
	before := nc.DeepCopy()
	refs := append([]cvk.NetworkControllerMetadataFenceReference(nil), nc.Status.Configuration.MetadataHandoff.Fences...)
	compatible := contract.MetadataSettlementRuntimeCheck(func(context.Context, *cvk.NetworkController) error { return nil })
	// A changed native policy must be repaired before even certified no-dispatch
	// can release physical metadata custody. No native cleanup is inferred.
	if mode == "policy-drift" {
		if _, err := contract.SettleMetadataHandoff(ctx, a.statusReader, manager, nc, installation, policy, pnpMetadataSettlementState, compatible, time.Now()); err == nil {
			t.Fatal("altered policy settled original custody")
		}
		var p admission.ValidatingAdmissionPolicy
		if err := manager.Get(ctx, ctrlclient.ObjectKey{Name: policy}, &p); err != nil {
			t.Fatal(err)
		}
		fail := admission.Fail
		p.Spec.FailurePolicy = &fail
		if err := manager.Update(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "settle-foreign-history" {
		altered := nc.DeepCopy()
		altered.Status.Configuration.Owners[0].ConfigName = "foreign"
		if err := manager.Status().Update(ctx, altered); err != nil {
			t.Fatal(err)
		}
		if _, err := contract.SettleMetadataHandoff(ctx, a.statusReader, manager, altered, installation, policy, pnpMetadataSettlementState, compatible, time.Now()); err == nil {
			t.Fatal("foreign owner history released custody")
		}
		return
	}
	if mode == "settle-worker-gone" {
		var pod core.Pod
		if err := manager.Get(ctx, ctrlclient.ObjectKey{Namespace: nc.Namespace, Name: nc.Status.Configuration.MetadataHandoff.WorkerPod.Name}, &pod); err != nil {
			t.Fatal(err)
		}
		if err := manager.Delete(ctx, &pod); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "settle-missing-source" {
		if err := manager.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	fault := &metadataSettlementFault{Client: manager, mode: mode, editKey: ctrlclient.ObjectKeyFromObject(nc)}
	var reader ctrlclient.Reader = a.statusReader
	if mode == "settle-replacement" || mode == "settle-late-replacement" {
		reader = fault
	}
	authorize := compatible
	if mode == "settle-no-runtime" {
		authorize = nil
	}
	released, err := contract.SettleMetadataHandoff(ctx, reader, fault, nc, installation, policy, pnpMetadataSettlementState, authorize, time.Now())
	expectedError := mode == "settle-edit-marker" || mode == "settle-edit-delete" || mode == "settle-edit-release" || strings.HasPrefix(mode, "settle-lost-") && mode != "settle-lost-retire" || mode == "settle-replacement" || mode == "settle-no-runtime" || mode == "settle-pruned-intent" || mode == "settle-late-replacement"
	if (err != nil) != expectedError {
		t.Fatal("incorrect original settlement result", mode, err)
	}
	if mode == "settle-late-replacement" {
		if fault.deletes != 0 {
			t.Fatal("raced replacement reached deletion")
		}
		var current cvk.NetworkController
		if err := manager.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &current); err != nil {
			t.Fatal(err)
		}
		if current.Status.Configuration.MetadataHandoff.SettlementDigest == "" || current.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
			t.Fatal("replacement race lost retained intent")
		}
		return
	}
	if mode == "settle-replacement" || mode == "settle-no-runtime" {
		if fault.updates != 0 || fault.deletes != 0 || fault.statuses != 0 {
			t.Fatal("foreign/unconfigured settlement wrote authority")
		}
		return
	}
	if err != nil {
		var fresh cvk.NetworkController
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &fresh); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(mode, "settle-edit-") && mode != "settle-edit-before" && (!fault.fired || fresh.Generation <= before.Generation || !fresh.Spec.Paused) {
			t.Fatal("qualification edit did not commit; a rejected edit cannot prove epoch recovery")
		}
		if fresh.Status.Configuration.MetadataHandoff.SettlementDigest == "" && mode != "settle-pruned-intent" {
			t.Fatal("uncertain settlement lost original intent")
		}
		fault.mode = "valid"
		released, err = contract.SettleMetadataHandoff(ctx, a.statusReader, fault, &fresh, installation, policy, pnpMetadataSettlementState, compatible, time.Now())
		if err != nil {
			t.Fatal("original settlement response recovery", err)
		}
	}
	if released == nil || released.Status.Configuration.MetadataHandoff.ReleasedAt == nil || released.Status.Configuration.MetadataHandoff.SettlementDigest == "" || !reflect.DeepEqual(before.Status.Configuration.Operation, released.Status.Configuration.Operation) || !reflect.DeepEqual(before.Status.Configuration.ActionApplications, released.Status.Configuration.ActionApplications) {
		t.Fatal("settlement changed original outcome/ledger")
	}
	for _, ref := range refs {
		var l coordination.Lease
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: installation.LeaseNamespace, Name: ref.Name}, &l); !apierrors.IsNotFound(err) {
			t.Fatal("original fence not independently absent", err)
		}
	}
	updates, deletes, statuses := fault.updates, fault.deletes, fault.statuses
	if _, err := contract.SettleMetadataHandoff(ctx, a.statusReader, fault, released, installation, policy, pnpMetadataSettlementState, compatible, time.Now()); err != nil || fault.updates != updates || fault.deletes != deletes || fault.statuses != statuses {
		t.Fatal("released original settlement replayed", err)
	}
	retired, err := contract.RetireMetadataHandoff(ctx, a.statusReader, fault, released, installation, policy, pnpMetadataSettlementState, compatible, time.Now())
	if mode == "settle-lost-retire" {
		if err == nil {
			t.Fatal("lost retirement acknowledgement ignored")
		}
		retired = &cvk.NetworkController{}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), retired); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal("settled handoff retirement", err)
	}
	if retired.Status.Configuration.MetadataHandoff != nil || !reflect.DeepEqual(before.Status.Configuration.ActionApplications, retired.Status.Configuration.ActionApplications) {
		t.Fatal("retirement lost retained outcome")
	}
	if op := retired.Status.Configuration.Operation; op != nil {
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal("existing worker finish after manager retirement", err)
		}
	}
	if journalState(t, a).Operation != nil {
		t.Fatal("settled original operation did not finish")
	}
	// The original creator/tombstone prevents replay even after custody retires.
	if before.Status.Configuration.Operation != nil {
		if _, err := a.planOwnedPnPRemoval(ctx, c, source, before.Status.Configuration.Operation.TargetID); err == nil {
			t.Fatal("settled original native outcome replayed")
		}
	}
}
