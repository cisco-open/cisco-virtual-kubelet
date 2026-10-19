// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestLiveCatalystCenterLANActiveSessions(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	before, err := c.lanActiveSessions(ctx)
	if err != nil {
		t.Fatal("native active-session observation", err)
	}
	after, err := c.lanActiveSessions(ctx)
	if err != nil {
		t.Fatal("native active-session re-observation", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("native active sessions changed during read-only qualification")
	}
	t.Logf("qualified native activity snapshot: maximum=%d active=%d; no automation action submitted", before.Maximum, len(before.Active))
}
