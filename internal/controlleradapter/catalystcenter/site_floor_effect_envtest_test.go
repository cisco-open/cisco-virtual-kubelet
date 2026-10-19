// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// Real private API persistence/settlement tests, not native verification. The
// fixture injects already-verified evidence; no product request is performed.
func TestEnvtestSiteFloorEffectReceipts(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "multiple", "reordered", "missing-member", "missing", "changed", "pruned-result", "pruned-producer", "schema-empty", "schema-long", "schema-count", "schema-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			installWirelessFeatureQualificationAPI(t, api, "floor-effects-"+mode)(a, config)
			state, row := siteFloorEffectsFixture(t, func(op *cvk.NetworkControllerConfigurationOperation) {
				op.ConfigName, op.ConfigUID = config.Name, string(config.UID)
				op.ConfigGeneration, op.ControllerGeneration = config.Generation, a.generation
			})
			state.Owners = []cvk.NetworkControllerSectionOwner{{Section: "sites", ConfigName: config.Name, ConfigUID: string(config.UID)}}
			if mode == "multiple" || mode == "reordered" || mode == "missing-member" {
				anchor := *state.ActionApplications[1].DeepCopy()
				anchor.Operation.ID = strings.Repeat("b", 32)
				parts, _ := plannedAPPrivateParts(anchor.Operation.Resource)
				parts[0] = strings.Repeat("9", 64)
				anchor.Operation.Resource = plannedAPPrivateObservePrefix + strings.Join(parts, ":")
				id, mac := "33333333-3333-4333-8333-333333333333", strings.Repeat("8", 64)
				anchor.Resource = plannedAPIdentityResource(&anchor.Operation, id, mac)
				effect := plannedAPFloorEffectPrefix + parts[0] + ":" + id + ":" + parts[2] + ":" + strings.Repeat("7", 64) + ":" + siteReceiptDigest(anchor)
				row.EffectReceipts = append(row.EffectReceipts, effect)
				root, err := plannedAPFloorEffectRoot(row.EffectReceipts)
				if err != nil {
					t.Fatal(err)
				}
				row.Operation.Resource = row.Operation.Resource[:len(row.Operation.Resource)-64] + root
				row.Resource = row.Resource[:len(row.Resource)-64] + root
				state.ActionApplications = append(state.ActionApplications[:2], anchor, row)
				state.Operation = row.Operation.DeepCopy()
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = state
			// Mirror the real publisher: the original operation was read from
			// the API before changing Verifying to Verified. API-normalized time
			// representations must not be replaced with fixture-only Go values.
			nc.Status.Configuration.Operation.Phase = "Verifying"
			nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:len(nc.Status.Configuration.ActionApplications)-1]
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Verified"
			row.Operation = *nc.Status.Configuration.Operation.DeepCopy()
			nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, row)
			if err := a.persistConfigurationActionReceipt(ctx, nc); err != nil {
				t.Fatal("API lost the atomically persisted operation/effect receipt", err)
			}
			stored, err := a.currentController(ctx)
			if err != nil || !siteAppliedReceiptPresent(stored.Status.Configuration, &row.Operation) {
				t.Fatal("verified effect receipt did not survive API roundtrip", err)
			}
			storedRow := &stored.Status.Configuration.ActionApplications[len(stored.Status.Configuration.ActionApplications)-1]
			switch mode {
			case "reordered":
				storedRow.EffectReceipts[0], storedRow.EffectReceipts[1] = storedRow.EffectReceipts[1], storedRow.EffectReceipts[0]
			case "missing-member":
				storedRow.EffectReceipts = storedRow.EffectReceipts[:1]
			case "missing":
				storedRow.EffectReceipts = nil
			case "changed":
				storedRow.EffectReceipts[0] = strings.Replace(row.EffectReceipts[0], strings.Repeat("d", 64), strings.Repeat("a", 64), 1)
			case "pruned-result":
				r := storedRow
				r.Resource = r.Resource[:len(r.Resource)-65]
			case "pruned-producer":
				stored.Status.Configuration.Operation.Resource = "pruned-producer"
			case "schema-empty":
				storedRow.EffectReceipts = []string{""}
			case "schema-long":
				storedRow.EffectReceipts = []string{strings.Repeat("a", 513)}
			case "schema-count":
				storedRow.EffectReceipts = nil
				for i := 0; i < 257; i++ {
					storedRow.EffectReceipts = append(storedRow.EffectReceipts, fmt.Sprintf("effect-%d", i))
				}
			case "schema-duplicate":
				storedRow.EffectReceipts = []string{row.EffectReceipts[0], row.EffectReceipts[0]}
			}
			if mode != "complete" {
				err := a.statusWriter.Status().Update(ctx, stored)
				if strings.HasPrefix(mode, "schema-") {
					if err == nil {
						t.Fatal("API accepted out-of-bound or duplicate effects")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			err = a.finishConfiguration(ctx, row.Operation.ID)
			complete := mode == "complete" || mode == "multiple" || mode == "reordered"
			if (err == nil) != complete {
				t.Fatal("incomplete effect authority released the journal", err)
			}
			fresh, err := a.currentController(ctx)
			if err != nil || (fresh.Status.Configuration.Operation == nil) != complete {
				t.Fatal("journal persistence disagrees with effect settlement", err)
			}
		})
	}
}

func TestEnvtestSiteFloorEffectOldSchemaRefusesPublication(t *testing.T) {
	raw, err := os.ReadFile("../../../config/crd/cisco.vk_networkcontrollers.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var old apiextensions.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	for i := range old.Spec.Versions {
		row := old.Spec.Versions[i].Schema.OpenAPIV3Schema.Properties["status"].Properties["configuration"].Properties["actionApplications"].Items.Schema
		delete(row.Properties, "effectReceipts")
	}
	api := qualificationAPIServerClientEnvironment(t, &envtest.Environment{CRDs: []*apiextensions.CustomResourceDefinition{&old}, CRDDirectoryPaths: []string{"../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml"}, ErrorIfCRDPathMissing: true})
	ctx := context.Background()
	a, config := journalFixture(t)
	installWirelessFeatureQualificationAPI(t, api, "floor-effects-old-schema")(a, config)
	state, row := siteFloorEffectsFixture(t, func(op *cvk.NetworkControllerConfigurationOperation) {
		op.ConfigName, op.ConfigUID = config.Name, string(config.UID)
		op.ConfigGeneration, op.ControllerGeneration = config.Generation, a.generation
	})
	state.Owners = []cvk.NetworkControllerSectionOwner{{Section: "sites", ConfigName: config.Name, ConfigUID: string(config.UID)}}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state
	nc.Status.Configuration.Operation.Phase = "Verifying"
	nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:2]
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Verified"
	row.Operation = *nc.Status.Configuration.Operation.DeepCopy()
	nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, row)
	if err := a.persistConfigurationActionReceipt(ctx, nc); err == nil {
		t.Fatal("publisher accepted effects pruned by a real older API schema")
	}
	stored, err := a.currentController(ctx)
	if err != nil || stored.Status.Configuration.Operation == nil || len(stored.Status.Configuration.ActionApplications[2].EffectReceipts) != 0 {
		t.Fatal("old schema did not exercise actual effect pruning", err)
	}
	if siteAppliedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) || a.finishConfiguration(ctx, row.Operation.ID) == nil {
		t.Fatal("pruned effect proof released the journal")
	}
}
