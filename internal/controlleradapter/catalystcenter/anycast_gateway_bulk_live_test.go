// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

func TestLiveCatalystCenterAnycastBulkPrerequisites(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sites, err := c.fabricSites(ctx)
	if err != nil {
		t.Fatal("native fabric inventory unavailable", err)
	}
	t.Logf("read-only native fabric site count=%d; no fabric or gateway was created", len(sites))
	if len(sites) == 0 {
		t.Skip("no lab fabric site exists: accepted anycast bulk execution remains unqualified")
	}
	t.Log("fabric inventory available; isolated source, pool/VN prerequisites and accepted-task qualification still required")
}
