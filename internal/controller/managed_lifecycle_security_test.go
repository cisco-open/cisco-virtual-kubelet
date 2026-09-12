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
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestManagedWorkerUsesOwnedIncarnationServiceAccount(t *testing.T) {
	device := newDevice("switch-owned-worker", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	saName := managedWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(context.Background(), device, saName, true); err != nil {
		t.Fatalf("ensureVKAccess: %v", err)
	}

	var sa corev1.ServiceAccount
	key := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.Get(context.Background(), key, &sa); err != nil {
		t.Fatal(err)
	}
	if !managedServiceAccountOwnedByDevice(&sa, device) || !managedServiceAccountAnnotationsMatch(&sa, device) {
		t.Fatalf("managed ServiceAccount is not exactly device-bound: %#v", sa.ObjectMeta)
	}
	if got := r.serviceAccountForDevice(device); got != saName {
		t.Fatalf("durable managed ServiceAccount=%q, want %q", got, saName)
	}
}

func TestTopologyLegacyWorkerUsesOwnedIncarnationServiceAccount(t *testing.T) {
	device := newDevice("switch-legacy-worker", "edge")
	device.UID = "legacy-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	saName := topologyLegacyWorkerServiceAccountName(device)
	if got := r.serviceAccountForDevice(device); got != saName {
		t.Fatalf("topology legacy ServiceAccount=%q, want %q", got, saName)
	}
	if err := r.ensureVKAccess(context.Background(), device, saName, false); err != nil {
		t.Fatalf("ensureVKAccess: %v", err)
	}

	var sa corev1.ServiceAccount
	key := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.Get(context.Background(), key, &sa); err != nil {
		t.Fatal(err)
	}
	wantAnnotations := workerServiceAccountAnnotations(device, false)
	if !managedServiceAccountOwnedByDevice(&sa, device) || !workerAnnotationsMatch(sa.Annotations, wantAnnotations) {
		t.Fatalf("topology legacy ServiceAccount is not exactly device-bound: %#v", sa.ObjectMeta)
	}
	if sa.Annotations[managedprotocol.AnnotationManaged] != "" ||
		sa.Annotations[managedprotocol.AnnotationWorkerMode] != managedprotocol.WorkerModeLegacy {
		t.Fatalf("legacy worker mode annotations=%v", sa.Annotations)
	}
	var crb rbacv1.ClusterRoleBinding
	if err := r.Get(context.Background(), types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}, &crb); err != nil {
		t.Fatal(err)
	}
	if crb.RoleRef.Name != vkSharedClusterRole {
		t.Fatalf("legacy worker role=%q, want %q", crb.RoleRef.Name, vkSharedClusterRole)
	}
}

func TestGeneratedWorkerRejectsEveryAdditiveBinding(t *testing.T) {
	for _, clusterScoped := range []bool{false, true} {
		name := "rolebinding"
		if clusterScoped {
			name = "clusterrolebinding"
		}
		t.Run(name, func(t *testing.T) {
			device := newDevice("switch-additive-"+name, "edge")
			device.UID = types.UID("device-uid-" + name)
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			}
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			saName := managedWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(context.Background(), device, saName, true); err != nil {
				t.Fatal(err)
			}
			subjects := exactWorkerSubject(device.Namespace, saName)
			if clusterScoped {
				extra := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "unexpected-broad-grant"},
					RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole}, Subjects: subjects}
				if err := r.Create(context.Background(), extra); err != nil {
					t.Fatal(err)
				}
			} else {
				extra := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "unexpected-grant"},
					RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}, Subjects: subjects}
				if err := r.Create(context.Background(), extra); err != nil {
					t.Fatal(err)
				}
			}
			err := r.ensureVKAccess(context.Background(), device, saName, true)
			if err == nil || !strings.Contains(err.Error(), "unexpected additive") {
				t.Fatalf("additive binding audit error=%v", err)
			}
		})
	}
}

func TestGeneratedWorkerCleanupPreservesForeignBindingCollision(t *testing.T) {
	device := newDevice("switch-binding-collision", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	saName := managedWorkerServiceAccountName(device)
	ctx := context.Background()
	if err := r.ensureVKAccess(ctx, device, saName, true); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}
	var binding rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, key, &binding); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	foreign := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: key.Name, UID: "foreign-binding-uid"},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects: exactWorkerSubject(device.Namespace, saName)}
	if err := r.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	err := r.cleanupVKClusterAccess(ctx, device, saName)
	if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("cleanup collision error=%v", err)
	}
	var remaining rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, key, &remaining); err != nil {
		t.Fatalf("foreign binding was deleted: %v", err)
	}
}

func TestManagedWorkerRefusesForeignServiceAccountCollision(t *testing.T) {
	device := newDevice("switch-collision", "edge")
	device.UID = "device-uid"
	saName := managedWorkerServiceAccountName(device)
	foreign := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: saName, UID: "foreign-sa-uid",
	}}
	r := reconcilerFor(t, device, foreign)
	err := r.ensureVKAccess(context.Background(), device, saName, true)
	if err == nil || !strings.Contains(err.Error(), "not controlled") {
		t.Fatalf("foreign ServiceAccount collision error=%v", err)
	}
	var binding rbacv1.RoleBinding
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: saName}, &binding); err == nil {
		t.Fatal("foreign ServiceAccount collision unexpectedly created a RoleBinding")
	}
}

func TestManagedWorkerServiceAccountCleanupIsUIDAndOwnerBound(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "owned"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			device := newDevice("switch-cleanup-"+name, "edge")
			device.UID = types.UID("device-uid-" + name)
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			}
			saName := managedWorkerServiceAccountName(device)
			var objects []runtime.Object
			if foreign {
				objects = append(objects, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
					Namespace: device.Namespace, Name: saName, UID: "foreign-sa-uid",
				}})
			}
			r := reconcilerFor(t, append([]runtime.Object{device}, objects...)...)
			ctx := context.Background()
			if !foreign {
				if err := r.ensureVKAccess(ctx, device, saName, true); err != nil {
					t.Fatal(err)
				}
				var sa corev1.ServiceAccount
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &sa); err != nil {
					t.Fatal(err)
				}
				// A real API server assigns this immutable identity. fake.Client does
				// not, so seed it to exercise the deletion precondition.
				sa.UID = "owned-sa-uid"
				if err := r.Update(ctx, &sa); err != nil {
					t.Fatal(err)
				}
			}
			err := r.cleanupVKClusterAccess(ctx, device, saName)
			if foreign {
				if err == nil || !strings.Contains(err.Error(), "not exactly owned") {
					t.Fatalf("foreign cleanup error=%v, want fail-closed ownership error", err)
				}
			} else if err != nil {
				t.Fatalf("cleanupVKClusterAccess: %v", err)
			}
			var remaining corev1.ServiceAccount
			getErr := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &remaining)
			if foreign && getErr != nil {
				t.Fatalf("foreign ServiceAccount was removed: %v", getErr)
			}
			if !foreign && !apierrors.IsNotFound(getErr) {
				t.Fatalf("owned ServiceAccount remains or lookup failed: %v", getErr)
			}
		})
	}
}

func TestManagedReconcileAlwaysUsesRecreateStrategy(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	device := newDevice("switch-managed-strategy", "edge")
	device.UID = "device-uid"
	device.Spec.PhysicalIdentity = "serial-switch-managed-strategy"
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        device.Name,
			managedprotocol.AnnotationNodeUID:         "node-uid",
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		},
	}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	policy, ledger := managedPolicyAndLedger(t, nil)

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithObjects(device, node, policy, ledger).Build()
	r := &CiscoDeviceReconciler{
		Client: leaseUIDAssigningClient{Client: apiClient}, APIReader: apiClient, Scheme: scheme, Image: "cisco-vk:test",
		ManagedTopology: true, TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace,
	}
	if _, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var deployment appsv1.Deployment
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("managed Deployment strategy=%q, want Recreate", deployment.Spec.Strategy.Type)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	var sa corev1.ServiceAccount
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: managedWorkerServiceAccountName(&current)}, &sa); err != nil {
		t.Fatal(err)
	}
	if !managedServiceAccountOwnedByDevice(&sa, &current) {
		t.Fatal("managed reconcile did not bind the worker ServiceAccount to the CiscoDevice incarnation")
	}
}

func TestManagedWorkerLeasesArePrecreatedAndExactlyBound(t *testing.T) {
	device := newDevice("switch-leases", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: "shared-leases"}
	ctx := context.Background()
	if err := r.ensureManagedMutationLease(ctx, device, node); err != nil {
		t.Fatalf("ensure mutation Lease: %v", err)
	}
	if err := r.ensureManagedNodeHeartbeatLease(ctx, device, node); err != nil {
		t.Fatalf("ensure heartbeat Lease: %v", err)
	}
	if err := r.ensureManagedConfigLease(ctx, device, node, "interface_ethernet"); err != nil {
		t.Fatalf("ensure config Lease: %v", err)
	}

	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	checks := []struct {
		key     types.NamespacedName
		family  string
		purpose string
		owners  int
	}{
		{types.NamespacedName{Namespace: "shared-leases", Name: configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)}, devicecoordination.MutationLeaseFamily, managedprotocol.LeasePurposeDeviceMutation, 0},
		{types.NamespacedName{Namespace: corev1.NamespaceNodeLease, Name: node.Name}, managedNodeHeartbeatFamily, managedprotocol.LeasePurposeNodeHeartbeat, 1},
		{types.NamespacedName{Namespace: "shared-leases", Name: configengine.LeaseName(deviceKey, "interface_ethernet")}, "interface_ethernet", managedprotocol.LeasePurposeConfigFamily, 0},
	}
	for _, check := range checks {
		var lease coordv1.Lease
		if err := base.Get(ctx, check.key, &lease); err != nil {
			t.Fatalf("get %s Lease: %v", check.purpose, err)
		}
		if lease.UID == "" || lease.Annotations[managedprotocol.AnnotationLeasePurpose] != check.purpose ||
			lease.Labels["cisco.vk/device"] != deviceKey || lease.Labels["cisco.vk/family"] != check.family ||
			lease.Annotations[managedprotocol.AnnotationWorkerUsername] != node.Annotations[managedprotocol.AnnotationWorkerUsername] ||
			len(lease.OwnerReferences) != check.owners {
			t.Fatalf("%s Lease is not exactly bound: %#v", check.purpose, lease.ObjectMeta)
		}
	}
}

func TestManagedHeartbeatAdoptsEmptyLegacyLeaseWithCanonicalIdentity(t *testing.T) {
	device := newDevice("switch-empty-heartbeat", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	legacy := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: corev1.NamespaceNodeLease, Name: node.Name, UID: "legacy-heartbeat-uid",
	}}
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node, legacy).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	if err := r.ensureManagedNodeHeartbeatLease(context.Background(), device, node); err != nil {
		t.Fatalf("adopt empty heartbeat: %v", err)
	}
	var adopted coordv1.Lease
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(legacy), &adopted); err != nil {
		t.Fatal(err)
	}
	if adopted.Spec.HolderIdentity == nil || *adopted.Spec.HolderIdentity != node.Name ||
		adopted.Spec.LeaseDurationSeconds == nil || *adopted.Spec.LeaseDurationSeconds != managedNodeLeaseDuration ||
		adopted.Spec.RenewTime != nil || adopted.Spec.AcquireTime != nil || adopted.Spec.LeaseTransitions != nil {
		t.Fatalf("adopted heartbeat spec=%#v, want canonical holder/duration without a manager-authored heartbeat", adopted.Spec)
	}
	if len(adopted.OwnerReferences) != 1 || adopted.OwnerReferences[0].UID != node.UID ||
		adopted.Annotations[managedprotocol.AnnotationLeasePurpose] != managedprotocol.LeasePurposeNodeHeartbeat {
		t.Fatalf("adopted heartbeat identity=%#v", adopted.ObjectMeta)
	}
}

func TestManagedConfigLeaseRefusesForeignCanonicalCollision(t *testing.T) {
	device := newDevice("switch-lease-collision", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	foreign := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, "vlan"), UID: "foreign-uid",
		Labels: map[string]string{"cisco.vk/device": "other-device", "cisco.vk/family": "vlan"},
	}}
	r := reconcilerFor(t, device, node, foreign)
	if err := r.ensureManagedConfigLease(context.Background(), device, node, "vlan"); err == nil ||
		!strings.Contains(err.Error(), "already bound") {
		t.Fatalf("foreign canonical Lease collision error = %v", err)
	}
}

type leaseUIDAssigningClient struct {
	client.Client
}

func (c leaseUIDAssigningClient) Create(ctx context.Context, object client.Object, opts ...client.CreateOption) error {
	if lease, ok := object.(*coordv1.Lease); ok && lease.UID == "" {
		lease.UID = "server-assigned-lease-uid"
	}
	return c.Client.Create(ctx, object, opts...)
}

func TestManagedDeviceDeletionFencesEveryUnresolvedAuthority(t *testing.T) {
	tests := map[string]func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger){
		"idle": func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger) {
		},
		"active session": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-session-token"}
		},
		"legacy handoff preparing": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffPreparing}
		},
		"legacy writer pending": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffLegacyWriterPending}
		},
		"held lease": func(_ *ciskov1.CiscoDevice, lease *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			lease.Spec.HolderIdentity = ptr.To("software-upgrade/edge/upgrade/leaf-uid")
		},
		"orphan request": func(_ *ciskov1.CiscoDevice, lease *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "orphan-request-token"
		},
		"unsettled leaf": func(_ *ciskov1.CiscoDevice, _ *coordv1.Lease, leaf *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
		},
		"ledger reservation": func(_ *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, ledger *topologyrollout.Ledger) {
			ledger.Reservations["reservation-1"] = validDeletionReservation()
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r, device := managedDeletionFixture(t, mutate)
			err := r.ensureManagedDeviceDeletionSafe(context.Background(), device)
			if name == "idle" && err != nil {
				t.Fatalf("idle deletion blocked: %v", err)
			}
			if name != "idle" && err == nil {
				t.Fatal("unresolved managed authority did not block deletion")
			}
		})
	}
}

func TestManagedDeletionMissingMutationLeaseRequiresRevokedWorkerAccess(t *testing.T) {
	for _, bindingKind := range []string{"none", "RoleBinding", "ClusterRoleBinding"} {
		t.Run(bindingKind, func(t *testing.T) {
			r, device := managedDeletionFixture(t, func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger) {
			})
			ctx := context.Background()
			deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
			var lease coordv1.Lease
			leaseKey := types.NamespacedName{
				Namespace: device.Namespace,
				Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
			}
			if err := r.Get(ctx, leaseKey, &lease); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(ctx, &lease); err != nil {
				t.Fatal(err)
			}
			saName := managedWorkerServiceAccountName(device)
			switch bindingKind {
			case "RoleBinding":
				if err := r.Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
					Namespace: device.Namespace, Name: saName,
				}}); err != nil {
					t.Fatal(err)
				}
			case "ClusterRoleBinding":
				if err := r.Create(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
					Name: vkAccessClusterRoleBindingName(device.Namespace, saName),
				}}); err != nil {
					t.Fatal(err)
				}
			}

			err := r.ensureManagedDeviceDeletionSafe(ctx, device)
			if bindingKind == "none" && err != nil {
				t.Fatalf("idempotent retry after access revocation was blocked: %v", err)
			}
			if bindingKind != "none" && (err == nil || !strings.Contains(err.Error(), "requires its canonical mutation Lease")) {
				t.Fatalf("missing Lease with live %s was not blocked: %v", bindingKind, err)
			}
		})
	}
}

func TestManagedLeaseCleanupValidatesCompleteSetBeforeDeleting(t *testing.T) {
	device := newDevice("switch-cleanup-leases", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: device.Namespace}
	ctx := context.Background()
	for _, family := range []string{"vlan", "interface_ethernet"} {
		if err := r.ensureManagedConfigLease(ctx, device, node, family); err != nil {
			t.Fatalf("ensure %s Lease: %v", family, err)
		}
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	badKey := types.NamespacedName{Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, "interface_ethernet")}
	var malformed coordv1.Lease
	if err := base.Get(ctx, badKey, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Secret", Name: "foreign", UID: "foreign-uid"}}
	if err := base.Update(ctx, &malformed); err != nil {
		t.Fatal(err)
	}

	if err := r.cleanupManagedWorkerLeases(ctx, device); err == nil {
		t.Fatal("malformed managed Lease did not block cleanup")
	}
	for _, family := range []string{"vlan", "interface_ethernet"} {
		key := types.NamespacedName{Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, family)}
		if err := base.Get(ctx, key, &coordv1.Lease{}); err != nil {
			t.Fatalf("Lease %s was deleted before complete-set validation: %v", family, err)
		}
	}
}

func TestManagedLeaseCleanupRetriesAfterBoundNodeDeletion(t *testing.T) {
	device := newDevice("switch-cleanup-node-gone", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: device.Namespace}
	ctx := context.Background()
	if err := r.ensureManagedConfigLease(ctx, device, node, "vlan"); err != nil {
		t.Fatal(err)
	}
	if err := base.Delete(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupManagedWorkerLeases(ctx, device); err != nil {
		t.Fatalf("cleanup after bound Node deletion: %v", err)
	}
	key := types.NamespacedName{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), "vlan"),
	}
	if err := base.Get(ctx, key, &coordv1.Lease{}); !apierrors.IsNotFound(err) {
		t.Fatalf("managed Lease remains after retry cleanup: %v", err)
	}
}

func managedDeletionFixture(
	t *testing.T,
	mutate func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger),
) (*CiscoDeviceReconciler, *ciskov1.CiscoDevice) {
	t.Helper()
	device := newDevice("switch-delete", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid"}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
		UID:       "lease-uid",
		Labels: map[string]string{
			"cisco.vk/device": deviceKey,
			"cisco.vk/family": devicecoordination.MutationLeaseFamily,
		},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        device.Name,
			managedprotocol.AnnotationNodeUID:         "node-uid",
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
			managedprotocol.AnnotationLeasePurpose:    managedprotocol.LeasePurposeDeviceMutation,
			devicecoordination.RetainLeaseAnnotation:  "true",
		},
	}}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "settled-upgrade", UID: "leaf-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:       "true",
			managedprotocol.AnnotationDeviceUID:     string(device.UID),
			managedprotocol.AnnotationReservationID: "settled-reservation",
		},
	}}
	leaf.Spec.DeviceRef.Name = device.Name
	leaf.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
		State: opsv1alpha1.UpgradeManagerAdmissionSettled, DeviceUID: string(device.UID),
	}
	policy, ledgerCM := managedPolicyAndLedger(t, nil)
	ledger, err := topologyrollout.Decode([]byte(ledgerCM.Data[topologyrollout.LedgerDataKey]), string(ledgerCM.UID))
	if err != nil {
		t.Fatal(err)
	}
	mutate(device, lease, leaf, ledger)
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM.Data[topologyrollout.LedgerDataKey] = string(encoded)

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(device, lease, leaf, policy, ledgerCM).Build()
	r := &CiscoDeviceReconciler{
		Client: apiClient, APIReader: apiClient, Scheme: scheme,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace,
	}
	return r, device
}

func managedPolicyAndLedger(t *testing.T, reservations map[string]topologyrollout.Reservation) (*corev1.ConfigMap, *corev1.ConfigMap) {
	t.Helper()
	const (
		namespace  = "cvk-system"
		ledgerName = "cvk-rollout-ledger"
		ledgerUID  = "ledger-uid"
	)
	cfg := topologyrollout.AdminPolicyConfig{
		Version:                      topologyrollout.PolicyVersion,
		FleetSelector:                metav1.LabelSelector{MatchLabels: map[string]string{managedprotocol.AnnotationManaged: "true"}},
		RequiredTopologyKeys:         []string{topology.CiscoTopologyLabelPrefix + "site"},
		ProjectedTopologyKeys:        []string{topology.CiscoTopologyLabelPrefix + "site"},
		GlobalMaxConcurrentTransfers: 1,
		GlobalMaxUnavailable:         1,
		DomainMaxConcurrentTransfers: map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		DomainMaxUnavailable:         map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		HealthFreshnessSeconds:       120,
		MaxCampaignTargets:           10,
		MaxActiveReservations:        32,
		MaxLedgerBytes:               64 * 1024,
		LedgerName:                   ledgerName,
	}
	data, err := topologyrollout.CanonicalPolicyJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: "topology-policy", UID: "policy-uid", ResourceVersion: "1",
		Annotations: map[string]string{
			topologyrollout.PolicyManagedAnnotation:   "true",
			topologyrollout.LedgerUIDAnnotation:       ledgerUID,
			topologyrollout.AdmissionPrefixAnnotation: "cvk-topology",
		},
	}, Data: map[string]string{topologyrollout.PolicyDataKey: data}}
	ledger, err := topologyrollout.NewLedger(ledgerUID)
	if err != nil {
		t.Fatal(err)
	}
	for id, reservation := range reservations {
		ledger.Reservations[id] = reservation
	}
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: ledgerName, UID: ledgerUID, ResourceVersion: "1",
	}, Data: map[string]string{topologyrollout.LedgerDataKey: string(encoded)}}
	return policy, ledgerCM
}

func validDeletionReservation() topologyrollout.Reservation {
	return topologyrollout.Reservation{
		ReservationRequest: topologyrollout.ReservationRequest{
			ID: "reservation-1", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
			PolicyUID: "policy-uid", PolicyVersion: "1", PolicyEpoch: 1, TopologyLockID: strings.Repeat("1", 32), PhysicalID: "serial-1", DeviceUID: "device-uid",
			NodeUID: "node-uid", ChildNamespace: "edge", ChildName: "upgrade", Domains: map[string]string{topology.CiscoTopologyLabelPrefix + "site": "site-a"},
		},
		State: topologyrollout.ReservationReserved,
	}
}
