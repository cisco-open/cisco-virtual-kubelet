// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckBootstrapDeviceEnrollment checks known declared/retained serials using
// uncached manager reads. It is shared by manager enrollment and aggregator
// write guards. Absence is neither a distributed lock nor dispatch authority.
// Unknown identities wait while any reserved serial key exists; already-running
// operations and undeclared hardware aliases still require separate proof.
func CheckBootstrapDeviceEnrollment(ctx context.Context, reader client.Reader, namespace string, device *cvk.CiscoDevice) error {
	if reader == nil || device == nil || len(validation.IsDNS1123Label(namespace)) != 0 {
		return errors.New("bootstrap device inspection requires current uncached identity")
	}
	serials, err := DevicePhysicalIdentities(device)
	if err != nil {
		return err
	}
	var current cvk.CiscoDevice
	if err := reader.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		return err
	}
	if current.UID == "" || current.UID != device.UID || current.Generation != device.Generation || !reflect.DeepEqual(current.Spec, device.Spec) || !reflect.DeepEqual(current.Status.NodeIdentity, device.Status.NodeIdentity) || !reflect.DeepEqual(current.DeletionTimestamp, device.DeletionTimestamp) {
		return errors.New("device changed before bootstrap enrollment inspection")
	}
	if len(serials) == 0 {
		// An address-only legacy consumer cannot prove it is disjoint from a
		// retained serial fence. Repeated full namespace reads are also used
		// by the aggregator before writes; no labels or TTL imply clearance.
		for range 2 {
			var fences coordination.LeaseList
			if err := reader.List(ctx, &fences, client.InNamespace(namespace)); err != nil {
				return err
			}
			if fences.Continue != "" || fences.RemainingItemCount != nil && *fences.RemainingItemCount > 0 {
				return errors.New("bootstrap enrollment requires the complete fence namespace")
			}
			for _, fence := range fences.Items {
				if strings.HasPrefix(fence.Name, bootstrapFenceNamePrefix) {
					return errors.New("unknown device identity cannot bypass retained bootstrap custody")
				}
			}
		}
	}
	for _, serial := range serials {
		if err := CheckBootstrapEnrollment(ctx, reader, namespace, serial); err != nil {
			return err
		}
	}
	var fresh cvk.CiscoDevice
	if err := reader.Get(ctx, client.ObjectKeyFromObject(device), &fresh); err != nil {
		return err
	}
	if fresh.UID != current.UID || fresh.ResourceVersion != current.ResourceVersion || !reflect.DeepEqual(fresh.Spec, current.Spec) || !reflect.DeepEqual(fresh.Status.NodeIdentity, current.Status.NodeIdentity) || !reflect.DeepEqual(fresh.DeletionTimestamp, current.DeletionTimestamp) {
		return errors.New("device changed during bootstrap enrollment inspection")
	}
	return nil
}

// DevicePhysicalIdentities returns only canonical declared and retained serials.
// It does not resolve addresses, native inventory or unknown hardware aliases.
func DevicePhysicalIdentities(device *cvk.CiscoDevice) ([]string, error) {
	if device == nil {
		return nil, errors.New("device physical identity is unavailable")
	}
	identities := []string{device.Spec.PhysicalIdentity}
	if device.Status.NodeIdentity != nil {
		identities = append(identities, device.Status.NodeIdentity.PhysicalIdentity)
	}
	serials := map[string]bool{}
	for _, identity := range identities {
		if identity == "" {
			continue
		}
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(identity)
		if err != nil {
			return nil, err
		}
		serials[canonical] = true
	}

	result := make([]string, 0, len(serials))
	for serial := range serials {
		result = append(result, serial)
	}
	slices.Sort(result)
	return result, nil
}
