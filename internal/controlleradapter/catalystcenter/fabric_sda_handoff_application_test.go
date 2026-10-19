// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func testFabricSDAHandoffApplicationPersistence(t *testing.T, native *client, verified *cvk.NetworkControllerConfigurationOperation, key fabricSDAHandoffIntent, now time.Time) {
	t.Helper()
	raw := `{"catalyst_center":{"inventory":{"devices":[{"name":"border","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"border","border_types":["LAYER_3"],"local_autonomous_system_number":65000,"default_exit":true,"import_external_routes":true,"sda_transit":"SDA"}]}}}`
	record := func(a *adapter, ctx context.Context, config *configapi.NetworkControllerConfig, id, name string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, at time.Time) error {
		selected := key
		if name != "Shared" {
			selected.Device = "Absent"
		}
		return a.recordFabricSDAHandoffApplication(ctx, config, id, selected, members, authorize, at)
	}
	check := func(state *cvk.NetworkControllerConfigurationStatus, name, uid string) (bool, error) {
		if state == nil || len(state.ActionApplications) != 1 {
			return false, errConfigurationStale
		}
		receipt := state.ActionApplications[0]
		return receipt.Section == "fabric" && strings.HasPrefix(receipt.Resource, "fabric-sda-handoff:") && receipt.Operation.ConfigName == name && receipt.Operation.ConfigUID == uid && receipt.Operation.Phase == "Verified", nil
	}
	testGroupApplicationPersistence(t, native, raw, verified, now, record, check)
}
