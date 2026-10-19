//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Preserve public metadata evidence on a terminal qualification failure, never
// credentials, comparison keys, raw native progress or source configuration.
func (h *liveNetworkProfileRecovery) captureUnsettledProfile(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nc, err := h.a.currentController(ctx)
	if err != nil || nc.Status.Configuration == nil {
		t.Log("unsettled profile journal unavailable")
		return
	}
	reasons := []string{}
	for _, condition := range h.config.Status.Conditions {
		reasons = append(reasons, condition.Reason)
	}
	phase := ""
	if nc.Status.Configuration.Operation != nil {
		phase = nc.Status.Configuration.Operation.Phase
	}
	t.Log("unsettled profile phase", phase, "config phase", h.config.Status.Phase, "condition reasons", reasons)
	dir := os.Getenv("CATC_PROFILE_RECOVERY_STATE_DIR")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Log("diagnostic directory must be absolute")
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Log("diagnostic directory unavailable")
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		t.Log("diagnostic directory must be private")
		return
	}
	digest := sha256.Sum256(h.config.Spec.Source.Inline.Raw)
	data, err := json.MarshalIndent(map[string]any{"sourceSha256": hex.EncodeToString(digest[:]), "operation": nc.Status.Configuration.Operation, "owners": nc.Status.Configuration.Owners, "actionApplications": nc.Status.Configuration.ActionApplications, "configPhase": h.config.Status.Phase, "conditionReasons": reasons}, "", "  ")
	if err != nil {
		t.Log("diagnostic metadata encoding failed")
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("profile-%d.json", time.Now().UnixNano()))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Log("diagnostic metadata file unavailable")
		return
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Log("diagnostic metadata write incomplete")
		return
	}
	t.Log("public recovery metadata retained", path)
}
