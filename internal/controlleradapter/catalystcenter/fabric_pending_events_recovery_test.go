// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestFabricPendingEventDeclaration(t *testing.T) {
	for _, mode := range []string{"valid", "disabled", "foreign-site", "extra-section", "extra-fabric", "source"} {
		t.Run(mode, func(t *testing.T) {
			_, config := journalFixture(t)
			site := map[string]any{"name": "Global/Lab/Building", "pub_sub_enabled": true, "authentication_template": map[string]any{"name": "Closed Authentication"}, "reconfigure": true}
			fabric := map[string]any{"fabric_sites": []any{site}}
			root := map[string]any{"fabric": fabric}
			switch mode {
			case "disabled":
				site["reconfigure"] = false
			case "foreign-site":
				site["name"] = "Global/Other"
			case "extra-section":
				root["inventory"] = map[string]any{}
			case "extra-fabric":
				fabric["other"] = true
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
			config.Spec.Source.Inline.Raw = raw
			var source *configSourceSnapshot
			if mode == "source" {
				source = &configSourceSnapshot{raw: raw}
				config.Spec.Source.Inline = nil
			}
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			err := declaredPendingFabricEvent(config, source, want)
			if (err == nil) != (mode == "valid" || mode == "source") {
				t.Fatal("declaration result", err)
			}
		})
	}
}

func TestFabricOwnedObservationBracket(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-section", "missing-owner", "foreign-owner", "late-owner", "late-source", "wrong-operation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, nil, "fabric", "fabric-test", "Update")
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "wrong-section":
				op.Section = "inventory"
			case "wrong-operation":
				op.ID = "different"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			calls := 0
			ok, err := a.observeOwnedConfigurationOperation(ctx, config, nil, op, "fabric", func(ctx context.Context) (bool, error) {
				calls++
				if mode == "late-owner" {
					fresh, e := a.currentController(ctx)
					if e != nil {
						return false, e
					}
					fresh.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{}
					if e := a.statusWriter.Status().Update(ctx, fresh); e != nil {
						return false, e
					}
				}
				if mode == "late-source" {
					copy := config.DeepCopy()
					copy.Generation++
					if e := a.statusWriter.Update(ctx, copy); e != nil {
						return false, e
					}
				}
				return true, nil
			})
			if (err == nil && ok) != (mode == "valid") {
				t.Fatal("owner bracket", err)
			}
			if (mode == "missing-owner" || mode == "foreign-owner" || mode == "wrong-section" || mode == "wrong-operation") && calls != 0 {
				t.Fatal("unowned observation executed")
			}
		})
	}
}
