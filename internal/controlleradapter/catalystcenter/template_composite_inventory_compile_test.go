// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"strings"
	"testing"
)

func testCompositeInventoryCompiler(t *testing.T, native *client, setUnprovisioned func(bool)) {
	t.Helper()
	for _, mode := range []string{"composite", "mixed", "bad-member"} {
		t.Run("compiler-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = native
			config.Spec.ManagedSections = []string{"inventory"}
			dayn := `"composite":[{"name":"Lab#Combined"}]`
			if mode == "mixed" {
				dayn += `,"regular":[{"name":"Lab#Baseline"}]`
			}
			if mode == "bad-member" {
				dayn = `"composite":[{"name":"Lab#Combined","variables":[{"template_name":"Missing","name":"value","value":"sensitive"}]}]`
			}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","serial_number":"FOC12345678","site":"Global/Lab/Building","state":"PROVISION","dayn_templates":{` + dayn + `}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.compileCompositeInventoryApply(config, source); err == nil {
				t.Fatal("missing admission hook accepted")
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileCompositeInventoryApply(config, source)
			if err != nil {
				t.Fatal(err)
			}
			setUnprovisioned(true)
			defer setUnprovisioned(false)
			write, err := plan.executor.next(ctx)
			if mode == "bad-member" {
				if err == nil || write != nil {
					t.Fatal("unresolved member permitted provisioning")
				}
				return
			}
			if err != nil || write == nil || !validWiredProvisionResource(write.resource) {
				t.Fatal("composite preflight blocked prerequisite planning", err)
			}
			setUnprovisioned(false)
			write, err = plan.executor.next(ctx)
			prefix := "template-composite-deploy:"
			if mode == "mixed" {
				prefix = "template-deploy:"
			}
			if err != nil || write == nil || !strings.HasPrefix(write.resource, prefix) {
				t.Fatal("wrong post-provision stage", err)
			}
			if mode == "composite" && (write.identity.dependencies == nil || len(write.identity.dependencies.Entries) != 3) {
				t.Fatal("compiled deployment lost parent/member graph")
			}
			if _, err := a.compileAdmittedInventoryApply(config, source); err != nil {
				t.Fatal("public composite compiler rejected qualified model", err)
			}
		})
	}
}
