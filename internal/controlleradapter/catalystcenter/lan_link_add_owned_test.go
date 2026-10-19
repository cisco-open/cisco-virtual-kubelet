// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLANLinkAddOwnedResultAdmission(t *testing.T) {
	for _, mode := range []string{"unchanged", "exhausted", "topology-refresh", "task-pruned", "changed-addresses", "changed-routing", "legacy", "foreign-owner", "missing-owner", "duplicate", "replaced-node", "new-source-generation", "next-link", "next-link-invalid"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f, members, _ := dispatchedLANLinkAddFixture(t, false)
			setLANLinkResult(f)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginLANLinkAddVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			if err := a.recordLANLinkAddApplication(ctx, config, op.ID, members, authorize, func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error {
				return nil
			}, time.Now()); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "exhausted":
				f.mode = "exhausted"
			case "topology-refresh":
				f.topologyID = "another-cache-record"
			case "task-pruned":
				f.taskMode = "failed"
			case "changed-addresses":
				f.ports[0].IPv4, f.ports[1].IPv4 = "192.0.2.132", "192.0.2.133"
			case "changed-routing":
				f.ports[0].ISIS = "changed"
			case "legacy":
				nc.Status.Configuration.ActionApplications[0].Resource = strings.Join(strings.Split(nc.Status.Configuration.ActionApplications[0].Resource, ":")[:2], ":")
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, *nc.Status.Configuration.ActionApplications[0].DeepCopy())
			case "replaced-node":
				var node core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "edge1"}, &node); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Delete(ctx, &node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion, node.UID = "", "replacement"
				if err := a.statusWriter.Create(ctx, &node); err != nil {
					t.Fatal(err)
				}
			case "next-link", "next-link-invalid":
				var document map[string]any
				if err := json.Unmarshal(source.raw, &document); err != nil {
					t.Fatal(err)
				}
				section := document["catalyst_center"].(map[string]any)["lan_automation"].(map[string]any)
				later := lanLinkFixture()
				later["name"] = "zz-next"
				later["primary_device_management_ip_address"] = want.primaryIP
				later["secondary_device_management_ip_address"] = want.secondaryIP
				later["primary_device_interface_name"] = "TenGigabitEthernet1/0/3"
				later["secondary_device_interface_name"] = "TenGigabitEthernet1/0/4"
				later["ip_pool_name"] = want.pool
				section["links"] = append(section["links"].([]any), later)
				raw, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				source.raw, config.Spec.Source.Inline.Raw = raw, raw
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				if mode == "next-link" {
					for i, device := range []string{"edge-0", "edge-1"} {
						port := lanPortFixture(device, []string{"TenGigabitEthernet1/0/3", "TenGigabitEthernet1/0/4"}[i])
						port.ID, port.Index = device+"-next-port", "2"
						f.extraPorts = append(f.extraPorts, port)
					}
				}
			case "new-source-generation":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			observed, _, err := a.observeLANLinkAddModel(ctx, config, source)
			success := mode == "unchanged" || mode == "exhausted" || mode == "topology-refresh" || mode == "task-pruned" || mode == "new-source-generation" || mode == "next-link"
			if (err == nil) != success {
				t.Fatal("unexpected owned-result admission", err)
			}
			if success && !observed[want.name].owned {
				t.Fatal("owned addressed link treated as a new write")
			}
			if mode == "next-link" && (len(observed) != 2 || observed["zz-next"].owned || !validLANLinkAddResource(observed["zz-next"].resource, "Update")) {
				t.Fatal("completed link blocked the next valid ADD")
			}
			if f.posts != 1 {
				t.Fatal("owned observation repeated native mutation")
			}
			if journalState(t, a).Operation.Phase != "Verified" {
				t.Fatal("observation changed settlement")
			}
			assertLANClaimsRetained(t, a, false)
		})
	}
}
