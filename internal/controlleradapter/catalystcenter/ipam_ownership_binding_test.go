// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

func TestIPAMOwnershipBindsComposedExecutors(t *testing.T) {
	for _, kind := range []string{"direct", "network", "credentials", "fabric-group", "anchor-gate", "extranet-gate", "multicast", "vlan-ssid", "actual-fabric-compiler"} {
		t.Run(kind, func(t *testing.T) {
			a, config := journalFixture(t)
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			ipam := &ipamConfigurationExecutor{intent: intent}
			network := &networkSectionExecutor{ipam: ipam}
			var executor configurationExecutor = ipam
			section, prefix := "network_settings", ""
			switch kind {
			case "network":
				executor = network
			case "credentials":
				executor = &networkCredentialConfigurationExecutor{network: network}
			case "fabric-group", "anchor-gate", "extranet-gate", "multicast", "vlan-ssid":
				section, prefix = "fabric", "network_settings/"
				executor = &configurationGroup{parts: []configurationPart{{name: "network_settings", executor: network}}}
				switch kind {
				case "anchor-gate":
					executor = &fabricAnchorAdmissionGate{configurationExecutor: executor}
				case "extranet-gate":
					executor = &fabricExtranetAdmissionGate{configurationExecutor: executor}
				case "multicast":
					executor = &fabricMulticastConfiguration{foundation: executor}
				case "vlan-ssid":
					executor = &fabricVLANSSIDConfiguration{foundation: executor}
				}
			case "actual-fabric-compiler":
				raw, err := os.ReadFile("../../../examples/configs/catalyst-center/ipam-fabric-apply.yaml")
				if err != nil {
					t.Fatal(err)
				}
				var example configapi.NetworkControllerConfig
				if err := yaml.Unmarshal(raw, &example); err != nil {
					t.Fatal(err)
				}
				compiled, err := (&client{}).compileConfigurationApply(example.Spec.Source.Inline.Raw, example.Spec.ManagedSections, nil)
				if err != nil {
					t.Fatal(err)
				}
				executor = compiled.executor
				section, prefix = "fabric", "network_settings/"
				group, ok := executor.(*configurationGroup)
				if !ok {
					t.Fatal("example compiler changed")
				}
				ipam = group.parts[0].executor.(*networkSectionExecutor).ipam
			}
			var existingCalls int
			plan := configurationApplyPlan{executor: executor, section: section, observeCompletion: func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
				existingCalls++
				return true, nil
			}}
			if err := a.bindIPAMOwnership(nc, config, &configSourceSnapshot{raw: []byte(poolModel)}, &plan); err != nil {
				t.Fatal(err)
			}
			if ipam.owner != string(config.UID) || plan.recordCompletion == nil || plan.receiptOperation == nil {
				t.Fatal("IPAM not bound")
			}
			legacy := ipamPoolIdentity(ipam.intent.pools[0].Name)
			receipt := ipamOwnershipReceiptFixture()
			op := receipt.Operation.DeepCopy()
			op.Phase = "Unknown"
			op.Section = section
			op.Resource = prefix + ipamOwnedResource(legacy)
			if !plan.receiptOperation(op) || !plan.canObserveUnknownTask(op) {
				t.Fatal("composed receipt/recovery selection missing")
			}
			now := time.Now()
			stamp := now.UnixMilli()
			op.SubmittedAt.Time = now.Add(-time.Second)
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": op.TaskID, "startTime": stamp, "endTime": stamp, "isError": false}})
			})
			if done, err := plan.taskComplete(context.Background(), a.client, op, now); err != nil || !done || existingCalls != 0 {
				t.Fatal("IPAM task routed to another resource observer", done, err, existingCalls)
			}
			other := op.DeepCopy()
			other.Phase = "Accepted"
			other.Resource = "another-resource"
			if done, err := plan.taskComplete(context.Background(), a.client, other, now); err != nil || !done || existingCalls != 1 {
				t.Fatal("existing observer lost", done, err, existingCalls)
			}
			for _, change := range []string{"owner", "parent", "physical", "malformed"} {
				bad := op.DeepCopy()
				switch change {
				case "owner":
					bad.ConfigUID = "foreign"
				case "parent":
					bad.ParentID = "unexpected"
				case "physical":
					bad.TargetSerial = "device"
				case "malformed":
					bad.Resource = prefix + ipamOwnedPrefix + "pool:bad"
				}
				if plan.canObserveUnknownTask(bad) {
					t.Fatal("bad recovery accepted", change)
				}
			}
		})
	}
}
