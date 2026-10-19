// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"strings"
	"testing"
)

func testTemplateInventoryCompiler(t *testing.T, c *client, setProvisioned func(bool)) {
	t.Helper()
	t.Run("compiler-prerequisites", func(t *testing.T) {
		ctx := context.Background()
		a, config := journalFixture(t)
		a.client = c
		config.Spec.ManagedSections = []string{"inventory"}
		config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","serial_number":"FOC12345678","site":"Global/Lab/Building","state":"PROVISION","dayn_templates":{"regular":[{"name":"Lab#Baseline","variables":[{"name":"value","value":"secret-original"}]}]}}]}}}`)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.compileTemplateInventoryApply(config, source); err == nil {
			t.Fatal("compiled without admission hook")
		}
		a.workerPodName, a.workerPodUID = "connector", "connector-uid"
		if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
			t.Fatal(err)
		}
		plan, err := a.compileTemplateInventoryApply(config, source)
		if err != nil {
			t.Fatal(err)
		}
		if plan.observeCompletion == nil || plan.section != "inventory" || plan.executor.count() < 2 {
			t.Fatal("missing composition metadata")
		}
		setProvisioned(false)
		defer setProvisioned(true)
		write, err := plan.executor.next(ctx)
		if err != nil || write == nil || !validWiredProvisionResource(write.resource) {
			t.Fatal("pre-provision template preflight failed", err)
		}
		setProvisioned(true)
		write, err = plan.executor.next(ctx)
		if err != nil || write == nil || !strings.HasPrefix(write.resource, "template-deploy:") {
			t.Fatal("deployment did not follow provisioning", err)
		}
		if write.identity.serial == "" {
			t.Fatal("target lost physical binding")
		}
		// Production dispatch uses the same admission-gated compiler.
		if _, err := a.compileAdmittedInventoryApply(config, source); err != nil {
			t.Fatal("public template compiler rejected intent", err)
		}
	})
}
