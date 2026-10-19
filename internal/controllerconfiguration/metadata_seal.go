// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetadataHandoffRuntimeCheck must verify compatible deployed manager/worker and
// retained-fence settlement behavior. It must perform no writes or native calls.
// Nil keeps new seals disabled; policy presence alone cannot supply this check.
type MetadataHandoffRuntimeCheck func(context.Context, *cvk.NetworkController, MetadataScopeWorkerObservation, AdmissionProtection) error

// EnsureMetadataHandoff seals only a Prepared metadata operation in the existing
// controller status CAS. The caller independently validates its product/cohort.
// No native API, fence acquisition, claim, release or operation start occurs.
func EnsureMetadataHandoff(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, policyName, managerUsername, leaseNamespace string, serials []string, authorize MetadataHandoffRuntimeCheck, now time.Time) (*cvk.NetworkControllerConfigurationMetadataHandoff, error) {
	reject := errors.New("metadata custody requires a current preparation and compatible protected runtime")
	if reader == nil || writer == nil || expected == nil || authorize == nil || now.IsZero() {
		return nil, reject
	}
	expected = expected.DeepCopy()
	serials = append([]string(nil), serials...)
	observation, err := ReadMetadataScopeWorker(ctx, reader, expected, leaseNamespace, serials, now)
	if err != nil {
		return nil, err
	}
	protection, err := ReadMetadataHandoffProtection(ctx, reader, policyName, managerUsername, leaseNamespace)
	if err != nil {
		return nil, err
	}
	desired := &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: observation.BindingDigest, LeaseNamespace: leaseNamespace, WorkerPod: observation.Pod, WorkerUsername: observation.Username, PhysicalIdentities: append([]string(nil), observation.PhysicalIdentities...), Operation: *expected.Status.Configuration.Operation.DeepCopy()}
	if existing := expected.Status.Configuration.MetadataHandoff; existing != nil && !reflect.DeepEqual(existing, desired) {
		return nil, reject
	}
	check := func(current *cvk.NetworkController) error {
		fresh, err := ReadMetadataScopeWorker(ctx, reader, current, leaseNamespace, serials, now)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(fresh, observation) {
			return reject
		}
		p, err := ReadMetadataHandoffProtection(ctx, reader, policyName, managerUsername, leaseNamespace)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(p, protection) {
			return reject
		}
		argument := *fresh
		argument.PhysicalIdentities = append([]string(nil), fresh.PhysicalIdentities...)
		return authorize(ctx, current.DeepCopy(), argument, *p)
	}
	if err := check(expected); err != nil {
		return nil, err
	}
	if expected.Status.Configuration.MetadataHandoff != nil {
		return desired.DeepCopy(), nil
	}
	updated := expected.DeepCopy()
	updated.Status.Configuration.MetadataHandoff = desired.DeepCopy()
	if err := writer.Status().Update(ctx, updated); err != nil {
		// An uncertain acknowledgement may have committed custody. Never erase it or
		// retry with the stale snapshot; the caller must read current API state.
		return nil, err
	}
	var stored cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &stored); err != nil {
		return nil, err
	}
	if stored.UID != expected.UID || stored.Generation != expected.Generation || !reflect.DeepEqual(stored.Spec, expected.Spec) || stored.Status.Configuration == nil || !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
		return nil, reject
	}
	if err := check(&stored); err != nil {
		return nil, err
	}
	return stored.Status.Configuration.MetadataHandoff.DeepCopy(), nil
}
