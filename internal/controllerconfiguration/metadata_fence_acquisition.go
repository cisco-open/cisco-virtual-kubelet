// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"slices"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AcquireMetadataFenceCohort creates retained canonical bootstrap Leases and
// appends each observed original UID to the existing Prepared metadata handoff.
// Callers supply an uncached global reader, independently validated complete
// product cohort and compatible installed runtime check. Nil keeps it disabled.
// It never starts an operation, contacts a device or releases retained evidence.
// Errors preserve both fences and status. A lost acknowledgement requires a fresh
// controller snapshot; the helper never retries a stale status CAS or deletes a
// fence. Only the exact next pending binding may be recovered before its first UID
// publication. Once recorded, that UID is mandatory, including after a restart.
func AcquireMetadataFenceCohort(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, installation BootstrapFenceInstallation, metadataPolicyName string, serials []string, authorize MetadataHandoffRuntimeCheck, now time.Time) (*cvk.NetworkController, error) {
	reject := errors.New("metadata fence acquisition lacks original custody or compatible protected runtime")
	if reader == nil || writer == nil || expected == nil || authorize == nil || now.IsZero() {
		return nil, reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	current := expected.DeepCopy()
	serials = append([]string(nil), serials...)
	slices.Sort(serials)
	metadataProtection, err := ReadMetadataHandoffProtection(ctx, reader, metadataPolicyName, installation.ManagerUsername, installation.LeaseNamespace)
	if err != nil {
		return nil, err
	}
	bootstrapProtection, err := ReadBootstrapFenceProtection(ctx, reader, installation.PolicyName, installation.ManagerUsername, installation.LeaseNamespace)
	if err != nil {
		return nil, err
	}
	worker, err := readBootstrapControllerWorker(ctx, reader, current, observedNow())
	if err != nil {
		return nil, err
	}
	candidate := func(n *cvk.NetworkController, serial string) (*coordination.Lease, error) {
		h := n.Status.Configuration.MetadataHandoff
		op := h.Operation
		identity := BootstrapIdentityObservation{Controller: ops.ConfigurationReservationReference{Name: n.Name, UID: string(n.UID)}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, ControllerGeneration: n.Generation, ConfigGeneration: op.ConfigGeneration, OperationID: op.ID, OperationDigest: h.BindingDigest, NativeTargetID: op.TargetID, PhysicalIdentity: serial, SourceUID: op.SourceUID, SourceVersion: op.SourceVersion}
		l, err := bootstrapFenceCandidate(installation.LeaseNamespace, n.Namespace, &identity, worker, installation, bootstrapProtection)
		if err != nil {
			return nil, err
		}
		l.Labels[metadataFenceLabel] = MetadataFenceProtocol
		return l, nil
	}
	matches := func(actual, desired *coordination.Lease) bool {
		return actual != nil && actual.UID != "" && actual.ResourceVersion != "" && actual.DeletionTimestamp == nil && len(actual.OwnerReferences) == 0 && actual.Name == desired.Name && actual.Namespace == desired.Namespace && reflect.DeepEqual(actual.Labels, desired.Labels) && reflect.DeepEqual(actual.Annotations, desired.Annotations) && slices.Equal(actual.Finalizers, desired.Finalizers) && reflect.DeepEqual(actual.Spec, desired.Spec)
	}
	// checks deliberately reuse the same global census/source/actor validators;
	// the private exception recognizes only this operation's retained originals.
	check := func(n *cvk.NetworkController) (string, error) {
		if n.Status.Configuration == nil || n.Status.Configuration.Operation == nil {
			return "", reject
		}
		h := n.Status.Configuration.MetadataHandoff
		if h == nil || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || h.LeaseNamespace != installation.LeaseNamespace || h.CancelledAt != nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil || h.ReleasedAt != nil || !reflect.DeepEqual(h.Operation, *n.Status.Configuration.Operation) || h.WorkerPod != worker.Pod || h.WorkerUsername != worker.Username {
			return "", reject
		}
		if h.ReadyAt != nil && (h.ReadyAt.IsZero() || h.ReadyAt.After(observedNow())) {
			return "", reject
		}
		cohort := append([]string(nil), h.PhysicalIdentities...)
		slices.Sort(cohort)
		if !slices.Equal(cohort, serials) || len(serials) == 0 || len(h.Fences) > len(serials) || h.ReadyAt != nil && len(h.Fences) != len(serials) {
			return "", reject
		}
		refs := map[string]cvk.NetworkControllerMetadataFenceReference{}
		uids := map[string]bool{}
		for _, ref := range h.Fences {
			name, e := BootstrapFenceName(ref.PhysicalIdentity)
			if e != nil || name != ref.Name || ref.UID == "" || uids[ref.UID] || !slices.Contains(serials, ref.PhysicalIdentity) {
				return "", reject
			}
			if _, exists := refs[ref.PhysicalIdentity]; exists {
				return "", reject
			}
			refs[ref.PhysicalIdentity] = ref
			uids[ref.UID] = true
		}
		pending := ""
		for _, serial := range serials {
			if _, found := refs[serial]; !found {
				pending = serial
				break
			}
		}
		fenceCheck := func(serial string) error {
			actual, e := readBootstrapFenceObject(ctx, reader, installation.LeaseNamespace, serial)
			if e != nil {
				return e
			}
			ref, recorded := refs[serial]
			if actual == nil {
				if recorded {
					return reject
				}
				return nil
			}
			if !recorded && serial != pending {
				return reject
			}
			desired, e := candidate(n, serial)
			if e != nil {
				return e
			}
			if !matches(actual, desired) || recorded && string(actual.UID) != ref.UID {
				return reject
			}
			return nil
		}
		observation, e := readMetadataScopeWorker(ctx, reader, n, installation.LeaseNamespace, serials, observedNow(), fenceCheck)
		if e != nil {
			return "", e
		}
		if observation.BindingDigest != h.BindingDigest || observation.Pod != worker.Pod || observation.Username != worker.Username || observation.ContractHash != worker.ContractHash || observation.Deployment != worker.Deployment || observation.ReplicaSet != worker.ReplicaSet {
			return "", reject
		}
		mp, e := ReadMetadataHandoffProtection(ctx, reader, metadataPolicyName, installation.ManagerUsername, installation.LeaseNamespace)
		if e != nil {
			return "", e
		}
		bp, e := ReadBootstrapFenceProtection(ctx, reader, installation.PolicyName, installation.ManagerUsername, installation.LeaseNamespace)
		if e != nil {
			return "", e
		}
		if !reflect.DeepEqual(mp, metadataProtection) || !reflect.DeepEqual(bp, bootstrapProtection) {
			return "", reject
		}
		argument := *observation
		argument.PhysicalIdentities = append([]string(nil), observation.PhysicalIdentities...)
		if e := authorize(ctx, n.DeepCopy(), argument, *mp); e != nil {
			return "", e
		}
		// Runtime checks receive copies and may only observe. Repeat authoritative
		// facts afterward so denied or drifting deployment/source state cannot grant.
		after, e := readMetadataScopeWorker(ctx, reader, n, installation.LeaseNamespace, serials, observedNow(), fenceCheck)
		if e != nil {
			return "", e
		}
		if !reflect.DeepEqual(after, observation) {
			return "", reject
		}
		mp, e = ReadMetadataHandoffProtection(ctx, reader, metadataPolicyName, installation.ManagerUsername, installation.LeaseNamespace)
		if e != nil {
			return "", e
		}
		bp, e = ReadBootstrapFenceProtection(ctx, reader, installation.PolicyName, installation.ManagerUsername, installation.LeaseNamespace)
		if e != nil {
			return "", e
		}
		if !reflect.DeepEqual(mp, metadataProtection) || !reflect.DeepEqual(bp, bootstrapProtection) {
			return "", reject
		}
		return pending, nil
	}
	for range len(serials) + 1 {
		pending, err := check(current)
		if err != nil {
			return nil, err
		}
		if pending == "" {
			if _, err := ReadMetadataFenceCohort(ctx, reader, current); err != nil {
				return nil, err
			}
			if current.Status.Configuration.MetadataHandoff.ReadyAt == nil {
				updated := current.DeepCopy()
				at := metav1.NewTime(observedNow().UTC().Truncate(time.Second))
				updated.Status.Configuration.MetadataHandoff.ReadyAt = &at
				if err := writer.Status().Update(ctx, updated); err != nil {
					return nil, err
				}
				var stored cvk.NetworkController
				if err := reader.Get(ctx, client.ObjectKeyFromObject(updated), &stored); err != nil {
					return nil, err
				}
				if stored.UID != current.UID || stored.Generation != current.Generation || !reflect.DeepEqual(stored.Spec, current.Spec) || !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
					return nil, reject
				}
				current = stored.DeepCopy()
				if _, err := check(current); err != nil {
					return nil, err
				}
			}
			return current.DeepCopy(), nil
		}
		desired, err := candidate(current, pending)
		if err != nil {
			return nil, err
		}
		var retained coordination.Lease
		err = reader.Get(ctx, client.ObjectKeyFromObject(desired), &retained)
		if apierrors.IsNotFound(err) {
			retained = *desired.DeepCopy()
			if err = writer.Create(ctx, &retained); err != nil && !apierrors.IsAlreadyExists(err) {
				return nil, err
			}
			// Independently read the created/raced object; a successful Create response
			// cannot stand in for retained shape and original-incarnation corroboration.
			acknowledgedUID := retained.UID
			if err = reader.Get(ctx, client.ObjectKeyFromObject(desired), &retained); err != nil {
				return nil, err
			}
			if acknowledgedUID != "" && retained.UID != acknowledgedUID {
				return nil, reject
			}
		} else if err != nil {
			return nil, err
		}
		if !matches(&retained, desired) {
			return nil, reject
		}
		updated := current.DeepCopy()
		updated.Status.Configuration.MetadataHandoff.Fences = append(updated.Status.Configuration.MetadataHandoff.Fences, cvk.NetworkControllerMetadataFenceReference{PhysicalIdentity: pending, Name: retained.Name, UID: string(retained.UID)})
		if err := writer.Status().Update(ctx, updated); err != nil {
			return nil, err
		}
		var stored cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(updated), &stored); err != nil {
			return nil, err
		}
		if stored.UID != current.UID || stored.Generation != current.Generation || !reflect.DeepEqual(stored.Spec, current.Spec) || !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
			return nil, reject
		}
		current = stored.DeepCopy()
	}
	return nil, reject
}
