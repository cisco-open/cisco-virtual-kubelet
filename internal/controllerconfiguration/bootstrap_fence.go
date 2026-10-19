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
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const BootstrapFenceProtocol = "cvk-bootstrap-fence.v1"
const bootstrapFenceNamePrefix = "cvk-bootstrap-"
const bootstrapFenceBindingAnnotation = "ops.cisco.vk/bootstrap-binding"
const bootstrapFenceLabel = "ops.cisco.vk/bootstrap-fence"
const bootstrapFenceFinalizer = "ops.cisco.vk/bootstrap-fence-evidence"

// BootstrapFenceRuntimeCheck must corroborate deployed manager/admission
// compatibility and the configured shared lease namespace. It does not grant
// native dispatch. No production runtime supplies this check yet.
type BootstrapFenceRuntimeCheck func(context.Context, string, *cvk.NetworkController, BootstrapIdentityObservation) error

// BootstrapFenceName is independent of controller and tenant namespace. All
// managers must use the same configured lease namespace for this serial fence.
// Existing per-CiscoDevice disruptive-mutation lease names are unchanged.
func BootstrapFenceName(physical string) (string, error) {
	canonical, err := topologyidentity.CanonicalPhysicalIdentity(physical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return bootstrapFenceNamePrefix + hex.EncodeToString(sum[:16]), nil
}

type bootstrapFenceRecord struct {
	Protocol       string                       `json:"protocol"`
	OwnerNamespace string                       `json:"ownerNamespace"`
	Identity       BootstrapIdentityObservation `json:"identity"`
	Worker         bootstrapControllerWorker    `json:"worker"`
	Installation   BootstrapFenceInstallation   `json:"installation"`
	Protection     BootstrapFenceProtection     `json:"protection"`
}

func bootstrapFenceCandidate(namespace, ownerNamespace string, identity *BootstrapIdentityObservation, worker *bootstrapControllerWorker, installation BootstrapFenceInstallation, protection *BootstrapFenceProtection) (*coordination.Lease, error) {
	if len(validation.IsDNS1123Label(namespace)) != 0 || len(validation.IsDNS1123Label(ownerNamespace)) != 0 || identity == nil {
		return nil, errors.New("bootstrap fence namespace or identity is unavailable")
	}
	canonical, physicalErr := topologyidentity.CanonicalPhysicalIdentity(identity.PhysicalIdentity)
	digest, digestErr := hex.DecodeString(identity.OperationDigest)
	if physicalErr != nil || canonical != identity.PhysicalIdentity || digestErr != nil || len(digest) != 32 || hex.EncodeToString(digest) != identity.OperationDigest ||
		!operationIDPattern.MatchString(identity.OperationID) || identity.Controller.UID == "" || identity.Config.UID == "" ||
		len(validation.IsDNS1123Subdomain(identity.Controller.Name)) != 0 || len(validation.IsDNS1123Subdomain(identity.Config.Name)) != 0 ||
		identity.ControllerGeneration < 1 || identity.ConfigGeneration < 1 || identity.NativeTargetID == "" || (identity.SourceUID == "") != (identity.SourceVersion == "") {
		return nil, errors.New("bootstrap fence binding is incomplete")
	}
	if worker == nil || len(worker.ContractHash) != 64 || strings.Trim(worker.ContractHash, "0123456789abcdef") != "" || !strings.HasPrefix(worker.Username, "system:serviceaccount:"+ownerNamespace+":") ||
		len(validation.IsDNS1123Subdomain(strings.TrimPrefix(worker.Username, "system:serviceaccount:"+ownerNamespace+":"))) != 0 {
		return nil, errors.New("bootstrap fence worker binding is incomplete")
	}
	for _, ref := range []ops.ConfigurationReservationReference{worker.Deployment, worker.ReplicaSet, worker.Pod} {
		if ref.UID == "" || len(validation.IsDNS1123Subdomain(ref.Name)) != 0 {
			return nil, errors.New("bootstrap fence worker incarnation is incomplete")
		}
	}
	if installation.LeaseNamespace != namespace || len(validation.IsDNS1123Subdomain(installation.PolicyName)) != 0 || !validBootstrapProtectionIdentities(installation.ManagerUsername, namespace) || protection == nil || protection.PolicyUID == "" || protection.BindingUID == "" || protection.NamespaceUID == "" || protection.PolicyGeneration < 1 || protection.BindingGeneration < 1 || (protection.PolicyDigest != bootstrapProtectionSpecDigest && protection.PolicyDigest != bootstrapLegacyProtectionSpecDigest) {
		return nil, errors.New("bootstrap fence protection binding is incomplete")
	}
	name, err := BootstrapFenceName(identity.PhysicalIdentity)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(bootstrapFenceRecord{BootstrapFenceProtocol, ownerNamespace, *identity, *worker, installation, *protection})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	holder := devicecoordination.HolderIdentity("controller-bootstrap", namespace, name, hex.EncodeToString(sum[:]))
	return &coordination.Lease{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name,
			Labels:      map[string]string{bootstrapFenceLabel: "true"},
			Annotations: map[string]string{bootstrapFenceBindingAnnotation: string(raw), devicecoordination.RetainLeaseAnnotation: "true"},
			Finalizers:  []string{bootstrapFenceFinalizer}},
		// This is a retained reservation, not an expiring leader-election grant.
		// No renewal, expiry takeover, release or native permission is implied.
		Spec: coordination.LeaseSpec{HolderIdentity: &holder},
	}, nil
}

// ReadBootstrapFence identifies retained evidence without requiring its source
// to remain live. Device enrollment must wait on either a returned fence OR an
// error; malformed/deleting evidence is not absence. This is not a grant check.
func ReadBootstrapFence(ctx context.Context, reader client.Reader, namespace, physical string) (*coordination.Lease, error) {
	reject := errors.New("bootstrap fence evidence is unavailable or malformed")
	lease, err := readBootstrapFenceObject(ctx, reader, namespace, physical)
	if err != nil || lease == nil {
		return nil, err
	}
	name := lease.Name
	var record bootstrapFenceRecord
	if json.Unmarshal([]byte(lease.Annotations[bootstrapFenceBindingAnnotation]), &record) != nil || record.Protocol != BootstrapFenceProtocol {
		return nil, reject
	}
	expected, err := bootstrapFenceCandidate(namespace, record.OwnerNamespace, &record.Identity, &record.Worker, record.Installation, &record.Protection)
	if err != nil || expected.Name != name || !sameBootstrapFence(lease, expected) {
		return nil, reject
	}
	return lease.DeepCopy(), nil
}

// CheckBootstrapEnrollment proves only absence at the reserved serial key.
// Any object there, including foreign/malformed/deleting evidence, holds new
// enrollment. It never treats an invalid binding as absence, reads source
// values, changes a Lease or grants physical dispatch authority.
func CheckBootstrapEnrollment(ctx context.Context, reader client.Reader, namespace, physical string) error {
	lease, err := readBootstrapFenceObject(ctx, reader, namespace, physical)
	if err != nil {
		return err
	}
	if lease != nil {
		return errors.New("device enrollment is held by retained controller bootstrap evidence")
	}
	return nil
}

func readBootstrapFenceObject(ctx context.Context, reader client.Reader, namespace, physical string) (*coordination.Lease, error) {
	reject := errors.New("bootstrap fence evidence is unavailable or malformed")
	if reader == nil || len(validation.IsDNS1123Label(namespace)) != 0 {
		return nil, reject
	}
	name, err := BootstrapFenceName(physical)
	if err != nil {
		return nil, err
	}
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return lease.DeepCopy(), nil
}

func sameBootstrapFence(actual, expected *coordination.Lease) bool {
	if actual != nil {
		if _, marked := actual.Labels[metadataFenceLabel]; marked {
			return false
		}
	}

	return actual != nil && expected != nil && actual.UID != "" && actual.ResourceVersion != "" && actual.DeletionTimestamp == nil &&
		actual.Namespace == expected.Namespace && actual.Name == expected.Name && len(actual.OwnerReferences) == 0 &&
		actual.Labels[bootstrapFenceLabel] == "true" && actual.Annotations[devicecoordination.RetainLeaseAnnotation] == "true" &&
		actual.Annotations[bootstrapFenceBindingAnnotation] == expected.Annotations[bootstrapFenceBindingAnnotation] &&
		slices.Equal(actual.Finalizers, expected.Finalizers) && reflect.DeepEqual(actual.Spec, expected.Spec)
}

// ReserveBootstrapFence creates only a retained manager-side serial fence.
// The returned Lease is not a DispatchPermit and has no Check/submit method.
// Callers must supply the global uncached reader and an installed runtime check.
// Bootstrap worker admission, new-device rollout exclusion and completion remain
// separate; this helper is intentionally not wired into production dispatch.
func ReserveBootstrapFence(ctx context.Context, reader client.Reader, writer client.Writer, expected *cvk.NetworkController, installation BootstrapFenceInstallation, authorize BootstrapFenceRuntimeCheck) (*coordination.Lease, error) {
	reject := errors.New("bootstrap serial fence lacks current identity or compatible runtime")
	leaseNamespace := installation.LeaseNamespace
	if reader == nil || writer == nil || expected == nil || authorize == nil || len(validation.IsDNS1123Label(leaseNamespace)) != 0 {
		return nil, reject
	}
	expected = expected.DeepCopy()
	identity, err := ReadBootstrapIdentity(ctx, reader, expected)
	if err != nil {
		return nil, err
	}
	if err := authorize(ctx, leaseNamespace, expected.DeepCopy(), *identity); err != nil {
		return nil, err
	}
	protection, err := ReadBootstrapFenceProtection(ctx, reader, installation.PolicyName, installation.ManagerUsername, leaseNamespace)
	if err != nil {
		return nil, err
	}
	worker, err := readBootstrapControllerWorker(ctx, reader, expected, time.Now())
	if err != nil {
		return nil, err
	}
	corroborate := func() (*cvk.NetworkController, error) {
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
			return nil, err
		}
		fresh, err := ReadBootstrapIdentity(ctx, reader, &nc)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(fresh, identity) {
			return nil, reject
		}
		actor, err := readBootstrapControllerWorker(ctx, reader, &nc, time.Now())
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(actor, worker) {
			return nil, reject
		}
		currentProtection, err := ReadBootstrapFenceProtection(ctx, reader, installation.PolicyName, installation.ManagerUsername, leaseNamespace)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(currentProtection, protection) {
			return nil, reject
		}
		return &nc, nil
	}
	if _, err := corroborate(); err != nil {
		return nil, err
	}
	candidate, err := bootstrapFenceCandidate(leaseNamespace, expected.Namespace, identity, worker, installation, protection)
	if err != nil {
		return nil, err
	}
	stored := &coordination.Lease{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(candidate), stored); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		stored = candidate.DeepCopy()
		if err := writer.Create(ctx, stored); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				// An uncertain create can leave a fence. Never delete or take over
				// that evidence; a subsequent Prepared observation may read it.
				return nil, err
			}
			if err := reader.Get(ctx, client.ObjectKeyFromObject(candidate), stored); err != nil {
				return nil, err
			}
		}
	}
	if !sameBootstrapFence(stored, candidate) {
		return nil, reject
	}
	nc, err := corroborate()
	if err != nil {
		return nil, err
	}
	if err := authorize(ctx, leaseNamespace, nc.DeepCopy(), *identity); err != nil {
		return nil, err
	}
	if _, err := corroborate(); err != nil {
		return nil, err
	}
	var fresh coordination.Lease
	if err := reader.Get(ctx, client.ObjectKeyFromObject(stored), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != stored.UID || !sameBootstrapFence(&fresh, candidate) {
		return nil, reject
	}
	return fresh.DeepCopy(), nil
}
