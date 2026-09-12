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

package maintenance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
)

// BeforeSoftwareUpgradeMutation requests the manager-owned scheduling guard
// for one exact managed leaf. In standalone mode it preserves the historical
// direct Node-taint path.
func (c *Coordinator) BeforeSoftwareUpgradeMutation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if c == nil {
		return nil
	}
	if !c.ManagedTopology {
		return c.BeforeMutation(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	if c.Client == nil || c.Namespace == "" || c.DeviceName == "" || c.DeviceUID == "" ||
		c.NodeName == "" || c.LeaseNamespace == "" {
		return fmt.Errorf("managed maintenance coordinator is incomplete")
	}
	if up == nil || up.UID == "" || up.Namespace != c.Namespace || up.Spec.DeviceRef.Name != c.DeviceName {
		return fmt.Errorf("managed maintenance operation identity is incomplete or does not target this device")
	}
	if up.Annotations[managedprotocol.AnnotationManaged] != "true" ||
		up.Annotations[managedprotocol.AnnotationDeviceUID] != c.DeviceUID {
		return fmt.Errorf("managed maintenance requires a campaign-owned, device-UID-bound software upgrade")
	}
	if up.Status.ManagerAdmission == nil || up.Status.ManagerControl == nil {
		return fmt.Errorf("managed maintenance requires manager admission and control")
	}

	var node corev1.Node
	if err := c.Client.Get(ctx, types.NamespacedName{Name: c.NodeName}, &node); err != nil {
		return fmt.Errorf("read managed maintenance Node: %w", err)
	}
	if err := c.validateManagedNode(&node); err != nil {
		return err
	}
	holder := mutationguard.UpgradeHolderIdentity(up)
	request, err := c.publishMaintenanceRequest(ctx, up, &node, holder)
	if err != nil {
		return err
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("cvk.maintenance.session_id", request.Token),
		attribute.String("cvk.maintenance.lease_uid", request.LeaseUID),
		attribute.String("cvk.maintenance.operation_uid", request.Operation.UID),
	)

	var device ciskov1.CiscoDevice
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: c.DeviceName}, &device); err != nil {
		return fmt.Errorf("read managed maintenance CiscoDevice: %w", err)
	}
	if device.UID == "" || string(device.UID) != c.DeviceUID {
		return fmt.Errorf("managed maintenance CiscoDevice UID %q does not match runtime UID %q", device.UID, c.DeviceUID)
	}
	if !device.DeletionTimestamp.IsZero() {
		return fmt.Errorf("managed maintenance is disabled while the CiscoDevice is terminating")
	}
	if device.Status.NodeIdentity == nil || device.Status.NodeIdentity.NodeName != node.Name ||
		device.Status.NodeIdentity.NodeUID != string(node.UID) || device.Status.NodeIdentity.DeviceUID != c.DeviceUID {
		return fmt.Errorf("managed maintenance Node identity is not manager-bound in CiscoDevice status")
	}
	if err := validateMaintenanceAcknowledgement(device.Status.MaintenanceSession, request, &node); err != nil {
		return err
	}
	if !hasMaintenanceTaint(node.Spec.Taints) {
		return fmt.Errorf("managed maintenance acknowledgement exists but the exact Node guard is absent")
	}
	return nil
}

type publishedRequest struct {
	Token           string
	RequestedAt     time.Time
	ControlRevision int64
	LeaseNamespace  string
	LeaseName       string
	LeaseUID        string
	Holder          string
	Operation       ciskov1.DeviceMaintenanceObjectReference
}

const maxManagedMutationLeaseSeconds = int32((7*24*time.Hour + 26*time.Hour) / time.Second)

func (c *Coordinator) publishMaintenanceRequest(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	node *corev1.Node,
	holder string,
) (publishedRequest, error) {
	key := types.NamespacedName{
		Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(c.Namespace, c.DeviceName),
			devicecoordination.MutationLeaseFamily,
		),
	}
	operation := ciskov1.DeviceMaintenanceObjectReference{
		Namespace: up.Namespace,
		Name:      up.Name,
		UID:       string(up.UID),
	}
	controlRevision := up.Status.ManagerControl.Revision
	var published publishedRequest
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var lease coordv1.Lease
		if err := c.Client.Get(ctx, key, &lease); err != nil {
			return err
		}
		if lease.UID == "" || lease.ResourceVersion == "" ||
			validateActiveManagedMutationLease(&lease, holder) != nil ||
			!time.Now().Before(lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second)) {
			return fmt.Errorf("mutation Lease is not held by this exact software-upgrade operation")
		}
		if err := c.validateManagedLeaseBinding(&lease, node); err != nil {
			return err
		}
		before := lease.DeepCopy()
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		token := lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken]
		parsedToken, tokenErr := uuid.Parse(token)
		requestedAt, parseErr := time.Parse(time.RFC3339Nano,
			lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
		if tokenErr != nil || parsedToken.String() != token || parseErr != nil || !maintenanceRequestMatches(
			lease.Annotations, c, up, node,
		) {
			token = uuid.NewString()
			requestedAt = time.Now().UTC()
		}
		// metav1.Time persists at second precision. Use the same precision on
		// the Lease so a real API round trip can match the acknowledgement.
		requestedAt = requestedAt.UTC().Truncate(time.Second)
		values := map[string]string{
			managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.Version,
			managedprotocol.AnnotationMaintenanceSessionToken:    token,
			managedprotocol.AnnotationMaintenanceRequestedAt:     requestedAt.Format(time.RFC3339Nano),
			managedprotocol.AnnotationMaintenanceOperationNS:     operation.Namespace,
			managedprotocol.AnnotationMaintenanceOperationName:   operation.Name,
			managedprotocol.AnnotationMaintenanceOperationUID:    operation.UID,
			managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(controlRevision, 10),
		}
		for annotation, value := range values {
			lease.Annotations[annotation] = value
		}
		if !stringMapEqual(before.Annotations, lease.Annotations) {
			if err := c.Client.Patch(ctx, &lease,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}
		published = publishedRequest{
			Token: token, RequestedAt: requestedAt, ControlRevision: controlRevision,
			LeaseNamespace: lease.Namespace, LeaseName: lease.Name, LeaseUID: string(lease.UID),
			Holder: holder, Operation: operation,
		}
		return nil
	})
	if err != nil {
		return publishedRequest{}, fmt.Errorf("publish managed maintenance request: %w", err)
	}
	return published, nil
}

func validateActiveManagedMutationLease(lease *coordv1.Lease, holder string) error {
	if lease == nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder ||
		strings.TrimSpace(holder) != holder || holder == "" || len(holder) > 512 ||
		strings.ContainsAny(holder, "\r\n\x00") || lease.Spec.Strategy != nil || lease.Spec.PreferredHolder != nil {
		return fmt.Errorf("managed mutation Lease holder is malformed")
	}
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds <= 0 ||
		*lease.Spec.LeaseDurationSeconds > maxManagedMutationLeaseSeconds || lease.Spec.AcquireTime == nil ||
		lease.Spec.RenewTime == nil || lease.Spec.AcquireTime.After(lease.Spec.RenewTime.Time) ||
		lease.Spec.LeaseTransitions == nil || *lease.Spec.LeaseTransitions <= 0 {
		return fmt.Errorf("managed mutation Lease timing/transition metadata is malformed")
	}
	return nil
}

func maintenanceRequestMatches(
	annotations map[string]string,
	c *Coordinator,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	node *corev1.Node,
) bool {
	return annotations[managedprotocol.AnnotationMaintenanceRequestVersion] == managedprotocol.Version &&
		annotations[managedprotocol.AnnotationMaintenanceOperationNS] == up.Namespace &&
		annotations[managedprotocol.AnnotationMaintenanceOperationName] == up.Name &&
		annotations[managedprotocol.AnnotationMaintenanceOperationUID] == string(up.UID) &&
		annotations[managedprotocol.AnnotationDeviceNamespace] == c.Namespace &&
		annotations[managedprotocol.AnnotationDeviceName] == c.DeviceName &&
		annotations[managedprotocol.AnnotationDeviceUID] == c.DeviceUID &&
		annotations[managedprotocol.AnnotationNodeName] == node.Name &&
		annotations[managedprotocol.AnnotationNodeUID] == string(node.UID) &&
		annotations[managedprotocol.AnnotationWorkerUsername] == node.Annotations[managedprotocol.AnnotationWorkerUsername]
}

func (c *Coordinator) validateManagedNode(node *corev1.Node) error {
	if node == nil || node.UID == "" || node.Name != c.NodeName {
		return fmt.Errorf("managed maintenance Node identity is incomplete")
	}
	expected := map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: c.Namespace,
		managedprotocol.AnnotationDeviceName:      c.DeviceName,
		managedprotocol.AnnotationDeviceUID:       c.DeviceUID,
		managedprotocol.AnnotationNodeUID:         string(node.UID),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
	}
	for key, value := range expected {
		if node.Annotations[key] != value {
			return fmt.Errorf("managed maintenance Node annotation %s=%q, want %q", key, node.Annotations[key], value)
		}
	}
	username := node.Annotations[managedprotocol.AnnotationWorkerUsername]
	prefix := "system:serviceaccount:" + c.Namespace + ":"
	if !strings.HasPrefix(username, prefix) || strings.TrimPrefix(username, prefix) == "" {
		return fmt.Errorf("managed maintenance Node has no valid worker binding")
	}
	return nil
}

func (c *Coordinator) validateManagedLeaseBinding(lease *coordv1.Lease, node *corev1.Node) error {
	if lease.UID == "" {
		return fmt.Errorf("managed mutation Lease has no persistent UID")
	}
	for annotation, expected := range map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: c.Namespace,
		managedprotocol.AnnotationDeviceName:      c.DeviceName,
		managedprotocol.AnnotationDeviceUID:       c.DeviceUID,
		managedprotocol.AnnotationNodeName:        node.Name,
		managedprotocol.AnnotationNodeUID:         string(node.UID),
		managedprotocol.AnnotationWorkerUsername:  node.Annotations[managedprotocol.AnnotationWorkerUsername],
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		managedprotocol.AnnotationLeasePurpose:    managedprotocol.LeasePurposeDeviceMutation,
		devicecoordination.RetainLeaseAnnotation:  "true",
	} {
		if lease.Annotations[annotation] != expected {
			return fmt.Errorf("managed mutation Lease binding %s does not match the runtime", annotation)
		}
	}
	deviceKey := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	if lease.Labels["cisco.vk/device"] != deviceKey ||
		lease.Labels["cisco.vk/family"] != devicecoordination.MutationLeaseFamily {
		return fmt.Errorf("managed mutation Lease labels do not match the runtime device")
	}
	return nil
}

// Managed health/soak settlement outlives the worker's disruptive Lease holder.
// Retain the write fence until the manager settles the exact bound session.
func (c *Coordinator) checkManagedWriteSession(ctx context.Context) error {
	if !c.ManagedTopology {
		return nil
	}
	if c.DeviceUID == "" || c.NodeName == "" {
		return fmt.Errorf("managed maintenance runtime identity is incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var device ciskov1.CiscoDevice
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: c.DeviceName}, &device); err != nil {
		return fmt.Errorf("read managed maintenance state: %w", err)
	}
	if !device.DeletionTimestamp.IsZero() {
		return fmt.Errorf("managed device writes are disabled while the CiscoDevice is terminating")
	}
	var node corev1.Node
	if err := c.Client.Get(ctx, types.NamespacedName{Name: c.NodeName}, &node); err != nil {
		return fmt.Errorf("read managed maintenance Node: %w", err)
	}
	if err := c.validateManagedNode(&node); err != nil {
		return err
	}
	if string(device.UID) != c.DeviceUID || device.Status.NodeIdentity == nil ||
		device.Status.NodeIdentity.DeviceUID != c.DeviceUID || device.Status.NodeIdentity.NodeName != c.NodeName ||
		device.Status.NodeIdentity.NodeUID != string(node.UID) {
		return fmt.Errorf("managed maintenance CiscoDevice/Node identity is not bound to this worker")
	}
	ready := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration < device.Generation ||
		device.Status.TopologyProjection == nil || device.Status.TopologyProjection.EffectiveLabelHash == "" ||
		device.Status.TopologyProjection.EffectiveLabelHash != node.Annotations[managedprotocol.AnnotationProjectionHash] {
		return fmt.Errorf("managed device topology is not ready or its projection is not bound to the Node")
	}
	for _, taint := range node.Spec.Taints {
		if taint.Key == managedprotocol.TopologyInitializingTaint && taint.Effect == corev1.TaintEffectNoSchedule {
			return fmt.Errorf("managed device topology initialization guard remains active")
		}
	}
	if session := device.Status.MaintenanceSession; session != nil {
		if session.Phase != ciskov1.DeviceMaintenanceSessionSettled || session.DeviceUID != c.DeviceUID ||
			session.NodeName != node.Name || session.NodeUID != string(node.UID) {
			return fmt.Errorf("managed maintenance session remains unresolved or belongs to a different device incarnation")
		}
	}
	var lease coordv1.Lease
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily)}, &lease); err != nil {
		return fmt.Errorf("read managed canonical mutation Lease: %w", err)
	}
	if err := c.validateManagedLeaseBinding(&lease, &node); err != nil {
		return err
	}
	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = strings.TrimSpace(*lease.Spec.HolderIdentity)
	}
	if holder == "" {
		if lease.Spec.LeaseDurationSeconds != nil || lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
			(lease.Spec.LeaseTransitions != nil && *lease.Spec.LeaseTransitions < 0) {
			return fmt.Errorf("managed canonical mutation Lease has malformed idle state")
		}
	} else if err := validateActiveManagedMutationLease(&lease, holder); err != nil {
		return err
	}
	if (holder == "" || strings.HasPrefix(holder, routineHolderPrefix)) &&
		hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
		return fmt.Errorf("idle or routine-write mutation Lease retains maintenance request metadata")
	}
	return nil
}

func hasManagedMaintenanceRequestAnnotations(annotations map[string]string) bool {
	for _, key := range []string{
		managedprotocol.AnnotationMaintenanceRequestVersion,
		managedprotocol.AnnotationMaintenanceSessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt,
		managedprotocol.AnnotationMaintenanceOperationNS,
		managedprotocol.AnnotationMaintenanceOperationName,
		managedprotocol.AnnotationMaintenanceOperationUID,
		managedprotocol.AnnotationMaintenanceControlRevision,
	} {
		if _, present := annotations[key]; present {
			return true
		}
	}
	return false
}

func validateMaintenanceAcknowledgement(
	session *ciskov1.DeviceMaintenanceSessionStatus,
	request publishedRequest,
	node *corev1.Node,
) error {
	if session == nil {
		return fmt.Errorf("waiting for manager maintenance acknowledgement")
	}
	if session.Phase != ciskov1.DeviceMaintenanceSessionAcknowledged &&
		session.Phase != ciskov1.DeviceMaintenanceSessionActive {
		return fmt.Errorf("manager maintenance session is %s, not acknowledged", session.Phase)
	}
	if session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() ||
		session.SessionToken != request.Token || session.ControlRevision != request.ControlRevision ||
		session.DeviceUID != node.Annotations[managedprotocol.AnnotationDeviceUID] ||
		session.NodeName != node.Name || session.NodeUID != string(node.UID) ||
		session.Lease.Namespace != request.LeaseNamespace || session.Lease.Name != request.LeaseName ||
		session.Lease.UID != request.LeaseUID || session.Lease.Holder != request.Holder ||
		session.Operation != request.Operation || !session.RequestedAt.Time.Equal(request.RequestedAt) {
		return fmt.Errorf("manager maintenance acknowledgement does not match the current Lease request")
	}
	return nil
}

func hasMaintenanceTaint(taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Key == TaintKey && taint.Value == TaintValue && taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
