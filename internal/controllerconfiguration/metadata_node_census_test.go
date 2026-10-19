// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMetadataNodeIdentityCensus(t *testing.T) {
	for _, kind := range []string{"metadata", "bootstrap"} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"ordinary-empty", "other-virtual-empty", "provider-empty", "os-empty", "owner-empty", "empty-owner-marker", "protocol-empty", "known-machine", "known-system", "known-consistent", "conflicting-observations", "malformed-observation", "matching-alias", "managed-matching-alias", "deleting-empty"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					c, nc, _ := bootstrapIdentityFixture(t)
					_ = ops.AddToScheme(c.Scheme())
					_ = coordination.AddToScheme(c.Scheme())
					if kind == "metadata" {
						nc.Status.Configuration.Operation.Action = "Delete"
						nc.Status.Configuration.Operation.TargetSerial = ""
						nc.Status.Configuration.Operation.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
						if err := c.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
							t.Fatal(err)
						}
					}
					node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "orphan", UID: "node", Labels: map[string]string{topology.LabelProvider: topology.ProviderCiscoAppHosting}}}
					switch mode {
					case "ordinary-empty":
						node.Labels = nil
					case "other-virtual-empty":
						node.Labels = map[string]string{topology.LabelProvider: "other-provider", topology.LabelType: topology.TypeVirtualKubelet}
					case "provider-empty":
					case "os-empty":
						node.Labels = nil
						node.Status.NodeInfo.OperatingSystem = "Cisco"
					case "owner-empty":
						node.Labels = nil
						node.Annotations = map[string]string{managedprotocol.AnnotationDeviceUID: "retained-device"}
					case "empty-owner-marker":
						node.Labels = nil
						node.Annotations = map[string]string{managedprotocol.AnnotationDeviceNamespace: ""}
					case "protocol-empty":
						node.Labels = nil
						node.Annotations = map[string]string{managedprotocol.AnnotationManaged: "true"}
					case "known-machine":
						node.Status.NodeInfo.MachineID = "different123"
					case "known-system":
						node.Status.NodeInfo.SystemUUID = "different123"
					case "known-consistent":
						node.Status.NodeInfo.MachineID = "DIFFERENT123"
						node.Status.NodeInfo.SystemUUID = "different123"
					case "conflicting-observations":
						node.Status.NodeInfo.MachineID = "different123"
						node.Status.NodeInfo.SystemUUID = "another123"
					case "malformed-observation":
						node.Status.NodeInfo.MachineID = "bad serial"
					case "matching-alias":
						node.Labels = nil
						node.Status.NodeInfo.MachineID = "BOOTSTRAP123"
					case "managed-matching-alias":
						node.Status.NodeInfo.SystemUUID = "bootstrap123"
					case "deleting-empty":
						now := metav1.Now()
						node.DeletionTimestamp = &now
						node.Finalizers = []string{"retain-test-node"}
					}
					if err := c.Create(ctx, node); err != nil {
						t.Fatal(err)
					}
					var err error
					if kind == "metadata" {
						err = CheckUnmanagedMetadataScope(ctx, c, nc, "leases", []string{"bootstrap123", "second123"})
					} else {
						_, err = ReadBootstrapIdentity(ctx, c, nc)
					}
					allowed := mode == "ordinary-empty" || mode == "other-virtual-empty" || mode == "known-machine" || mode == "known-system" || mode == "known-consistent"
					if kind == "bootstrap" {
						allowed = mode != "matching-alias" && mode != "managed-matching-alias"
					}
					if (err == nil) != allowed {
						t.Fatal("unexpected Node identity census result", err)
					}
				})
			}
		})
	}
}
