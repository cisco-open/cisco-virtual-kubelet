// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// One DeviceOperation envelope for resource-specific group convergence proofs.
// Existing LAN names/digests remain unchanged. Protocol namespaces prevent a
// result from another controller action family from being consumed as proof.
func configurationDeviceProofOperation(nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, member *ops.ControllerConfigurationReservation, result string, completed time.Time, commands []string, namePrefix, annotation string) *ops.DeviceOperation {
	raw, _ := json.Marshal(struct {
		ControllerUID, Operation, Group, Task, DeviceUID, WorkerUID, Result string
		Completed                                                           time.Time
	}{string(nc.UID), op.ID, op.PhysicalGroup.Digest, op.TaskID, member.Spec.Device.UID, member.Spec.DeviceWorkerPod.UID, result, completed.UTC()})
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ttl := int32(300)
	return &ops.DeviceOperation{ObjectMeta: metav1.ObjectMeta{
		Namespace: nc.Namespace, Name: namePrefix + digest[:48],
		Annotations:     map[string]string{annotation: digest},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: cvk.GroupVersion.String(), Kind: "NetworkController", Name: nc.Name, UID: nc.UID}},
	}, Spec: ops.DeviceOperationSpec{DeviceRef: configapi.DeviceRef{Name: member.Spec.Device.Name}, Operation: ops.DeviceOperationRequest{Kind: ops.OperationKindShowCommand, Commands: append([]string(nil), commands...)}, TTLSecondsAfterFinished: &ttl}}
}

func validateConfigurationDeviceProofOperation(leaf, expected *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, completed, now time.Time, annotation string, pending error) error {
	if leaf == nil || expected == nil || member == nil || leaf.Namespace != expected.Namespace || leaf.Name != expected.Name || leaf.DeletionTimestamp != nil || !reflect.DeepEqual(leaf.Spec, expected.Spec) || !reflect.DeepEqual(leaf.OwnerReferences, expected.OwnerReferences) || leaf.Annotations[annotation] != expected.Annotations[annotation] {
		return errConfigurationStale
	}
	if leaf.UID == "" || !managedprotocol.NetworkObjectBindingComplete(leaf.Annotations) || leaf.Annotations[managedprotocol.AnnotationDeviceNamespace] != member.Namespace || leaf.Annotations[managedprotocol.AnnotationDeviceName] != member.Spec.Device.Name || leaf.Annotations[managedprotocol.AnnotationDeviceUID] != member.Spec.Device.UID || leaf.Annotations[managedprotocol.AnnotationNetworkWorkerUsername] != member.Spec.DeviceWorkerUsername || leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] != member.Spec.DeviceWorkerPod.Name || leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] != member.Spec.DeviceWorkerPod.UID {
		return pending
	}
	status := &leaf.Status
	if status.Phase != ops.OperationPhaseSucceeded || status.ObservedGeneration != leaf.Generation || status.StartTime == nil || status.CompletionTime == nil || leaf.CreationTimestamp.IsZero() || !leaf.CreationTimestamp.After(completed) || status.StartTime.Before(&leaf.CreationTimestamp) || status.CompletionTime.Before(status.StartTime) || status.CompletionTime.After(now) || now.Sub(status.CompletionTime.Time) > 5*time.Minute {
		return pending
	}
	ready := false
	for _, condition := range status.Conditions {
		if condition.Type == "Ready" {
			if ready || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != leaf.Generation || condition.LastTransitionTime.IsZero() || condition.LastTransitionTime.After(now) {
				return pending
			}
			ready = true
		}
	}
	if !ready {
		return pending
	}
	if len(status.ArtifactURIs) != 0 || len(status.Outputs) != len(expected.Spec.Operation.Commands) {
		return errInvalidResponse
	}
	for i, output := range status.Outputs {
		if output.Command != expected.Spec.Operation.Commands[i] || output.Err != "" || output.Output == "" || output.Redacted || output.Truncated || len(output.Output) > 64*1024 {
			return errInvalidResponse
		}
	}
	return nil
}

// The manager alone supplies protected worker bindings; the connector creates
// only a read request. Stable names make observation idempotent across restarts.
func (a *adapter) readConfigurationDeviceProof(ctx context.Context, expected *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, annotation string, completed, now time.Time, pending error) ([]string, error) {
	var leaf ops.DeviceOperation
	err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(expected), &leaf)
	if apierrors.IsNotFound(err) {
		if err := a.statusWriter.Create(ctx, expected); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		return nil, pending
	}
	if err != nil {
		return nil, err
	}
	if err := validateConfigurationDeviceProofOperation(&leaf, expected, member, completed, now, annotation, pending); err != nil {
		return nil, err
	}
	outputs := make([]string, len(leaf.Status.Outputs))
	for i, output := range leaf.Status.Outputs {
		outputs[i] = output.Output
	}
	return outputs, nil
}

func verifyConfigurationChassisSerial(serial, output string) error {
	serials := []string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.Join(fields[:3], " ") == "Processor board ID" {
			if len(fields) != 4 {
				return errInvalidResponse
			}
			serials = append(serials, fields[3])
		}
	}
	expected, err := topologyidentity.CanonicalPhysicalIdentity(serial)
	if err != nil || len(serials) != 1 {
		return errConfigurationStale
	}
	actual, err := topologyidentity.CanonicalPhysicalIdentity(serials[0])
	if err != nil || actual != expected {
		return errConfigurationStale
	}

	return nil
}
