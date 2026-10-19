// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckUnmanagedMetadataScope is an uncached manager-side observation for an
// already Prepared metadata Delete, with serials supplied by the adapter's
// independently validated creator cohort. It reuses the bootstrap identity
// census and source binding; it neither reconstructs physical TargetSerial nor
// creates a reservation, grant, lease, status attestation or dispatch permit.
// Namespace-scoped workers must not be given cluster-wide list access to call
// this helper. Installation verification, actor binding and serialization are
// still required before a production manager may admit new metadata cleanup.
// Cross-object reads are not atomic and cannot exclude unknown hardware aliases
// or external activity. Address-only/malformed existing CiscoDevice consumers
// hold metadata admission closed; enrollment must also fence future consumers.
// Every returned error holds the prerequisite closed.
func CheckUnmanagedMetadataScope(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, leaseNamespace string, serials []string) error {
	return checkUnmanagedMetadataScope(ctx, reader, expected, leaseNamespace, serials, nil)
}

// fenceCheck is private to acquisition: public observation still requires absence.
func checkUnmanagedMetadataScope(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, leaseNamespace string, serials []string, fenceCheck func(string) error) error {
	reject := errors.New("unmanaged metadata scope lacks current source-bound preparation or complete manager observations")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || len(validation.IsDNS1123Label(leaseNamespace)) != 0 || len(serials) == 0 || len(serials) > 256 {
		return reject
	}
	expected = expected.DeepCopy()
	serials = append([]string(nil), serials...)
	physical := map[string]bool{}
	for _, serial := range serials {
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(serial)
		if err != nil || canonical != serial || physical[canonical] {
			return reject
		}
		physical[canonical] = true
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Generation != expected.Generation || nc.Generation < 1 || nc.Namespace == "" || nc.Name == "" || nc.Spec.Type == "" || nc.Spec.Paused || nc.DeletionTimestamp != nil || !reflect.DeepEqual(nc.Spec, expected.Spec) || !reflect.DeepEqual(nc.Status.Configuration, expected.Status.Configuration) || nc.Status.Configuration == nil {
		return reject
	}
	op := nc.Status.Configuration.Operation
	if op == nil || !operationIDPattern.MatchString(op.ID) || op.Phase != "Prepared" || op.Action != "Delete" || op.Section == "" || op.Resource == "" || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration != nc.Generation || op.TargetID == "" || op.TargetSerial != "" || op.TaskID != "" || op.CreatedResourceID != "" || op.ParentID != "" || op.SubmittedAt != nil || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) > 0 || len(op.SSIDCredentialHistory) > 0 || (op.SourceUID == "") != (op.SourceVersion == "") {
		return reject
	}
	ref := ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
	for range 2 {
		if err := readBoundConfigurationSource(ctx, reader, &nc, nc.Namespace, ref, op.ConfigGeneration); err != nil {
			return err
		}
		if err := readUnmanagedIdentityConflicts(ctx, reader, &nc, physical, true); err != nil {
			return err
		}
		// Retained reservations can outlive their original device/controller work.
		// Do not infer that settled, cancelled, deleting or malformed rows are clear.
		var reservations ops.ControllerConfigurationReservationList
		if err := reader.List(ctx, &reservations); err != nil {
			return err
		}
		for _, row := range reservations.Items {
			canonical, err := topologyidentity.CanonicalPhysicalIdentity(row.Spec.PhysicalIdentity)
			if err != nil || physical[canonical] {
				return reject
			}
		}
		for _, serial := range serials {
			check := fenceCheck
			if check == nil {
				check = func(serial string) error { return CheckBootstrapEnrollment(ctx, reader, leaseNamespace, serial) }
			}
			if err := check(serial); err != nil {
				return err
			}
		}
	}
	if err := readBoundConfigurationSource(ctx, reader, &nc, nc.Namespace, ref, op.ConfigGeneration); err != nil {
		return err
	}
	var fresh cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&nc), &fresh); err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(fresh.Spec, nc.Spec) || !reflect.DeepEqual(fresh.Status.Configuration, nc.Status.Configuration) {
		return reject
	}
	return nil
}
