//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"
)

func TestLiveCatalystCenterNetworkNativeComparison(t *testing.T) {
	if os.Getenv("CATC_NETWORK_NATIVE_COMPARISON") != "isolated-keyed" || os.Getenv("CATC_TELEMETRY_PARTIAL_EXECUTION") != "isolated-preserving" {
		t.Skip("isolated private native baseline qualification disabled")
	}
	qualifyLiveSiteExecution(t, false)
}
