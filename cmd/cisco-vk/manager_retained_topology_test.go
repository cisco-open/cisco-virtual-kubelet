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

package main

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestManagedTopologyStatePresentDetectsEveryRetainedIdentity(t *testing.T) {
	tests := []struct {
		name    string
		objects []runtime.Object
		want    bool
	}{
		{name: "fresh default-off installation"},
		{name: "retained configuration observer", objects: []runtime.Object{&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "retained-observer", Labels: map[string]string{"configuration.cisco.vk/controller-uid": "controller-uid"}, Annotations: map[string]string{"configuration.cisco.vk/controller-namespace": "lab"}},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementGlobalReadClusterRole},
		}}, want: true},
		{name: "orphan held reservation", objects: []runtime.Object{&ops.ControllerConfigurationReservation{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "retained"},
			Status:     ops.ControllerConfigurationReservationStatus{Grant: &ops.ConfigurationReservationGrant{Phase: "Held"}},
		}}, want: true},
		{name: "orphan settled reservation", objects: []runtime.Object{&ops.ControllerConfigurationReservation{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "retained"},
			Status:     ops.ControllerConfigurationReservationStatus{Grant: &ops.ConfigurationReservationGrant{Phase: "Released"}},
		}}, want: true},
		{name: "incomplete reservation evidence", objects: []runtime.Object{&ops.ControllerConfigurationReservation{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "retained"},
		}}, want: true},
		{
			name: "managed Node identity",
			objects: []runtime.Object{&ciskov1.CiscoDevice{
				ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "device"},
				Status:     ciskov1.DeviceStatus{NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{NodeName: "node", NodeUID: "node-uid", DeviceUID: "device-uid"}},
			}},
			want: true,
		},
		{
			name: "completed legacy handoff",
			objects: []runtime.Object{&ciskov1.CiscoDevice{
				ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "device"},
				Status:     ciskov1.DeviceStatus{LegacyHandoff: &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffComplete}},
			}},
			want: true,
		},
		{
			name: "damaged managed Pod-delete completion authority",
			objects: []runtime.Object{&rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-delete"},
				RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.ManagedWorkerPodDeleteClusterRole},
				Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Namespace: "edge", Name: "cisco-vk-managed-node-12345678"}},
			}},
			want: true,
		},
		{
			name: "isolated legacy cluster authority",
			objects: []runtime.Object{&rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy", Annotations: map[string]string{
					managedprotocol.AnnotationWorkerMode:      managedprotocol.WorkerModeLegacy,
					managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
					managedprotocol.AnnotationDeviceNamespace: "edge",
					managedprotocol.AnnotationDeviceName:      "device",
					managedprotocol.AnnotationDeviceUID:       "device-uid",
				}},
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cisco-virtual-kubelet"},
				Subjects: []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Namespace: "edge", Name: "legacy"}},
			}},
			want: true,
		},
		{
			name: "forged ServiceAccount annotation is not authority",
			objects: []runtime.Object{&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: "edge", Name: "forged", Annotations: map[string]string{
					managedprotocol.AnnotationWorkerMode: managedprotocol.WorkerModeLegacy,
				},
			}}},
		},
		{
			name: "released legacy Node",
			objects: []runtime.Object{&corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node", Annotations: map[string]string{managedprotocol.AnnotationLegacyHandoff: "node-uid"},
			}}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := ciskov1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := ops.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objects...).Build()
			got, err := managedTopologyStatePresent(context.Background(), reader)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("managedTopologyStatePresent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManagedTopologyStatePresentRejectsNilReader(t *testing.T) {
	if _, err := managedTopologyStatePresent(context.Background(), nil); err == nil {
		t.Fatal("nil API reader was accepted")
	}
}

// Fault only the additive reservation discovery; earlier topology reads remain
// successful and empty, matching an installation with no other retained state.
type reservationListFailureReader struct {
	client.Reader
	failure error
}

func (r reservationListFailureReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*ops.ControllerConfigurationReservationList); ok {
		return r.failure
	}
	return r.Reader.List(ctx, list, opts...)
}
func TestManagedTopologyReservationDiscoveryErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ciskov1.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	resource := schema.GroupResource{Group: ops.GroupVersion.Group, Resource: "controllerconfigurationreservations"}
	for _, tc := range []struct {
		name    string
		failure error
		wantErr bool
	}{
		{"CRD not installed", &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Group: resource.Group, Kind: "ControllerConfigurationReservation"}}, false},
		{"API endpoint absent", apierrors.NewNotFound(resource, ""), false},
		{"forbidden is not absence", apierrors.NewForbidden(resource, "", errors.New("forbidden")), true},
		{"unavailable is not absence", apierrors.NewServiceUnavailable("unavailable"), true},
		{"cancelled is not absence", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, err := managedTopologyStatePresent(context.Background(), reservationListFailureReader{Reader: base, failure: tc.failure})
			if present || (err != nil) != tc.wantErr {
				t.Fatalf("present=%v err=%v", present, err)
			}
		})
	}
}
