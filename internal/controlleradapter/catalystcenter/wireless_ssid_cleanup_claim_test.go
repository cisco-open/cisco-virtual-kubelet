// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWirelessSSIDQualificationCleanupClaimIsDurableAndSingleUse(t *testing.T) {
	identity, state, _ := ssidQualificationProof(t, 2)
	cleanup := qualificationCleanup{Name: identity.Name, ID: identity.ID, UpdateTaskID: state.Operation.TaskID, DeleteAttempted: true, SubmittedAt: time.Now().UTC()}
	path := filepath.Join(t.TempDir(), "cleanup.json")
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := claimQualificationCleanup(path, cleanup); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("cleanup claimed more than once", wins.Load())
	}
	if err := claimQualificationCleanup(path, cleanup); err == nil {
		t.Fatal("restart acquired another deletion permit")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cleanup marker permission", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored qualificationCleanup
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if validateSSIDQualificationCleanup(identity, state.Operation, stored) == nil {
		t.Fatal("unknown DELETE outcome granted replay/recovery without task")
	}
	stored.DeleteTaskID = "delete-task"
	if err := validateSSIDQualificationCleanup(identity, state.Operation, stored); err != nil {
		t.Fatal("known delete task not observable", err)
	}
	stored.ID = "replaced-id"
	if validateSSIDQualificationCleanup(identity, state.Operation, stored) == nil {
		t.Fatal("cleanup target changed")
	}
}
