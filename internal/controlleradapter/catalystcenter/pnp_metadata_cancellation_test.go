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
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPnPMetadataCancellation(t *testing.T) {
	for _, mode := range []string{"cancel-valid", "cancel-paused", "cancel-source-gone", "cancel-worker-gone", "cancel-lost-ack", "cancel-conflict", "cancel-claim-race", "cancel-unknown", "cancel-no-runtime", "cancel-no-projection", "cancel-foreign-history", "cancel-replacement"} {
		t.Run(mode, func(t *testing.T) { exercisePnPMetadataPreparedSubmission(t, mode, nil, nil) })
	}
}

type metadataCancellationFault struct {
	ctrlclient.Client
	worker       ctrlclient.Client
	reader       ctrlclient.Reader
	installation contract.BootstrapFenceInstallation
	policy, mode string
	fired        bool
	writes       int
}

func (s *metadataCancellationFault) Status() ctrlclient.SubResourceWriter {
	return metadataCancellationWriter{s.Client.Status(), s}
}

type metadataCancellationWriter struct {
	ctrlclient.SubResourceWriter
	s *metadataCancellationFault
}

func (w metadataCancellationWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	n := obj.(*cvk.NetworkController)
	if n.Status.Configuration.MetadataHandoff != nil && n.Status.Configuration.MetadataHandoff.CancelledAt != nil {
		w.s.writes++
		if !w.s.fired && w.s.mode == "cancel-conflict" {
			w.s.fired = true
			return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, n.Name, errors.New("concurrent write"))
		}
		if !w.s.fired && w.s.mode == "cancel-claim-race" {
			w.s.fired = true
			var current cvk.NetworkController
			if err := w.s.reader.Get(ctx, ctrlclient.ObjectKeyFromObject(n), &current); err != nil {
				return err
			}
			if _, err := contract.ClaimMetadataOnce(ctx, w.s.reader, w.s.worker, &current, current.Status.Configuration.MetadataHandoff.WorkerPod, w.s.installation, w.s.policy, func(context.Context, *cvk.NetworkController) error { return nil }, time.Now()); err != nil {
				return err
			}
		}
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if !w.s.fired && w.s.mode == "cancel-lost-ack" {
		w.s.fired = true
		return errors.New("cancellation acknowledgement lost")
	}
	return nil
}

func qualifyPnPMetadataCancellation(t *testing.T, ctx context.Context, a *adapter, c *configapi.NetworkControllerConfig, manager, worker ctrlclient.Client, installation contract.BootstrapFenceInstallation, policy, mode string, real bool) {
	t.Helper()
	n, err := a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "cancel-paused":
		n.Spec.Paused = true
		n.Generation++
		if err := manager.Update(ctx, n); err != nil {
			t.Fatal(err)
		}
	case "cancel-source-gone":
		if err := manager.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "cancel-worker-gone":
		var p core.Pod
		if err := manager.Get(ctx, ctrlclient.ObjectKey{Namespace: n.Namespace, Name: a.workerPodName}, &p); err != nil {
			t.Fatal(err)
		}
		if err := manager.Delete(ctx, &p); err != nil {
			t.Fatal(err)
		}
	case "cancel-foreign-history":
		n.Status.Configuration.Owners[0].ConfigUID = "foreign"
		if err := manager.Status().Update(ctx, n); err != nil {
			t.Fatal(err)
		}
	case "cancel-unknown":
		if _, err := contract.ClaimMetadataOnce(ctx, a.statusReader, worker, n, n.Status.Configuration.MetadataHandoff.WorkerPod, installation, policy, func(context.Context, *cvk.NetworkController) error { return nil }, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := manager.Get(ctx, ctrlclient.ObjectKeyFromObject(n), n); err != nil {
			t.Fatal(err)
		}
		n.Status.Configuration.Operation.Phase = "Unknown"
		if err := worker.Status().Update(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Get(ctx, ctrlclient.ObjectKeyFromObject(n), n); err != nil {
		t.Fatal(err)
	}
	before := n.Status.Configuration.DeepCopy()
	if real && mode == "cancel-valid" {
		attempted := n.DeepCopy()
		attempted.Status.Configuration.Operation = nil
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		attempted.Status.Configuration.MetadataHandoff.CancelledAt = &at
		if err := worker.Status().Update(ctx, attempted, ctrlclient.DryRunAll); (!apierrors.IsForbidden(err) && !apierrors.IsInvalid(err)) || !strings.Contains(err.Error(), policy) {
			t.Fatal("worker manufactured manager cancellation", err)
		}
		attempted = n.DeepCopy()
		attempted.Status.Configuration.MetadataHandoff.CancelledAt = &at
		if err := manager.Status().Update(ctx, attempted, ctrlclient.DryRunAll); (!apierrors.IsForbidden(err) && !apierrors.IsInvalid(err)) || !strings.Contains(err.Error(), policy) {
			t.Fatal("non-atomic cancellation marker accepted", err)
		}
	}
	store := &metadataCancellationFault{Client: manager, reader: a.statusReader, worker: worker, installation: installation, policy: policy, mode: mode}
	var reader ctrlclient.Reader = a.statusReader
	if mode == "cancel-replacement" {
		reader = &metadataSettlementFault{Client: manager, mode: "settle-replacement"}
	}
	authorize := contract.MetadataSettlementRuntimeCheck(func(context.Context, *cvk.NetworkController) error { return nil })
	project := contract.MetadataSettlementProjection(pnpMetadataCancellationState)
	if mode == "cancel-no-runtime" {
		authorize = nil
	}
	if mode == "cancel-no-projection" {
		project = nil
	}
	cancelled, err := contract.CancelMetadataHandoff(ctx, reader, store, n, installation, policy, project, authorize, time.Now())
	denied := mode == "cancel-unknown" || mode == "cancel-no-runtime" || mode == "cancel-no-projection" || mode == "cancel-foreign-history" || mode == "cancel-replacement" || mode == "cancel-claim-race"
	if (err != nil) != (denied || mode == "cancel-conflict" || mode == "cancel-lost-ack") {
		t.Fatal("incorrect prepared cancellation", mode, err)
	}
	if err != nil {
		if err := manager.Get(ctx, ctrlclient.ObjectKeyFromObject(n), n); err != nil {
			t.Fatal(err)
		}
		if denied {
			if n.Status.Configuration.MetadataHandoff.CancelledAt != nil || n.Status.Configuration.Operation == nil {
				t.Fatal("claimed/foreign/unsupported work became cancelled")
			}
			if mode == "cancel-claim-race" && n.Status.Configuration.Operation.Phase != "Submitting" {
				t.Fatal("winning worker claim lost")
			}
			if mode != "cancel-claim-race" && !reflect.DeepEqual(before, n.Status.Configuration) {
				t.Fatal("denied cancellation changed journal")
			}
			return
		}
		if mode == "cancel-lost-ack" {
			cancelled = n.DeepCopy()
			if _, err := contract.CancelMetadataHandoff(ctx, a.statusReader, store, n, installation, policy, project, authorize, time.Now()); err == nil {
				t.Fatal("lost acknowledgement replayed cancellation")
			}
		} else {
			store.mode = "valid"
			cancelled, err = contract.CancelMetadataHandoff(ctx, a.statusReader, store, n, installation, policy, project, authorize, time.Now())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if cancelled.Status.Configuration.Operation != nil || cancelled.Status.Configuration.MetadataHandoff.CancelledAt == nil || cancelled.Status.Configuration.MetadataHandoff.RefusedSubmissionAt != nil || !reflect.DeepEqual(before.ActionApplications, cancelled.Status.Configuration.ActionApplications) {
		t.Fatal("cancellation invented native proof or lost ownership")
	}
	if real && mode == "cancel-valid" {
		changed := cancelled.DeepCopy()
		changed.Status.Configuration.MetadataHandoff.CancelledAt = nil
		err := manager.Status().Update(ctx, changed, ctrlclient.DryRunAll)
		if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
			t.Fatal("cancellation evidence erased", err)
		}
	}
	if _, err := contract.ReadMetadataFenceCohort(ctx, a.statusReader, cancelled); err != nil {
		t.Fatal("cancellation released custody before settlement", err)
	}
	settled, err := contract.SettleMetadataHandoff(ctx, a.statusReader, manager, cancelled, installation, policy, pnpMetadataSettlementState, authorize, time.Now())
	if err != nil {
		t.Fatal("cancelled custody settlement", err)
	}
	retired, err := contract.RetireMetadataHandoff(ctx, a.statusReader, manager, settled, installation, policy, pnpMetadataSettlementState, authorize, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status.Configuration.Operation != nil || retired.Status.Configuration.MetadataHandoff != nil || !reflect.DeepEqual(before.ActionApplications, retired.Status.Configuration.ActionApplications) {
		t.Fatal("cancellation retirement changed creator ledger")
	}
}
