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
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

const podNodeNameIndex = "spec.nodeName"

type managedWorkerPodIdentity struct {
	username string
	name     string
	uid      string
	revision string
}

func managedAppWorkerUsername(node *corev1.Node) string {
	if node == nil {
		return ""
	}
	if username := strings.TrimSpace(node.Annotations[managedprotocol.AnnotationAppWorkerUsername]); username != "" {
		return username
	}
	return strings.TrimSpace(node.Annotations[managedprotocol.AnnotationWorkerUsername])
}

func managedNetworkWorkerUsername(node *corev1.Node) string {
	if node == nil {
		return ""
	}
	if username := strings.TrimSpace(node.Annotations[managedprotocol.AnnotationNetworkWorkerUsername]); username != "" {
		return username
	}
	return strings.TrimSpace(node.Annotations[managedprotocol.AnnotationWorkerUsername])
}

func (i *managedWorkerPodIdentity) complete() bool {
	return i != nil && i.username != "" && i.name != "" && i.uid != ""
}

func workerPodIdentity(namespace, serviceAccount string, pod *corev1.Pod) *managedWorkerPodIdentity {
	if pod == nil || pod.Name == "" || pod.UID == "" || pod.Spec.ServiceAccountName != serviceAccount {
		return nil
	}
	return &managedWorkerPodIdentity{
		username: "system:serviceaccount:" + namespace + ":" + serviceAccount,
		name:     pod.Name,
		uid:      string(pod.UID),
		revision: pod.Annotations[managedprotocol.AnnotationWorkerConfigRevision],
	}
}

func workerIdentityAnnotations(app, network *managedWorkerPodIdentity) map[string]string {
	out := map[string]string{}
	if app != nil {
		out[managedprotocol.AnnotationWorkerUsername] = app.username
		out[managedprotocol.AnnotationAppWorkerUsername] = app.username
		if app.complete() {
			out[managedprotocol.AnnotationAppWorkerPodName] = app.name
			out[managedprotocol.AnnotationAppWorkerPodUID] = app.uid
		}
	}
	if network != nil {
		out[managedprotocol.AnnotationNetworkWorkerUsername] = network.username
		if network.complete() {
			out[managedprotocol.AnnotationNetworkWorkerPodName] = network.name
			out[managedprotocol.AnnotationNetworkWorkerPodUID] = network.uid
		}
	}
	return out
}

func copyManagedWorkerBindingAnnotations(destination, source map[string]string) {
	for _, key := range []string{
		managedprotocol.AnnotationAppWorkerUsername,
		managedprotocol.AnnotationAppWorkerPodName,
		managedprotocol.AnnotationAppWorkerPodUID,
		managedprotocol.AnnotationNetworkWorkerUsername,
		managedprotocol.AnnotationNetworkWorkerPodName,
		managedprotocol.AnnotationNetworkWorkerPodUID,
	} {
		if value := source[key]; value != "" {
			destination[key] = value
		}
	}
}

func applyWorkerIdentityAnnotations(annotations map[string]string, app, network *managedWorkerPodIdentity,
	clearApp, clearNetwork bool) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	if clearApp {
		delete(annotations, managedprotocol.AnnotationAppWorkerPodName)
		delete(annotations, managedprotocol.AnnotationAppWorkerPodUID)
	}
	if clearNetwork {
		delete(annotations, managedprotocol.AnnotationNetworkWorkerPodName)
		delete(annotations, managedprotocol.AnnotationNetworkWorkerPodUID)
	}
	for key, value := range workerIdentityAnnotations(app, network) {
		annotations[key] = value
	}
	return annotations
}

func podReadyTransition(pod *corev1.Pod) *metav1.Time {
	if pod == nil {
		return nil
	}
	for i := range pod.Status.Conditions {
		condition := &pod.Status.Conditions[i]
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			if !condition.LastTransitionTime.IsZero() {
				return condition.LastTransitionTime.DeepCopy()
			}
			if pod.Status.StartTime != nil {
				return pod.Status.StartTime.DeepCopy()
			}
		}
	}
	return nil
}

func soleCurrentWorkerPod(ctx context.Context, reader client.Reader, device *ciskov1.CiscoDevice,
	deployment *appsv1.Deployment, labels map[string]string, desiredRevision string) (*corev1.Pod, error) {
	if device == nil || deployment == nil || deployment.Name == "" {
		return nil, nil
	}
	var current appsv1.Deployment
	if err := reader.Get(ctx, client.ObjectKeyFromObject(deployment), &current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read worker Deployment: %w", err)
	}
	if current.UID == "" || !metav1.IsControlledBy(&current, device) {
		return nil, nil
	}
	if desiredRevision != "" && current.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != desiredRevision {
		return nil, nil
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(current.Namespace), client.MatchingLabels(labels)); err != nil {
		return nil, fmt.Errorf("list worker Pods: %w", err)
	}
	var currentPod *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		owned, err := podOwnedByDeployment(ctx, reader, pod, &current)
		if err != nil {
			return nil, err
		}
		if !owned {
			continue
		}
		if pod.DeletionTimestamp != nil {
			return nil, nil
		}
		// A bound-token caller cannot exist before its Pod is running. Requiring
		// Running (but deliberately not Ready) lets the runtime complete its
		// admission preflight without authorizing a merely created/unscheduled
		// identity.
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if currentPod != nil {
			return nil, nil
		}
		currentPod = pod
	}
	if currentPod == nil || currentPod.UID == "" ||
		(desiredRevision != "" && currentPod.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != desiredRevision) {
		return nil, nil
	}
	return currentPod.DeepCopy(), nil
}

func soleReadyWorkerPod(ctx context.Context, reader client.Reader, device *ciskov1.CiscoDevice,
	deployment *appsv1.Deployment, labels map[string]string, desiredRevision string) (*corev1.Pod, error) {
	pod, err := soleCurrentWorkerPod(ctx, reader, device, deployment, labels, desiredRevision)
	if err != nil || pod == nil {
		return pod, err
	}
	var current appsv1.Deployment
	if err := reader.Get(ctx, client.ObjectKeyFromObject(deployment), &current); err != nil {
		return nil, fmt.Errorf("read ready worker Deployment: %w", err)
	}
	if !deploymentRolloutComplete(&current) || pod.Status.StartTime == nil || !podConditionTrue(pod, corev1.PodReady) {
		return nil, nil
	}
	return pod, nil
}

func observeManagedNetworkWorkerRevision(ctx context.Context, reader client.Reader, now time.Time,
	device *ciskov1.CiscoDevice, deployment *appsv1.Deployment, desiredRevision string,
) (*ciskov1.DeviceNetworkWorkerRevisionStatus, bool, error) {
	if deployment == nil || desiredRevision == "" {
		return nil, false, nil
	}
	var current appsv1.Deployment
	if err := reader.Get(ctx, client.ObjectKeyFromObject(deployment), &current); err != nil {
		return nil, false, fmt.Errorf("read managed network worker Deployment revision: %w", err)
	}
	status := &ciskov1.DeviceNetworkWorkerRevisionStatus{
		DesiredRevision: desiredRevision,
		ObservedAt:      metav1.NewTime(now),
	}
	if current.UID == "" || current.Generation < 1 || !metav1.IsControlledBy(&current, device) ||
		current.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != desiredRevision {
		return status, false, nil
	}
	recomputed, err := managedWorkerPodTemplateRevision(&current.Spec.Template)
	if err != nil {
		return nil, false, err
	}
	if recomputed != desiredRevision {
		return status, false, nil
	}
	status.DeploymentUID = string(current.UID)
	status.DeploymentGeneration = current.Generation
	pod, err := soleReadyWorkerPod(ctx, reader, device, &current,
		perDeviceNetworkDeploymentLabels(device.Name), desiredRevision)
	if err != nil || pod == nil {
		return status, false, err
	}
	readyTime := podReadyTransition(pod)
	if readyTime == nil || readyTime.Before(pod.Status.StartTime) {
		return status, false, nil
	}
	status.ObservedRevision = desiredRevision
	status.PodUID = string(pod.UID)
	status.PodStartTime = pod.Status.StartTime.DeepCopy()
	status.PodReadyTime = readyTime
	return status, true, nil
}

func networkWorkerRevisionEvidenceEqual(a, b *ciskov1.DeviceNetworkWorkerRevisionStatus) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.DesiredRevision == b.DesiredRevision && a.ObservedRevision == b.ObservedRevision &&
		a.DeploymentUID == b.DeploymentUID && a.DeploymentGeneration == b.DeploymentGeneration &&
		a.PodUID == b.PodUID && timePointersEqual(a.PodStartTime, b.PodStartTime) &&
		timePointersEqual(a.PodReadyTime, b.PodReadyTime)
}

func (r *CiscoDeviceReconciler) updateManagedWorkerStatuses(ctx context.Context, device *ciskov1.CiscoDevice,
	appDeployment *appsv1.Deployment, appRevision string,
	networkDeployment *appsv1.Deployment, networkRevision string, configErr error) error {
	var appStatus *ciskov1.DeviceWorkerRevisionStatus
	appReady := false
	if appDeployment != nil && appRevision != "" {
		var err error
		appStatus, appReady, err = r.observeManagedWorkerRevision(ctx, device, appDeployment, appRevision)
		if err != nil {
			return err
		}
	}
	var networkStatus *ciskov1.DeviceNetworkWorkerRevisionStatus
	networkReady := false
	if networkDeployment != nil && networkRevision != "" {
		var err error
		networkStatus, networkReady, err = observeManagedNetworkWorkerRevision(
			ctx, r.reader(), r.now(), device, networkDeployment, networkRevision)
		if err != nil {
			return err
		}
	}

	var appPod, networkPod *corev1.Pod
	var err error
	if appDeployment != nil {
		appPod, err = soleCurrentWorkerPod(ctx, r.reader(), device, appDeployment,
			perDeviceDeploymentLabels(device.Name), appRevision)
		if err != nil {
			return err
		}
	}
	if networkDeployment != nil {
		networkPod, err = soleCurrentWorkerPod(ctx, r.reader(), device, networkDeployment,
			perDeviceNetworkDeploymentLabels(device.Name), networkRevision)
		if err != nil {
			return err
		}
	}
	appIdentity := workerPodIdentity(device.Namespace, r.appHostingServiceAccountName(), appPod)
	networkIdentity := workerPodIdentity(device.Namespace, r.networkManagementServiceAccountName(), networkPod)
	clearAppBinding := appDeployment == nil || appIdentity == nil
	clearNetworkBinding := networkDeployment == nil || networkIdentity == nil
	if err := r.reconcileManagedWorkerObjectBindings(ctx, device, appIdentity, networkIdentity,
		clearAppBinding, clearNetworkBinding); err != nil {
		return err
	}

	condition := metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Validated",
		Message:            "Referenced gNOI Secret material is valid and the exact network worker is ready; device connectivity has not been checked",
		ObservedGeneration: device.Generation,
	}
	configured := gnoiTLSSecretRef(&device.Spec) != nil || xeGNOICertificateProvisioning(&device.Spec) != nil
	_, appAccess, _, networkAccess, _, profileErr := r.managedWorkerProfiles()
	switch {
	case profileErr != nil:
		return profileErr
	case configErr != nil:
		condition.Status, condition.Reason = metav1.ConditionFalse, "InvalidSecret"
		condition.Message = "Worker gNOI is disabled until its Secret is repaired: " + configErr.Error()
	case appAccess != managedprotocol.WorkerAccessReadWrite:
		condition.Status, condition.Reason = metav1.ConditionFalse, "AppHostingUnavailable"
		condition.Message = "app-hosting read-write worker is required to maintain the managed Node health used by software rollout"
	case !appReady || !appIdentity.complete():
		condition.Status, condition.Reason = metav1.ConditionFalse, "AppWorkerRolloutPending"
		condition.Message = "software mutation is waiting for the exact app-hosting worker revision and bound Pod identity"
	case networkAccess == managedprotocol.WorkerAccessDisabled:
		condition.Status, condition.Reason = metav1.ConditionFalse, "NetworkManagementDisabled"
		condition.Message = "network-management worker access is disabled"
	case networkAccess != managedprotocol.WorkerAccessReadWrite:
		condition.Status, condition.Reason = metav1.ConditionFalse, "NetworkManagementReadOnly"
		condition.Message = "network-management worker is read-only; gNOI software mutation is unavailable"
	case gNOIDisabled() || !configured:
		condition.Status, condition.Reason = metav1.ConditionFalse, "NotConfigured"
		condition.Message = "Referenced gNOI Secret validation is inactive"
	case !networkReady || !networkIdentity.complete():
		condition.Status, condition.Reason = metav1.ConditionFalse, "NetworkWorkerRolloutPending"
		condition.Message = "validated gNOI configuration is waiting for the exact network worker revision and bound Pod identity"
	}

	before := device.DeepCopy()
	if appStatus != nil && workerRevisionEvidenceEqual(device.Status.WorkerRevision, appStatus) {
		appStatus.ObservedAt = device.Status.WorkerRevision.ObservedAt
	}
	if networkStatus != nil && networkWorkerRevisionEvidenceEqual(device.Status.NetworkWorkerRevision, networkStatus) {
		networkStatus.ObservedAt = device.Status.NetworkWorkerRevision.ObservedAt
	}
	device.Status.WorkerRevision = appStatus
	device.Status.NetworkWorkerRevision = networkStatus
	if err := r.applyCiscoDeviceConditionObserved(device, condition); err != nil {
		return err
	}
	if statusesEqual(before.Status, device.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("update managed functional worker status: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) reconcileManagedWorkerObjectBindings(ctx context.Context,
	device *ciskov1.CiscoDevice, app, network *managedWorkerPodIdentity, clearApp, clearNetwork bool) error {
	if device.Status.NodeIdentity == nil {
		return nil
	}
	var node corev1.Node
	key := types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}
	if err := r.reader().Get(ctx, key, &node); err != nil {
		return fmt.Errorf("read managed Node for worker Pod binding: %w", err)
	}
	if string(node.UID) != device.Status.NodeIdentity.NodeUID || !managedNodeMatchesDevice(&node, device) {
		return fmt.Errorf("managed Node identity changed before worker Pod binding")
	}
	before := node.DeepCopy()
	node.Annotations = applyWorkerIdentityAnnotations(node.Annotations, app, network, clearApp, clearNetwork)
	if !reflect.DeepEqual(before.Annotations, node.Annotations) {
		if err := r.Patch(ctx, &node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("stamp managed Node worker Pod identities: %w", err)
		}
	}
	if err := r.stampManagedLeases(ctx, device, &node); err != nil {
		return err
	}
	if err := r.stampManagedUpgradeLeaves(ctx, device, app, network, clearApp, clearNetwork); err != nil {
		return err
	}
	if err := r.stampManagedNetworkObjects(ctx, device, network, clearNetwork); err != nil {
		return err
	}
	if (app != nil && app.complete()) || clearApp {
		if err := r.stampManagedWorkloadPods(ctx, device, &node, app, clearApp); err != nil {
			return err
		}
	}
	return nil
}

func managedNetworkObjectDeviceName(object client.Object) string {
	switch typed := object.(type) {
	case *configv1alpha1.IOSXEConfig:
		return typed.Spec.DeviceRef.Name
	case *configv1alpha1.NXOSConfig:
		return typed.Spec.DeviceRef.Name
	case *configv1alpha1.IOSXETelemetry:
		return typed.Spec.DeviceRef.Name
	case *configv1alpha1.IOSXEDiagnostic:
		return typed.Spec.DeviceRef.Name
	case *configv1alpha1.IOSXEConfigApplyLog:
		return typed.Spec.DeviceRef.Name
	case *configv1alpha1.IOSXEConfigRevision:
		return typed.Spec.DeviceRef.Name
	case *opsv1alpha1.DeviceOperation:
		return typed.Spec.DeviceRef.Name
	case *opsv1alpha1.IOSXEOperationalAction:
		return typed.Spec.DeviceRef.Name
	default:
		return ""
	}
}

// stampManagedNetworkObjects binds every mutable network-management object to
// one CiscoDevice incarnation and the one currently running Pod. The manager
// owns this envelope; workers only copy it to their own child objects.
func (r *CiscoDeviceReconciler) stampManagedNetworkObjects(ctx context.Context,
	device *ciskov1.CiscoDevice, network *managedWorkerPodIdentity, clearNetwork bool) error {
	lists := []client.ObjectList{
		&configv1alpha1.IOSXEConfigList{},
		&configv1alpha1.NXOSConfigList{},
		&configv1alpha1.IOSXETelemetryList{},
		&configv1alpha1.IOSXEDiagnosticList{},
		&configv1alpha1.IOSXEConfigApplyLogList{},
		&configv1alpha1.IOSXEConfigRevisionList{},
		&opsv1alpha1.DeviceOperationList{},
		&opsv1alpha1.IOSXEOperationalActionList{},
	}
	expectedUsername := "system:serviceaccount:" + device.Namespace + ":" + r.networkManagementServiceAccountName()
	for _, list := range lists {
		if err := r.reader().List(ctx, list, client.InNamespace(device.Namespace)); err != nil {
			return fmt.Errorf("list managed network objects for worker Pod binding: %w", err)
		}
		if err := meta.EachListItem(list, func(item runtime.Object) error {
			object, ok := item.(client.Object)
			if !ok || managedNetworkObjectDeviceName(object) != device.Name {
				return nil
			}
			before := object.DeepCopyObject().(client.Object)
			annotations := object.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[managedprotocol.AnnotationManaged] = "true"
			annotations[managedprotocol.AnnotationDeviceNamespace] = device.Namespace
			annotations[managedprotocol.AnnotationDeviceName] = device.Name
			annotations[managedprotocol.AnnotationDeviceUID] = string(device.UID)
			annotations[managedprotocol.AnnotationNetworkWorkerUsername] = expectedUsername
			annotations = applyWorkerIdentityAnnotations(annotations, nil, network, false, clearNetwork)
			object.SetAnnotations(annotations)
			if reflect.DeepEqual(before.GetAnnotations(), annotations) {
				return nil
			}
			if err := r.Patch(ctx, object,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("stamp %T %s/%s network worker identity: %w",
					object, object.GetNamespace(), object.GetName(), err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) stampManagedUpgradeLeaves(ctx context.Context, device *ciskov1.CiscoDevice,
	app, network *managedWorkerPodIdentity, clearApp, clearNetwork bool) error {
	var leaves opsv1alpha1.IOSXESoftwareUpgradeList
	if err := r.reader().List(ctx, &leaves, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list managed software-upgrade leaves for worker Pod binding: %w", err)
	}
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		if leaf.Annotations[managedprotocol.AnnotationManaged] != "true" ||
			leaf.Annotations[managedprotocol.AnnotationDeviceName] != device.Name ||
			leaf.Annotations[managedprotocol.AnnotationDeviceUID] != string(device.UID) {
			continue
		}
		before := leaf.DeepCopy()
		primaryUsername := leaf.Annotations[managedprotocol.AnnotationWorkerUsername]
		leaf.Annotations = applyWorkerIdentityAnnotations(leaf.Annotations, app, network, clearApp, clearNetwork)
		leaf.Annotations[managedprotocol.AnnotationWorkerUsername] = primaryUsername
		if app.complete() && app.revision != "" {
			leaf.Annotations[managedprotocol.AnnotationAppWorkerConfigRevision] = app.revision
		} else if clearApp {
			delete(leaf.Annotations, managedprotocol.AnnotationAppWorkerConfigRevision)
		}
		if network != nil {
			leaf.Annotations[managedprotocol.AnnotationWorkerUsername] = network.username
		}
		if reflect.DeepEqual(before.Annotations, leaf.Annotations) {
			continue
		}
		if err := r.Patch(ctx, leaf, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("stamp software-upgrade leaf %s/%s network worker identity: %w", leaf.Namespace, leaf.Name, err)
		}
	}
	return nil
}

// stampManagedLeases repairs only existing canonical Leases. In particular,
// deletion must not recreate Leases that teardown has already removed.
func (r *CiscoDeviceReconciler) stampManagedLeases(ctx context.Context, device *ciskov1.CiscoDevice, node *corev1.Node) error {
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	var leases coordv1.LeaseList
	if err := r.reader().List(ctx, &leases, client.MatchingLabels{"cisco.vk/device": deviceKey}); err != nil {
		return err
	}
	for i := range leases.Items {
		lease := &leases.Items[i]
		if lease.Annotations[managedprotocol.AnnotationManaged] != "true" ||
			lease.Annotations[managedprotocol.AnnotationDeviceUID] != string(device.UID) ||
			lease.Annotations[managedprotocol.AnnotationNodeUID] != string(node.UID) {
			continue
		}
		purpose := lease.Annotations[managedprotocol.AnnotationLeasePurpose]
		family := lease.Labels["cisco.vk/family"]
		username := managedNetworkWorkerUsername(node)
		namespace := r.LeaseNamespace
		if namespace == "" {
			namespace = device.Namespace
		}
		name := configengine.LeaseName(deviceKey, family)
		var owners []metav1.OwnerReference
		switch purpose {
		case managedprotocol.LeasePurposeNodeHeartbeat:
			namespace, name, family = corev1.NamespaceNodeLease, node.Name, managedNodeHeartbeatFamily
			username = managedAppWorkerUsername(node)
			owners = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID}}
		case managedprotocol.LeasePurposeDeviceMutation:
			family = devicecoordination.MutationLeaseFamily
			name = configengine.LeaseName(deviceKey, family)
		case managedprotocol.LeasePurposeConfigFamily:
			if family == "" || family == managedNodeHeartbeatFamily || family == devicecoordination.MutationLeaseFamily {
				return fmt.Errorf("invalid managed config Lease family %q", family)
			}
		default:
			return fmt.Errorf("unknown managed Lease purpose %q", purpose)
		}
		if lease.Namespace != namespace || lease.Name != name {
			return fmt.Errorf("managed Lease %s/%s does not match canonical purpose identity", lease.Namespace, lease.Name)
		}
		annotations := managedLeaseBindingAnnotations(device, node.Name, string(node.UID), username, purpose)
		copyManagedWorkerBindingAnnotations(annotations, node.Annotations)
		if err := r.repairManagedLeaseBindings(ctx, lease, annotations, managedLeaseLabels(deviceKey, family), owners); err != nil {
			return fmt.Errorf("repair managed Lease %s/%s: %w", lease.Namespace, lease.Name, err)
		}
	}
	return nil
}

// repairManagedWorkerBindings runs before maintenance validation. A pending leaf
// or a restarted worker must not prevent the manager from repairing the exact
// Pod binding that maintenance validation itself requires.
func (r *CiscoDeviceReconciler) repairManagedWorkerBindings(ctx context.Context, device *ciskov1.CiscoDevice) error {
	app, err := r.currentManagedWorkerIdentity(ctx, device, device.Name+deploymentSuffix,
		r.appHostingServiceAccountName(), perDeviceDeploymentLabels(device.Name))
	if err != nil {
		return err
	}
	network, err := r.currentManagedWorkerIdentity(ctx, device, networkDeploymentName(string(device.UID)),
		r.networkManagementServiceAccountName(), perDeviceNetworkDeploymentLabels(device.Name))
	if err != nil {
		return err
	}
	return r.reconcileManagedWorkerObjectBindings(ctx, device, app, network, app == nil, network == nil)
}

func (r *CiscoDeviceReconciler) currentManagedWorkerIdentity(ctx context.Context, device *ciskov1.CiscoDevice,
	name, serviceAccount string, labels map[string]string) (*managedWorkerPodIdentity, error) {
	var deployment appsv1.Deployment
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: name}, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	revision := deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision]
	if revision == "" || !metav1.IsControlledBy(&deployment, device) ||
		deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount {
		return nil, nil
	}
	computed, err := managedWorkerPodTemplateRevision(&deployment.Spec.Template)
	if err != nil {
		return nil, err
	}
	if computed != revision {
		return nil, nil
	}
	pod, err := soleCurrentWorkerPod(ctx, r.reader(), device, &deployment, labels, revision)
	if err != nil {
		return nil, err
	}
	return workerPodIdentity(device.Namespace, serviceAccount, pod), nil
}

func (r *CiscoDeviceReconciler) stampManagedWorkloadPods(ctx context.Context, device *ciskov1.CiscoDevice,
	node *corev1.Node, app *managedWorkerPodIdentity, clearApp bool) error {
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.MatchingFields{podNodeNameIndex: node.Name}); err != nil {
		return fmt.Errorf("list workloads on managed Node %q: %w", node.Name, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != node.Name {
			continue
		}
		before := pod.DeepCopy()
		if clearApp {
			// Unlike Node/Lease bootstrap identities, workload bindings are
			// either complete or absent. A username-only remnant is rejected
			// by admission and would prevent rolling a worker with live Pods.
			delete(pod.Annotations, managedprotocol.AnnotationAppWorkerUsername)
			delete(pod.Annotations, managedprotocol.AnnotationAppWorkerPodName)
			delete(pod.Annotations, managedprotocol.AnnotationAppWorkerPodUID)
		} else if app != nil && app.complete() {
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Annotations[managedprotocol.AnnotationAppWorkerUsername] = app.username
			pod.Annotations[managedprotocol.AnnotationAppWorkerPodName] = app.name
			pod.Annotations[managedprotocol.AnnotationAppWorkerPodUID] = app.uid
		}
		if reflect.DeepEqual(before.Annotations, pod.Annotations) {
			continue
		}
		if err := r.Patch(ctx, pod, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("stamp workload Pod %s/%s app worker identity: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) mapManagedPodToCiscoDevice(ctx context.Context, obj client.Object) []ctrl.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	if pod.Labels["app.kubernetes.io/managed-by"] == "ciscodevice-controller" {
		name := strings.TrimSpace(pod.Labels["app.kubernetes.io/instance"])
		if name != "" {
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: pod.Namespace, Name: name}}}
		}
	}
	if pod.Spec.NodeName == "" {
		return nil
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, &node); err != nil ||
		node.Annotations[managedprotocol.AnnotationManaged] != "true" {
		return nil
	}
	namespace := node.Annotations[managedprotocol.AnnotationDeviceNamespace]
	name := node.Annotations[managedprotocol.AnnotationDeviceName]
	if namespace == "" || name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
}
