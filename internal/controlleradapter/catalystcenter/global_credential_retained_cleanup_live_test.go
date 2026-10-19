//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Supervised cleanup of an already-settled generated component fixture. This
// never resumes its intent, reads projections or changes Kubernetes ownership.
func TestLiveRetainedVersionedCredentialCleanup(t *testing.T) {
	if os.Getenv("CATC_RETAINED_CREDENTIAL_CLEANUP") != "settled-generated-snmp" {
		t.Skip("explicit retained fixture cleanup disabled")
	}
	dir := os.Getenv("CATC_RETAINED_CREDENTIAL_CHECKPOINT")
	target := os.Getenv("CATC_RETAINED_CREDENTIAL_UUID")
	task := os.Getenv("CATC_RETAINED_CREDENTIAL_TASK")
	if !filepath.IsAbs(dir) || !strings.HasPrefix(dir, "/private/tmp/cvk-global-credential-recovery-") || !validAPIID(target) || !validAPIID(task) {
		t.Fatal("exact retained fixture identities required")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal("checkpoint unavailable")
	}
	var saved struct {
		Controller cvk.NetworkController
		Config     configapi.NetworkControllerConfig
	}
	if json.Unmarshal(raw, &saved) != nil {
		t.Fatal("invalid checkpoint")
	}
	state := saved.Controller.Status.Configuration
	if state == nil || state.Operation != nil || len(state.CredentialApplications) != 1 || saved.Config.Status.LastAppliedTime == nil || saved.Config.Spec.ModelSource.Exporter != CredentialDefaultsExporter {
		t.Fatal("settled matching producer required")
	}
	wants, err := parseGlobalCredentials(saved.Config.Spec.Source.Inline.Raw, true)
	if err != nil || len(wants) != 1 || wants[0].kind.model != "snmpv3_credentials" || !strings.HasPrefix(wants[0].metadata.Name, "CVK-SNMP-Recover-") {
		t.Fatal("generated SNMP scope required")
	}
	want := wants[0]
	receipt := state.CredentialApplications[0]
	if receipt.TargetID != target || receipt.ConfigUID != string(saved.Config.UID) || receipt.Resource != want.resource() || receipt.Section != "network_settings" || !configurationOperationID.MatchString(receipt.OperationID) || len(receipt.Versions) != len(want.credentials) {
		t.Fatal("retained UUID receipt mismatch")
	}
	for _, leaf := range want.credentials {
		found := false
		for _, v := range receipt.Versions {
			if v.Path == leaf.path && v.Version == leaf.version {
				found = true
			}
		}
		if !found {
			t.Fatal("retained versions mismatch")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	observer := &installedCredentialObserver{cleanup: target}
	c.mu.Lock()
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		observer.base = base
		return observer
	}
	c.mu.Unlock()
	done, err := c.configurationTaskTreeComplete(ctx, task, saved.Config.Status.LastAppliedTime.Add(-5*time.Minute), time.Now())
	if err != nil || !done {
		t.Fatal("original task not terminal", err)
	}
	before, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := before[want.kind.model][want.metadata.Name]
	if !ok || got.ID != target || !globalCredentialMatches(want.metadata, got) {
		t.Fatal("generated native identity changed")
	}
	submitted := time.Now()
	reply, err := c.mutate(ctx, http.MethodDelete, globalCredentialsPath+"/"+target, nil)
	if err != nil {
		t.Fatal("cleanup uncertain", err)
	}
	var ack struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(reply, &ack) != nil || !validAPIID(ack.Response.ID) {
		t.Fatal("cleanup acknowledgement missing")
	}
	checkpoint := map[string]any{"target": target, "taskID": ack.Response.ID, "submittedAt": submitted, "originalTask": task}
	encoded, _ := json.MarshalIndent(checkpoint, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "supervised-cleanup.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := c.configurationTaskTreeComplete(ctx, ack.Response.ID, submitted, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("cleanup remains pending")
		case <-time.After(time.Second):
		}
	}
	after, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	delete(before[want.kind.model], want.metadata.Name)
	if !reflect.DeepEqual(before, after) || observer.deletes != 1 {
		t.Fatal("cleanup changed unrelated metadata")
	}
	checkpoint["exactTargetAbsent"] = true
	checkpoint["unrelatedMetadataUnchanged"] = true
	encoded, _ = json.MarshalIndent(checkpoint, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "supervised-cleanup.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("exact settled generated UUID removed; unrelated metadata unchanged", target, ack.Response.ID)
}
