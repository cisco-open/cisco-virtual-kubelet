// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetadataWorkerAuthorization must corroborate the actual Pod-bound caller and
// compatible installed worker/manager/enrollment/settlement authority. It may
// only observe. Nil disables claims. Product eligibility belongs to the caller's
// existing journal-start path; this callback cannot replace native preflight.
type MetadataWorkerAuthorization func(context.Context, *cvk.NetworkController) error

// MetadataDispatchPermit exists only after this process won the original
// Prepared -> Submitting journal CAS and verified readback. Copies share one
// consumption bit. It cannot be reconstructed from persisted Submitting state.
type MetadataDispatchPermit struct{ state *metadataDispatchState }
type metadataDispatchState struct {
	reader                                  client.Reader
	controller                              *cvk.NetworkController
	installation                            BootstrapFenceInstallation
	metadataPolicy                          string
	pod                                     ops.ConfigurationReservationReference
	authorize                               MetadataWorkerAuthorization
	metadataProtection, bootstrapProtection *AdmissionProtection
	used                                    atomic.Bool
	checked                                 atomic.Bool
}

// ClaimMetadataOnce is called by the existing resource journal start boundary
// after product admission. It claims the SAME operation slot, never creates a
// reservation, starts a native request or claims a CiscoDevice physical lease.
// Only a successful, independently verified CAS yields a process-local permit.
// Failed/uncertain writes and already-Submitting work never yield one or replay.
func ClaimMetadataOnce(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, pod ops.ConfigurationReservationReference, installation BootstrapFenceInstallation, metadataPolicy string, authorize MetadataWorkerAuthorization, now time.Time) (*MetadataDispatchPermit, error) {
	reject := errors.New("metadata claim requires original ready custody and compatible Pod-bound authority")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || pod.Name == "" || pod.UID == "" || authorize == nil || now.IsZero() {
		return nil, reject
	}
	state := &metadataDispatchState{reader: reader, controller: expected.DeepCopy(), pod: pod, installation: installation, metadataPolicy: metadataPolicy, authorize: authorize}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	if err := state.validate(ctx, "Prepared", observedNow()); err != nil {
		return nil, err
	}
	updated := expected.DeepCopy()
	op := updated.Status.Configuration.Operation
	op.Phase = "Submitting"
	at := metav1.NewTime(observedNow().UTC().Truncate(time.Second))
	op.SubmittedAt = &at
	if err := writer.Status().Update(ctx, updated); err != nil {
		return nil, err
	}
	var stored cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(updated), &stored); err != nil {
		return nil, err
	}
	if stored.UID != updated.UID || stored.Generation != updated.Generation || !reflect.DeepEqual(stored.Spec, updated.Spec) || !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
		return nil, reject
	}
	state.controller = stored.DeepCopy()
	if err := state.validate(ctx, "Submitting", observedNow()); err != nil {
		return nil, err
	}
	return &MetadataDispatchPermit{state: state}, nil
}

// Check consumes the process-local opportunity even on refusal. Call it after
// all product-native preflight, immediately before the one native submission.
// Native guards must still reobserve current authority around their own reads.
func (p *MetadataDispatchPermit) Check(ctx context.Context, now time.Time) error {
	if p == nil || p.state == nil || p.state.used.Swap(true) || now.IsZero() {
		return errors.New("metadata dispatch opportunity is unavailable or spent")
	}
	if err := p.state.validate(ctx, "Submitting", now); err != nil {
		return err
	}
	p.state.checked.Store(true)
	return nil
}

// Reobserve corroborates the winning epoch around product-native preflight.
// It is available only after successful one-shot Check and cannot issue a new
// claim or submission opportunity. The native write retains its own one-use guard.
func (p *MetadataDispatchPermit) Reobserve(ctx context.Context, now time.Time) error {
	if p == nil || p.state == nil || !p.state.checked.Load() || now.IsZero() {
		return errors.New("metadata dispatch has no checked original process authority")
	}
	return p.state.validate(ctx, "Submitting", now)
}

// ReadMetadataClaimPreparation reobserves original ready custody before product
// planning. It performs no CAS, produces no permit and grants no native dispatch.
func ReadMetadataClaimPreparation(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, pod ops.ConfigurationReservationReference, installation BootstrapFenceInstallation, metadataPolicy string, authorize MetadataWorkerAuthorization, now time.Time) error {
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || pod.Name == "" || pod.UID == "" || authorize == nil || now.IsZero() {
		return errors.New("metadata preparation authority is unavailable")
	}
	state := &metadataDispatchState{reader: reader, controller: expected.DeepCopy(), pod: pod, installation: installation, metadataPolicy: metadataPolicy, authorize: authorize}
	return state.validate(ctx, "Prepared", now)
}

func (s *metadataDispatchState) validate(ctx context.Context, phase string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reject := errors.New("original metadata claim source, custody, actor or protection changed")
	var observed *cvk.NetworkController
	check := func() error {
		var n cvk.NetworkController
		if err := s.reader.Get(ctx, client.ObjectKeyFromObject(s.controller), &n); err != nil {
			return err
		}
		if n.UID != s.controller.UID || (phase == "Prepared" && n.ResourceVersion != s.controller.ResourceVersion) || n.Generation != s.controller.Generation || n.Spec.Paused || n.DeletionTimestamp != nil || !reflect.DeepEqual(n.Spec, s.controller.Spec) || !reflect.DeepEqual(n.Status.Configuration, s.controller.Status.Configuration) || n.Status.Configuration == nil {
			return reject
		}
		h := n.Status.Configuration.MetadataHandoff
		op := n.Status.Configuration.Operation
		if h == nil || op == nil || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || h.ReadyAt == nil || h.ReadyAt.IsZero() || h.ReadyAt.After(now) || h.CancelledAt != nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil || h.ReleasedAt != nil || h.WorkerPod != s.pod || h.LeaseNamespace != s.installation.LeaseNamespace || op.Phase != phase || op.ControllerGeneration != n.Generation {
			return reject
		}
		original := op.DeepCopy()
		if phase == "Submitting" {
			if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || op.SubmittedAt.Before(h.ReadyAt) {
				return reject
			}
			original.Phase = "Prepared"
			original.SubmittedAt = nil
		}
		if !reflect.DeepEqual(*original, h.Operation) {
			return reject
		}
		if err := readBoundConfigurationSource(ctx, s.reader, &n, n.Namespace, ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, op.ConfigGeneration); err != nil {
			return err
		}
		actor, err := readBootstrapControllerWorker(ctx, s.reader, &n, now)
		if err != nil {
			return err
		}
		if actor.Pod != s.pod || actor.Username != h.WorkerUsername {
			return reject
		}
		normalized := n.DeepCopy()
		normalized.Status.Configuration.Operation = h.Operation.DeepCopy()
		serials := append([]string(nil), h.PhysicalIdentities...)
		slices.Sort(serials)
		binding, err := bindMetadataScopeWorker(normalized, actor, serials)
		if err != nil {
			return err
		}
		if binding.BindingDigest != h.BindingDigest {
			return reject
		}
		if _, err := ReadMetadataFenceCohort(ctx, s.reader, &n); err != nil {
			return err
		}
		mp, err := ReadMetadataHandoffProtection(ctx, s.reader, s.metadataPolicy, s.installation.ManagerUsername, s.installation.LeaseNamespace)
		if err != nil {
			return err
		}
		bp, err := ReadBootstrapFenceProtection(ctx, s.reader, s.installation.PolicyName, s.installation.ManagerUsername, s.installation.LeaseNamespace)
		if err != nil {
			return err
		}
		if s.metadataProtection == nil {
			s.metadataProtection = mp
			s.bootstrapProtection = bp
		} else if !reflect.DeepEqual(mp, s.metadataProtection) || !reflect.DeepEqual(bp, s.bootstrapProtection) {
			return reject
		}
		observed = n.DeepCopy()
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if err := s.authorize(ctx, observed.DeepCopy()); err != nil {
		return err
	}
	return check()
}
