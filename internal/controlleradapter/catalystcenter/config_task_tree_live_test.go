// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"testing"
	"time"
)

// Inspect a previously acknowledged lab task; never submit a replacement task
// to manufacture qualification. This does not qualify bulk/device convergence.
func TestLiveCatalystCenterRetainedTaskCompletionBoundary(t *testing.T) {
	id := os.Getenv("CATC_QUALIFICATION_TASK_ID")
	submitted, err := time.Parse(time.RFC3339, os.Getenv("CATC_QUALIFICATION_TASK_SUBMITTED"))
	if id == "" {
		t.Skip("retained acknowledged task identity not supplied")
	}
	if err != nil {
		t.Fatal("recorded submission time unavailable")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now().UTC()
	completed, err := c.configurationTaskTreeCompletion(ctx, id, submitted, now)
	if err != nil {
		t.Fatal("retained task completion evidence unavailable", err)
	}
	if completed.IsZero() || completed.After(now) {
		t.Fatal("retained task is incomplete or future-dated")
	}
	t.Logf("recorded native task submission=%s latest_validated_completion=%s; read-only controller evidence, not source-bound device proof", submitted.Format(time.RFC3339), completed.Format(time.RFC3339Nano))
}
