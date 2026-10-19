// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetadataSettlementProjection is product-pure original outcome validation and
// normalization to the exact pre-submission configuration ledger, with handoff
// removed and original Prepared operation restored. It performs no API calls.
type MetadataSettlementProjection func(*cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error)

// MetadataSettlementRuntimeCheck observes compatible installed manager, actor,
// enrollment and settlement authority. Nil disables all settlement writes. A
// live worker is not required: original incarnation facts remain in custody.
type MetadataSettlementRuntimeCheck func(context.Context, *cvk.NetworkController) error

// New settlement intents bind retained terminal evidence, not mutable desired
// spec. They remain recoverable after a later spec edit. Historical v1 markers
// are accepted only when their original current-spec digest still corroborates.
func metadataSettlementDigest(nc *cvk.NetworkController) (string, error) {
	stable, err := metadataTerminalDigest(nc)
	if err != nil {
		return "", err
	}
	retained := nc.Status.Configuration.MetadataHandoff.SettlementDigest
	if retained == "" || retained == stable {
		return stable, nil
	}
	legacy, err := metadataLegacySettlementDigest(nc)
	if err != nil {
		return "", err
	}
	if retained != legacy {
		return "", errors.New("retained metadata settlement intent cannot be corroborated")
	}
	return legacy, nil
}

func metadataTerminalDigest(nc *cvk.NetworkController) (string, error) {
	n := nc.DeepCopy()
	h := n.Status.Configuration.MetadataHandoff
	h.SettlementDigest = ""
	h.ReleasedAt = nil
	raw, err := json.Marshal(struct {
		Protocol, Namespace, Name, UID string
		OriginalGeneration             int64
		Configuration                  *cvk.NetworkControllerConfigurationStatus
	}{"controller-metadata-settlement-v2", n.Namespace, n.Name, string(n.UID), h.Operation.ControllerGeneration, n.Status.Configuration})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func metadataLegacySettlementDigest(nc *cvk.NetworkController) (string, error) {
	n := nc.DeepCopy()
	h := n.Status.Configuration.MetadataHandoff
	h.SettlementDigest = ""
	h.ReleasedAt = nil
	raw, err := json.Marshal(struct {
		Protocol, Namespace, Name, UID string
		Generation                     int64
		Spec                           cvk.NetworkControllerSpec
		Configuration                  *cvk.NetworkControllerConfigurationStatus
	}{"controller-metadata-settlement-v1", n.Namespace, n.Name, string(n.UID), n.Generation, n.Spec, n.Status.Configuration})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type metadataSettlementState struct {
	reader                                  client.Reader
	current                                 *cvk.NetworkController
	installation                            BootstrapFenceInstallation
	metadataPolicy                          string
	normalize                               MetadataSettlementProjection
	authorize                               MetadataSettlementRuntimeCheck
	metadataProtection, bootstrapProtection *AdmissionProtection
	digest                                  string
}

func (s *metadataSettlementState) check(ctx context.Context) error {
	reject := errors.New("metadata terminal outcome, original custody or settlement authority changed")
	if err := ctx.Err(); err != nil {
		return err
	}
	check := func() error {
		var nc cvk.NetworkController
		if err := s.reader.Get(ctx, client.ObjectKeyFromObject(s.current), &nc); err != nil {
			return err
		}
		if nc.UID != s.current.UID || nc.Generation != s.current.Generation || nc.DeletionTimestamp != nil || !reflect.DeepEqual(nc.Spec, s.current.Spec) || !reflect.DeepEqual(nc.Status.Configuration, s.current.Status.Configuration) {
			return reject
		}
		normalized, err := s.normalize(nc.DeepCopy())
		if err != nil {
			return err
		}
		if normalized == nil || normalized.MetadataHandoff != nil || normalized.Operation == nil || !reflect.DeepEqual(*normalized.Operation, nc.Status.Configuration.MetadataHandoff.Operation) {
			return reject
		}
		digest, err := metadataSettlementDigest(&nc)
		if err != nil {
			return err
		}
		if digest != s.digest {
			return reject
		}
		h := nc.Status.Configuration.MetadataHandoff
		if h.SettlementDigest != "" && h.SettlementDigest != digest {
			return reject
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
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if err := s.authorize(ctx, s.current.DeepCopy()); err != nil {
		return err
	}
	return check()
}

func newMetadataSettlement(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, installation BootstrapFenceInstallation, policy string, normalize MetadataSettlementProjection, authorize MetadataSettlementRuntimeCheck, now time.Time) (*metadataSettlementState, error) {
	reject := errors.New("metadata settlement requires original terminal custody and compatible protected runtime")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || expected.Status.Configuration == nil || normalize == nil || authorize == nil || now.IsZero() {
		return nil, reject
	}
	nc := expected.DeepCopy()
	h := nc.Status.Configuration.MetadataHandoff
	if h == nil || h.Operation.ControllerGeneration < 1 || h.Operation.ControllerGeneration > nc.Generation || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || h.LeaseNamespace != installation.LeaseNamespace || h.ReadyAt == nil || h.ReadyAt.IsZero() || h.ReadyAt.After(now) || len(h.Fences) == 0 || len(h.Fences) != len(h.PhysicalIdentities) || len(h.Fences) > 256 {
		return nil, reject
	}
	serials := map[string]bool{}
	uids := map[string]bool{}
	for _, serial := range h.PhysicalIdentities {
		if serials[serial] {
			return nil, reject
		}
		serials[serial] = true
	}
	for _, ref := range h.Fences {
		name, err := BootstrapFenceName(ref.PhysicalIdentity)
		if err != nil || name != ref.Name || !serials[ref.PhysicalIdentity] || ref.UID == "" || uids[ref.UID] {
			return nil, reject
		}
		delete(serials, ref.PhysicalIdentity)
		uids[ref.UID] = true
	}
	if len(serials) != 0 {
		return nil, reject
	}
	digest, err := metadataSettlementDigest(nc)
	if err != nil {
		return nil, err
	}
	s := &metadataSettlementState{reader: reader, current: nc, installation: installation, metadataPolicy: policy, normalize: normalize, authorize: authorize, digest: digest}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *metadataSettlementState) persist(ctx context.Context, writer client.Client, updated *cvk.NetworkController) error {
	if err := writer.Status().Update(ctx, updated); err != nil {
		return err
	}
	var stored cvk.NetworkController
	if err := s.reader.Get(ctx, client.ObjectKeyFromObject(updated), &stored); err != nil {
		return err
	}
	if stored.UID != s.current.UID || stored.Generation != s.current.Generation || !reflect.DeepEqual(stored.Spec, s.current.Spec) || !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
		return errors.New("metadata settlement status persistence could not be verified")
	}
	s.current = stored.DeepCopy()
	return nil
}
func (s *metadataSettlementState) absent(ctx context.Context) error {
	for range 2 {
		for _, ref := range s.current.Status.Configuration.MetadataHandoff.Fences {
			var l coordination.Lease
			err := s.reader.Get(ctx, client.ObjectKey{Namespace: s.installation.LeaseNamespace, Name: ref.Name}, &l)
			if !apierrors.IsNotFound(err) {
				if err != nil {
					return err
				}
				return errors.New("original metadata fence key is not independently absent")
			}
		}
	}
	return nil
}

// bindOriginal corroborates the exact sealed owner ledger against every original
// fence record. Both cancellation and terminal settlement use this same proof.
func (s *metadataSettlementState) bindOriginal(ctx context.Context) error {
	h := s.current.Status.Configuration.MetadataHandoff
	leases, err := ReadMetadataFenceCohort(ctx, s.reader, s.current)
	if err != nil {
		return err
	}
	normalized, err := s.normalize(s.current.DeepCopy())
	if err != nil {
		return err
	}
	original := s.current.DeepCopy()
	original.Status.Configuration = normalized.DeepCopy()
	// This is a pure reconstruction of the already sealed epoch for digest
	// comparison, never a live source/worker observation or dispatch permit.
	original.Generation = h.Operation.ControllerGeneration
	serials := append([]string(nil), h.PhysicalIdentities...)
	slices.Sort(serials)
	var worker *bootstrapControllerWorker
	for _, lease := range leases {
		var record bootstrapFenceRecord
		if json.Unmarshal([]byte(lease.Annotations[bootstrapFenceBindingAnnotation]), &record) != nil {
			return errors.New("original metadata fence record unavailable")
		}
		if record.Installation != s.installation {
			return errors.New("original metadata fence installation changed")
		}
		if worker == nil {
			v := record.Worker
			worker = &v
		} else if !reflect.DeepEqual(*worker, record.Worker) {
			return errors.New("original metadata fence workers disagree")
		}
	}
	binding, err := bindMetadataScopeWorker(original, worker, serials)
	if err != nil {
		return err
	}
	if binding.BindingDigest != h.BindingDigest {
		return errors.New("terminal metadata ledger does not match original sealed ownership")
	}
	return nil
}

// SettleMetadataHandoff first commits terminal outcome/ownership intent, then
// releases and UID/RV-deletes only its original Leases. It never contacts a
// native API. Errors preserve the intent for fresh-snapshot recovery, including
// lost marker/Delete/status acknowledgements. Unknown outcomes and replacements
// remain held. Acknowledgement is published only after independent full absence.
func SettleMetadataHandoff(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, installation BootstrapFenceInstallation, policy string, normalize MetadataSettlementProjection, authorize MetadataSettlementRuntimeCheck, now time.Time) (*cvk.NetworkController, error) {
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	s, err := newMetadataSettlement(ctx, reader, writer, expected, installation, policy, normalize, authorize, now)
	if err != nil {
		return nil, err
	}
	h := s.current.Status.Configuration.MetadataHandoff
	if h.SettlementDigest == "" {
		if err := s.bindOriginal(ctx); err != nil {
			return nil, err
		}
		if err := s.check(ctx); err != nil {
			return nil, err
		}
		updated := s.current.DeepCopy()
		updated.Status.Configuration.MetadataHandoff.SettlementDigest = s.digest
		if err := s.persist(ctx, writer, updated); err != nil {
			return nil, err
		}
		h = s.current.Status.Configuration.MetadataHandoff
	}
	if h.ReleasedAt != nil {
		if h.ReleasedAt.IsZero() || h.ReleasedAt.After(observedNow()) || h.ReleasedAt.Before(h.ReadyAt) {
			return nil, errors.New("metadata release time is invalid")
		}
		if err := s.absent(ctx); err != nil {
			return nil, err
		}
		if err := s.check(ctx); err != nil {
			return nil, err
		}
		return s.current.DeepCopy(), nil
	}
	for _, ref := range h.Fences {
		if err := s.check(ctx); err != nil {
			return nil, err
		}
		var lease coordination.Lease
		key := client.ObjectKey{Namespace: installation.LeaseNamespace, Name: ref.Name}
		err := reader.Get(ctx, key, &lease)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !metadataFenceBindingMatches(s.current, h, ref, &lease, s.digest) {
			if !metadataFenceBindingMatches(s.current, h, ref, &lease, "") {
				return nil, errors.New("original metadata fence was replaced or changed")
			}
			marked := lease.DeepCopy()
			marked.Annotations[metadataFenceReleaseAnnotation] = s.digest
			marked.Finalizers = nil
			if err := writer.Update(ctx, marked); err != nil {
				return nil, err
			}
			if err := reader.Get(ctx, key, &lease); err != nil {
				return nil, err
			}
			if !metadataFenceBindingMatches(s.current, h, ref, &lease, s.digest) {
				return nil, errors.New("original metadata fence release could not be verified")
			}
		}
		if err := s.check(ctx); err != nil {
			return nil, err
		}
		uid, rv := lease.UID, lease.ResourceVersion
		if err := writer.Delete(ctx, &lease, client.Preconditions{UID: &uid, ResourceVersion: &rv}); err != nil {
			return nil, err
		}
		var gone coordination.Lease
		if err := reader.Get(ctx, key, &gone); !apierrors.IsNotFound(err) {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("released metadata fence is still present")
		}
	}
	if err := s.absent(ctx); err != nil {
		return nil, err
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	updated := s.current.DeepCopy()
	at := metav1.NewTime(observedNow().UTC().Truncate(time.Second))
	updated.Status.Configuration.MetadataHandoff.ReleasedAt = &at
	if err := s.persist(ctx, writer, updated); err != nil {
		return nil, err
	}
	if err := s.absent(ctx); err != nil {
		return nil, err
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return s.current.DeepCopy(), nil
}

// RetireMetadataHandoff removes only independently settled custody. The original
// verified operation and creator/tombstone ledger remain for the existing worker
// finish path. Lost status acknowledgement is not retried with a stale snapshot.
func RetireMetadataHandoff(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, installation BootstrapFenceInstallation, policy string, normalize MetadataSettlementProjection, authorize MetadataSettlementRuntimeCheck, now time.Time) (*cvk.NetworkController, error) {
	s, err := newMetadataSettlement(ctx, reader, writer, expected, installation, policy, normalize, authorize, now)
	if err != nil {
		return nil, err
	}
	h := s.current.Status.Configuration.MetadataHandoff
	if h.SettlementDigest == "" || h.ReleasedAt == nil || h.ReleasedAt.IsZero() || h.ReleasedAt.After(now) || h.ReleasedAt.Before(h.ReadyAt) {
		return nil, errors.New("metadata custody is not independently settled")
	}
	if err := s.absent(ctx); err != nil {
		return nil, err
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	updated := s.current.DeepCopy()
	updated.Status.Configuration.MetadataHandoff = nil
	if err := s.persist(ctx, writer, updated); err != nil {
		return nil, err
	}
	return s.current.DeepCopy(), nil
}
