// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import "testing"

func TestCreatedReceiptExcludedFromPhysicalProtocols(t *testing.T) {
	op, targets := groupIdentityFixture()
	op.CreatedResourceID = "native-id"
	if _, err := GroupOperationDigest(op, targets); err == nil {
		t.Fatal("created receipt entered group protocol")
	}
	op.PhysicalGroup = nil
	op.TargetSerial = "serial"
	if _, err := OperationDigest(op); err == nil {
		t.Fatal("created receipt entered single-device protocol")
	}
	op.CreatedResourceID = ""
	if _, err := OperationDigest(op); err != nil {
		t.Fatal("legacy physical protocol regressed", err)
	}
}
