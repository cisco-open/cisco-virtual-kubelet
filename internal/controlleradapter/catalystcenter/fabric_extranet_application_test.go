// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"testing"
	"time"
)

func testExtranetApplicationPersistence(t *testing.T, native *client, raw string, verified *cvk.NetworkControllerConfigurationOperation, now time.Time, ownership ...cvk.NetworkControllerConfigurationActionApplication) {
	testGroupApplicationPersistence(t, native, raw, verified, now, (*adapter).recordExtranetApplication, func(state *cvk.NetworkControllerConfigurationStatus, name, uid string) (bool, error) {
		return ownedExtranetPolicy(state, name, uid, "Shared", "policy", now)
	}, ownership...)
}
