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

func testFabricL2HandoffApplicationPersistence(t *testing.T, native *client, verified *cvk.NetworkControllerConfigurationOperation, key fabricL2HandoffIntent, now time.Time) {
	t.Helper()
	raw := `{"catalyst_center":{"inventory":{"devices":[{"name":"border","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"border","border_types":["LAYER_2"],"l2_handoffs":{"l2_with_anycast_gateway":[{"l3_virtual_network":"Campus","ip_pool_name":"Users","external_vlan":400,"interfaces":["GigabitEthernet1/0/1"]}]}}]}}}`
	record := func(a *adapter, ctx context.Context, config *configapi.NetworkControllerConfig, id, name string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, at time.Time) error {
		selected := key
		if name != "Shared" {
			selected.device = "Absent"
		}
		return a.recordFabricL2HandoffApplication(ctx, config, id, selected, members, authorize, at)
	}
	check := func(state *cvk.NetworkControllerConfigurationStatus, name, uid string) (bool, error) {
		if state == nil || len(state.ActionApplications) != 1 {
			return false, errConfigurationStale
		}
		receipt := state.ActionApplications[0]
		return receipt.Section == "fabric" && strings.HasPrefix(receipt.Resource, "fabric-l2-handoff:") && strings.HasSuffix(receipt.Resource, ":created-handoff") && receipt.Operation.ConfigName == name && receipt.Operation.ConfigUID == uid && receipt.Operation.Phase == "Verified", nil
	}
	testGroupApplicationPersistence(t, native, raw, verified, now, record, check)
}
