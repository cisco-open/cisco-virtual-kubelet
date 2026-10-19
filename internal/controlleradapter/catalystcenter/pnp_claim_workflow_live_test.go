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

// This test only reads inventory. It cannot register, claim, reset or delete a
// device, and cannot manufacture an owned operation from an existing record.
func TestLivePnPWorkflowInventoryReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.pnpDevices(ctx)
	if err != nil {
		t.Fatal("native safe PnP inventory unavailable", err)
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("native network inventory unavailable", err)
	}
	after, err := c.pnpDevices(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("PnP snapshot did not remain stable across inventory observation", err)
	}
	workflows, imageRefs, templateRefs := 0, 0, 0
	for _, row := range before {
		if row.Workflow == nil {
			continue
		}
		workflows++
		if row.Workflow.ImageID != "" {
			imageRefs++
		}
		if row.Workflow.ConfigID != "" {
			templateRefs++
		}
	}
	t.Logf("read-only repeated safe inventory: PnP=%d network=%d main-workflows=%d image-references=%d template-references=%d; no device writes", len(before), len(devices), workflows, imageRefs, templateRefs)
	t.Run("AcceptedOwnedClaim", func(t *testing.T) {
		t.Skip("no explicitly owned lab PnP claim supplied; reference matching, native completion and admission remain unqualified")
	})
}
