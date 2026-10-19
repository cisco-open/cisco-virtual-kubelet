// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"strings"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BootstrapIdentityObservation contains source-bound facts with no known
// conflicting cluster identity. It is not a reservation, lease, grant or permit and
// cannot be passed to managed dispatch. Native readiness/ownership proof, actor
// admission, serialization and final observation remain separate requirements.
type BootstrapIdentityObservation struct {
	Controller                             ops.ConfigurationReservationReference
	Config                                 ops.ConfigurationReservationReference
	ControllerGeneration, ConfigGeneration int64
	OperationID, OperationDigest           string
	NativeTargetID, PhysicalIdentity       string
	SourceUID, SourceVersion               string
}

// ReadBootstrapIdentity requires an uncached reader with cluster-wide list
// visibility. It is intended for the manager, not a namespace-scoped controller
// worker. It does not read credentials or issue any Kubernetes/native writes.
// Two full observations detect known identity conflicts and late aliases;
// absence at this boundary is not an atomic lock against future declarations,
// nor proof about unknown aliases or hardware outside cluster observations.
func ReadBootstrapIdentity(ctx context.Context, reader client.Reader, expected *cvk.NetworkController) (*BootstrapIdentityObservation, error) {
	reject := errors.New("bootstrap identity lacks exclusive current source and unmanaged physical scope")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Namespace == "" || nc.Name == "" || nc.Generation < 1 || nc.Spec.Type == "" || nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Status.Configuration == nil {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	digest, err := OperationDigest(op)
	if err != nil {
		return nil, err
	}
	if op.Phase != "Prepared" || op.Action != "Update" || op.TaskID != "" || op.SubmittedAt != nil || op.ControllerGeneration != nc.Generation {
		return nil, reject
	}
	physical, err := topologyidentity.CanonicalPhysicalIdentity(op.TargetSerial)
	if err != nil || physical != op.TargetSerial {
		return nil, reject
	}
	ref := ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
	for range 2 {
		if err := readBoundConfigurationSource(ctx, reader, &nc, nc.Namespace, ref, op.ConfigGeneration); err != nil {
			return nil, err
		}
		if err := readBootstrapIdentityConflicts(ctx, reader, &nc, physical); err != nil {
			return nil, err
		}
	}
	// Corroborate source after the final global lists, including ConfigMap
	// incarnation/version. The common helper also rechecks controller RV.
	if err := readBoundConfigurationSource(ctx, reader, &nc, nc.Namespace, ref, op.ConfigGeneration); err != nil {
		return nil, err
	}
	var fresh cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&nc), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration, nc.Status.Configuration) {
		return nil, reject
	}
	return &BootstrapIdentityObservation{
		Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ref,
		ControllerGeneration: nc.Generation, ConfigGeneration: op.ConfigGeneration,
		OperationID: op.ID, OperationDigest: digest, NativeTargetID: op.TargetID, PhysicalIdentity: physical,
		SourceUID: op.SourceUID, SourceVersion: op.SourceVersion,
	}, nil
}

func readBootstrapIdentityConflicts(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, physical string) error {
	return readUnmanagedIdentityConflicts(ctx, reader, nc, map[string]bool{physical: true}, false)
}

// One complete cluster census for the selected set, shared by bootstrap and
// metadata prerequisites. Metadata additionally refuses unknown/malformed device
// identities and recognizable CVK Nodes without consistent observations. Both
// modes retain foreign handoff custody; bootstrap otherwise keeps its existing
// known-alias scope. Absence is never
// a reservation or proof of undeclared hardware aliases.
func readUnmanagedIdentityConflicts(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, physical map[string]bool, requireKnownDeviceIdentity bool) error {
	reject := errors.New("unmanaged physical scope conflicts with retained cluster identity or work")
	conflicts := func(value string) bool {
		serial, err := topologyidentity.CanonicalPhysicalIdentity(value)
		return err == nil && physical[serial]
	}
	var devices cvk.CiscoDeviceList
	if err := reader.List(ctx, &devices); err != nil {
		return err
	}
	for _, device := range devices.Items {
		if requireKnownDeviceIdentity {
			identities, err := DevicePhysicalIdentities(&device)
			if err != nil || len(identities) == 0 {
				return errors.New("metadata cleanup requires known declared or retained identity for every CiscoDevice consumer")
			}
		}
		if conflicts(device.Spec.PhysicalIdentity) || device.Status.NodeIdentity != nil && conflicts(device.Status.NodeIdentity.PhysicalIdentity) {
			return reject
		}
	}
	var nodes core.NodeList
	if err := reader.List(ctx, &nodes); err != nil {
		return err
	}
	for _, node := range nodes.Items {
		if requireKnownDeviceIdentity && metadataNodeIdentityRequired(&node) {
			declared := node.Status.NodeInfo.MachineID
			if declared == "" {
				declared = node.Status.NodeInfo.SystemUUID
			}
			if _, err := topologyidentity.ObservedPhysicalIdentity(declared, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.SystemUUID); err != nil {
				return reject
			}
		}
		if conflicts(node.Status.NodeInfo.MachineID) || conflicts(node.Status.NodeInfo.SystemUUID) {
			return reject
		}
	}
	var controllers cvk.NetworkControllerList
	if err := reader.List(ctx, &controllers); err != nil {
		return err
	}
	for _, controller := range controllers.Items {
		if controller.UID == nc.UID && controller.Namespace == nc.Namespace && controller.Name == nc.Name {
			continue
		}
		state := controller.Status.Configuration
		if state == nil {
			continue
		}
		// A cleared journal, certified refusal, release marker or deleting
		// controller cannot discard the original serial custody. Observe the
		// entire sealed cohort until its manager explicitly retires the handoff.
		if h := state.MetadataHandoff; h != nil {
			if h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || len(h.PhysicalIdentities) == 0 || len(h.PhysicalIdentities) > 256 {
				return reject
			}
			seen := make(map[string]bool, len(h.PhysicalIdentities))
			for _, identity := range h.PhysicalIdentities {
				canonical, err := topologyidentity.CanonicalPhysicalIdentity(identity)
				if err != nil || canonical != identity || seen[canonical] || physical[canonical] {
					return reject
				}
				seen[canonical] = true
			}
		}
		if state.Operation == nil {
			continue
		}
		op := state.Operation
		if conflicts(op.TargetSerial) {
			return reject
		}
		if op.PhysicalGroup != nil {
			for _, target := range op.PhysicalGroup.Targets {
				if conflicts(target.PhysicalIdentity) {
					return reject
				}
			}
		}
	}
	return nil
}

// Recognizable CVK Nodes must not escape a metadata census just because their
// hardware observation is missing or inconsistent. Ordinary compute Nodes and
// other virtual providers keep their existing known-alias collision checks.
func metadataNodeIdentityRequired(node *core.Node) bool {
	if node.Labels[topology.LabelProvider] == topology.ProviderCiscoAppHosting || strings.EqualFold(node.Status.NodeInfo.OperatingSystem, "Cisco") {
		return true
	}
	for _, key := range []string{managedprotocol.AnnotationManaged, managedprotocol.AnnotationDeviceNamespace, managedprotocol.AnnotationDeviceName, managedprotocol.AnnotationDeviceUID} {
		if _, present := node.Annotations[key]; present {
			return true
		}
	}
	return false
}
