// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

const ciscoDevicePhysicalIdentityIndex = "spec.physicalIdentity"

type managedPhysicalIdentityObservation struct {
	ready    bool
	conflict bool
	reason   string
	message  string
}

// managedPhysicalIdentityState keeps the administrator declaration and the
// immutable manager binding authoritative while treating NodeInfo as live,
// authenticated consistency evidence. The initialization guard may be removed
// only after all three identities agree for the exact Device and Node
// incarnations.
func managedPhysicalIdentityState(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) managedPhysicalIdentityObservation {
	if device == nil {
		return managedPhysicalIdentityObservation{
			conflict: true,
			reason:   "PhysicalIdentityBindingMismatch",
			message:  "CiscoDevice is unavailable for physical-identity verification",
		}
	}
	declared, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil {
		return managedPhysicalIdentityObservation{
			conflict: true,
			reason:   "PhysicalIdentityDeclarationInvalid",
			message:  err.Error(),
		}
	}
	if device.Status.NodeIdentity == nil {
		return managedPhysicalIdentityObservation{
			reason:  "PhysicalIdentityBindingPending",
			message: "manager physical-identity binding is not established yet",
		}
	}
	if node == nil {
		return managedPhysicalIdentityObservation{
			reason:  "PhysicalIdentityObservationPending",
			message: "bound Node is unavailable for physical-identity verification",
		}
	}
	binding := device.Status.NodeIdentity
	if binding.DeviceUID != string(device.UID) || binding.NodeName != node.Name ||
		binding.NodeUID != string(node.UID) || binding.PhysicalIdentity != declared {
		return managedPhysicalIdentityObservation{
			conflict: true,
			reason:   "PhysicalIdentityBindingMismatch",
			message:  "manager physical-identity binding does not match the current CiscoDevice and Node incarnations",
		}
	}
	machineID := strings.TrimSpace(node.Status.NodeInfo.MachineID)
	systemUUID := strings.TrimSpace(node.Status.NodeInfo.SystemUUID)
	if machineID == "" && systemUUID == "" {
		return managedPhysicalIdentityObservation{
			reason:  "PhysicalIdentityObservationPending",
			message: "managed worker has not reported a live device physical identity",
		}
	}
	if _, err := topology.ObservedPhysicalIdentity(declared, machineID, systemUUID); err != nil {
		return managedPhysicalIdentityObservation{
			conflict: true,
			reason:   "PhysicalIdentityObservationMismatch",
			message:  err.Error(),
		}
	}
	return managedPhysicalIdentityObservation{
		ready:   true,
		reason:  "PhysicalIdentityVerified",
		message: "declared, manager-bound, and live worker physical identities agree",
	}
}

func physicalIdentityIndexValues(object client.Object) []string {
	device, ok := object.(*ciskov1.CiscoDevice)
	if !ok {
		return nil
	}
	identity, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil {
		return nil
	}
	return []string{identity}
}

// validateManagedPhysicalIdentity uses an uncached cluster-wide list before a
// managed identity is first bound. Once bound, immutable declaration plus the
// admission-time scan prevents a second writer from enrolling, so steady-state
// health reconciles use the indexed cache. CiscoDevice watch fanout makes a new,
// removed, or newly selected peer re-evaluate both sides without an O(N²)
// uncached inventory loop.
func (r *CiscoDeviceReconciler) validateManagedPhysicalIdentity(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	policy *topologyrollout.ParsedAdminPolicy,
) (string, error) {
	if device == nil || device.UID == "" {
		return "", fmt.Errorf("managed physical identity requires a persisted CiscoDevice UID")
	}
	identity, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil {
		return "", err
	}
	if binding := device.Status.NodeIdentity; binding != nil && binding.PhysicalIdentity != identity {
		return "", fmt.Errorf("status.nodeIdentity.physicalIdentity %q does not match canonical spec.physicalIdentity %q",
			binding.PhysicalIdentity, identity)
	}
	if policy == nil || policy.Selector == nil {
		return "", fmt.Errorf("managed physical identity deduplication requires the administrator fleet selector")
	}
	var devices ciskov1.CiscoDeviceList
	if device.Status.NodeIdentity == nil {
		if r.APIReader == nil {
			return "", fmt.Errorf("managed physical identity enrollment requires an uncached API reader")
		}
		if err := r.APIReader.List(ctx, &devices); err != nil {
			return "", fmt.Errorf("list CiscoDevices for physical identity enrollment: %w", err)
		}
	} else {
		if r.Client == nil {
			return "", fmt.Errorf("managed physical identity reconciliation requires an indexed cache client")
		}
		if err := r.Client.List(ctx, &devices,
			client.MatchingFields{ciscoDevicePhysicalIdentityIndex: identity}); err != nil {
			return "", fmt.Errorf("list indexed CiscoDevice physical-identity peers: %w", err)
		}
	}
	for i := range devices.Items {
		other := &devices.Items[i]
		if other.UID == device.UID {
			continue
		}
		otherIdentity, otherErr := topology.CanonicalPhysicalIdentity(other.Spec.PhysicalIdentity)
		if otherErr != nil || otherIdentity != identity {
			continue
		}
		// A never-managed standalone object remains outside this opt-in
		// contract. Selected objects and every retained managed incarnation stay
		// in the duplicate fence even after selector exit or reverse handoff.
		managedPeer := policy.Selector.Matches(labels.Set(other.Labels)) ||
			other.Status.NodeIdentity != nil || other.Status.LegacyHandoff != nil
		if !managedPeer {
			continue
		}
		return "", fmt.Errorf("physicalIdentity %q is also declared by CiscoDevice %s/%s (UID %s)",
			identity, other.Namespace, other.Name, other.UID)
	}
	return identity, nil
}

// mapPhysicalIdentityPeers wakes an existing peer when a duplicate is created
// or removed. Admission still re-lists through APIReader, so cache lag cannot
// establish uniqueness.
func (r *CiscoDeviceReconciler) mapPhysicalIdentityPeers(
	ctx context.Context,
	object client.Object,
) []ctrl.Request {
	values := physicalIdentityIndexValues(object)
	if len(values) != 1 {
		return nil
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.Client.List(ctx, &devices, client.MatchingFields{ciscoDevicePhysicalIdentityIndex: values[0]}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "map physical identity peer CiscoDevices", "physicalIdentity", values[0])
		return nil
	}
	requests := make([]ctrl.Request, 0, len(devices.Items))
	for i := range devices.Items {
		peer := &devices.Items[i]
		sameObject := object.GetUID() != "" && peer.UID == object.GetUID()
		if object.GetUID() == "" {
			sameObject = peer.Namespace == object.GetNamespace() && peer.Name == object.GetName()
		}
		if sameObject {
			continue
		}
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(peer)})
	}
	return requests
}
