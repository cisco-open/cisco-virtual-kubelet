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
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
)

func TestReserveManagedNodeCollisionAndUIDBinding(t *testing.T) {
	ctx := context.Background()
	projectionHash := "sha256:" + strings.Repeat("a", 64)
	projected := map[string]string{topology.LabelType: topology.TypeVirtualKubelet}

	for _, test := range []struct {
		name      string
		configure func(*ciskov1.CiscoDevice, *corev1.Node)
		wantError string
		wantBound bool
	}{
		{
			name: "already-existing foreign Node requires exact adoption approval",
			configure: func(_ *ciskov1.CiscoDevice, node *corev1.Node) {
				node.Labels = map[string]string{topology.LabelType: topology.TypeVirtualKubelet}
			},
			wantError: "already exists without this device binding",
		},
		{
			name: "matching binding without Node UID is completed",
			configure: func(device *ciskov1.CiscoDevice, node *corev1.Node) {
				node.Annotations = managedReservationIdentity(device)
			},
			wantBound: true,
		},
		{
			name: "matching binding naming a stale Node UID is rejected",
			configure: func(device *ciskov1.CiscoDevice, node *corev1.Node) {
				node.Annotations = managedReservationIdentity(device)
				node.Annotations[managedprotocol.AnnotationNodeUID] = "deleted-node-uid"
			},
			wantError: "binding names stale UID",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			device := newDevice("switch-reservation", "edge")
			device.UID = "device-uid"
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: device.Name, UID: "current-node-uid", ResourceVersion: "1",
			}}
			test.configure(device, node)
			scheme := newTestScheme(t)
			apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
			r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}

			reserved, err := r.reserveManagedNode(ctx, device, projected, projectionHash)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("reserveManagedNode() error = %v, want %q", err, test.wantError)
				}
				var unchanged corev1.Node
				if getErr := apiClient.Get(ctx, client.ObjectKeyFromObject(node), &unchanged); getErr != nil {
					t.Fatal(getErr)
				}
				if unchanged.Annotations[managedprotocol.AnnotationNodeUID] == string(unchanged.UID) &&
					!managedNodeMatchesDevice(&unchanged, device) {
					t.Fatal("failed reservation partially bound a foreign Node")
				}
				return
			}
			if err != nil {
				t.Fatalf("reserveManagedNode() error = %v", err)
			}
			if !test.wantBound || reserved.Annotations[managedprotocol.AnnotationNodeUID] != string(node.UID) {
				t.Fatalf("reserved Node UID binding = %q, want %q", reserved.Annotations[managedprotocol.AnnotationNodeUID], node.UID)
			}
		})
	}
}

func TestReserveManagedNodeAdoptionUsesOptimisticLock(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-adoption-race", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "legacy-node-uid", ResourceVersion: "7",
		Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
	}}
	device.Annotations = map[string]string{managedprotocol.AnnotationAdoptNodeUID: string(node.UID)}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	conflictClient := &managedNodeAdoptionConflictClient{Client: base}
	r := &CiscoDeviceReconciler{Client: conflictClient, APIReader: base, Scheme: scheme}

	_, err := r.reserveManagedNode(ctx, device,
		map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
		"sha256:"+strings.Repeat("b", 64))
	if err == nil || !apierrors.IsConflict(errors.Unwrap(err)) {
		t.Fatalf("reserveManagedNode() error = %v, want wrapped optimistic-lock conflict", err)
	}
	if !conflictClient.sawResourceVersion {
		t.Fatal("adoption patch omitted the resourceVersion optimistic-lock precondition")
	}
	var current corev1.Node
	if err := base.Get(ctx, client.ObjectKeyFromObject(node), &current); err != nil {
		t.Fatal(err)
	}
	if managedNodeMatchesDevice(&current, device) || current.Annotations[managedprotocol.AnnotationNodeUID] != "" {
		t.Fatalf("conflicted adoption partially changed Node metadata: %#v", current.Annotations)
	}
}

func managedReservationIdentity(device *ciskov1.CiscoDevice) map[string]string {
	return map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
	}
}

type managedNodeAdoptionConflictClient struct {
	client.Client
	sawResourceVersion bool
}

func (c *managedNodeAdoptionConflictClient) Patch(
	ctx context.Context,
	object client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	if node, ok := object.(*corev1.Node); ok {
		data, err := patch.Data(object)
		if err != nil {
			return err
		}
		c.sawResourceVersion = strings.Contains(string(data), `"resourceVersion":"`+node.ResourceVersion+`"`)
		return apierrors.NewConflict(
			schema.GroupResource{Resource: "nodes"},
			node.Name,
			errors.New("Node changed after adoption read"),
		)
	}
	return c.Client.Patch(ctx, object, patch, opts...)
}
