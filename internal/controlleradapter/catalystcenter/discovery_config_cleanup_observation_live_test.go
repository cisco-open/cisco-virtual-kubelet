//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func qualifyLiveDiscoveryCleanupObservation(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, id, dir string) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Separate simulated API state models the deletion request. The original
	// qualification config/status and every real cluster are left unchanged.
	deleting := c.DeepCopy()
	deleting.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"test.cisco.vk/observation-only"}
	if os.Getenv("CATC_DISCOVERY_CLEANUP_COMPLETION") == "existing-lab-single" {
		deleting.Finalizers = append(deleting.Finalizers, discoveryConfigFinalizer)
	}
	drain := os.Getenv("CATC_DISCOVERY_DELETION_DRAIN") == "existing-lab-single"
	if drain {
		state := nc.Status.Configuration
		if state == nil || state.Operation != nil || len(state.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || state.ActionApplications[0].Operation.Action != "Create" {
			t.Fatal("drain requires original owned create receipt")
		}
		nc = nc.DeepCopy()
		op := nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
		op.Phase = "Accepted"
		nc.Status.Configuration.Operation = op
		nc.Status.Configuration.ActionApplications = nil
		deleting.Generation = op.ConfigGeneration + 1
	}
	api := fake.NewClientBuilder().WithScheme(a.statusWriter.Scheme()).WithStatusSubresource(&cvk.NetworkController{}, &configapi.NetworkControllerConfig{}).WithObjects(nc.DeepCopy(), deleting).Build()
	// Read back the stored representation, including timestamp normalization.
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(deleting), deleting); err != nil {
		t.Fatal(err)
	}
	observer := *a
	observer.statusReader = api
	observer.statusWriter = api
	if drain {
		qualifyLiveDiscoveryDeletionDrain(t, &observer, deleting, id, dir)
	}
	before := journalState(t, &observer).DeepCopy()
	observation, err := observer.observeDiscoveryConfigCleanup(ctx, deleting)
	if err != nil || observation == nil || len(observation.Candidates) != 1 || observation.Candidates[0].ID != id || !reflect.DeepEqual(before, journalState(t, &observer)) {
		t.Fatal("native cleanup prerequisite failed", err)
	}
	raw, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cleanup-observation.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("initial cleanup ownership observation performed no mutation; public finalizer and installed API remain unchanged", id)
	if os.Getenv("CATC_DISCOVERY_CLEANUP_PREPARATION") == "existing-lab-single" {
		op, err := observer.prepareDiscoveryConfigCleanup(ctx, deleting)
		if err != nil || op == nil || op.Phase != "Prepared" || op.TargetID != id || op.TaskID != "" || op.SubmittedAt != nil {
			t.Fatal("native-backed cleanup preparation failed", err)
		}
		source, err := loadControllerSource(ctx, api, deleting)
		if err != nil {
			t.Fatal(err)
		}
		if err := observer.startConfiguration(ctx, deleting, source, op.ID); err == nil {
			t.Fatal("native-backed Prepared operation enabled dispatch")
		}
		raw, err := json.MarshalIndent(op, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cleanup-preparation.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("native-backed deletion-bound Prepared persisted in separate simulated API; submission blocked", id)
		if os.Getenv("CATC_DISCOVERY_CLEANUP_EXECUTION") == "existing-lab-single" {
			qualifyLiveDiscoveryCleanupExecution(t, &observer, deleting, id, dir)
			if os.Getenv("CATC_DISCOVERY_CLEANUP_COMPLETION") == "existing-lab-single" {
				qualifyLiveDiscoveryConfigCompletion(t, &observer, deleting, id, dir)
			}
		}

	}

}

func qualifyLiveRetainedDiscoveryCleanupObservation(t *testing.T, c *client, source []byte, state *cvk.NetworkControllerConfigurationStatus, id, dir string) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	a.client = c
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline.Raw = source
	config.Generation = state.ActionApplications[0].Operation.ConfigGeneration
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state.DeepCopy()
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	qualifyLiveDiscoveryCleanupObservation(t, a, config, id, dir)
}

// The original qualification transport permits only one exact-ID delete. All
// Kubernetes recovery faults below are injected in separate simulated state.
func qualifyLiveDiscoveryCleanupExecution(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, id, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	save := func(name string, value any) {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	injected := false
	var verified *cvk.NetworkControllerConfigurationStatus
	for ctx.Err() == nil {
		state := journalState(t, a).DeepCopy()
		save("cleanup-execution-journal.json", state)
		if state.Operation == nil {
			if verified == nil || !injected || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.TargetID != id || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) {
				t.Fatal("native deletion receipt or task recovery missing")
			}
			save("cleanup.json", map[string]any{"id": id, "deleted": true, "deleteTaskId": state.ActionApplications[0].Operation.TaskID, "automaticPrivateLifecycle": true, "injectedKnownTaskRecovery": true})
			t.Log("private deletion-bound exact delete, injected Unknown original-task recovery, absence receipt and journal settlement verified", id)
			return
		}
		if state.Operation.Phase == "Accepted" && !injected {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			injected = true
			save("cleanup-execution-unknown.json", journalState(t, a))
		}
		if state.Operation.Phase == "Verified" {
			verified = state
			save("cleanup-execution-verified.json", state)
		}
		// No closure or remembered dispatch capability crosses this boundary.
		restarted := *a
		a = &restarted
		if err := a.advanceDiscoveryConfigCleanup(ctx, c); err != nil && !errors.Is(err, errConfigurationBusy) {
			t.Fatal("private cleanup retained for inspection; no replay", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	t.Fatal("private cleanup timed out; exact journal retained, no replay")
}

// Reconstruct the original acknowledged create as outstanding in a separate
// simulated API, then drain only its real native task/UUID. The deletion
// generation and Unknown fault are test-only; no new create is authorized.
func qualifyLiveDiscoveryDeletionDrain(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, id, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	original := journalState(t, a).Operation.DeepCopy()
	if original == nil || original.Phase != "Accepted" || original.ConfigGeneration+1 != c.Generation {
		t.Fatal("original drain binding unavailable")
	}
	if err := a.advanceConfiguration(ctx, original.ID, "Unknown", ""); err != nil {
		t.Fatal(err)
	}
	save := func(name string, value any) {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save("deletion-drain-unknown.json", journalState(t, a))
	for ctx.Err() == nil {
		state := journalState(t, a).DeepCopy()
		save("deletion-drain-journal.json", state)
		if state.Operation == nil {
			if len(state.ActionApplications) != 1 || len(state.Owners) != 1 || state.ActionApplications[0].Operation.ID != original.ID || state.ActionApplications[0].Operation.TaskID != original.TaskID || state.ActionApplications[0].Operation.ConfigGeneration != original.ConfigGeneration || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || !strings.HasSuffix(state.ActionApplications[0].Resource, ":"+id) {
				t.Fatal("original task/generation/UUID receipt not preserved")
			}
			save("deletion-drain-complete.json", map[string]any{"originalTaskId": original.TaskID, "originalGeneration": original.ConfigGeneration, "deletingGeneration": c.Generation, "id": id, "nativeMutations": 0, "simulatedDeletionAndUnknown": true})
			t.Log("original native create task and UUID drained across simulated deletion generation; observation only", id)
			return
		}
		restarted := *a
		a = &restarted
		if err := a.drainDiscoveryConfigOperation(ctx, c); err != nil && !errors.Is(err, errConfigurationBusy) {
			t.Fatal("pre-deletion task retained for inspection", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	t.Fatal("original task drain timed out; no replay")
}

func qualifyLiveDiscoveryConfigCompletion(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, id, dir string) {
	t.Helper()
	ctx := context.Background()
	save := func(name string, value any) {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.publishDiscoveryConfigCompletion(ctx, c); err != nil {
		t.Fatal("native completion proof unavailable", err)
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !discoveryDeletionCompleted(nc, &fresh) || len(fresh.Finalizers) != 2 {
		t.Fatal("native proof not metadata-verifiable or finalizer changed")
	}
	save("completion-status.json", fresh.Status)
	// Repeat against the real native catalogue with no retained local ownership.
	// Existing lab discoveries are foreign; none may be adopted or deleted.
	unowned := nc.DeepCopy()
	unowned.Status.Configuration = nil
	config := fresh.DeepCopy()
	config.Status = configapi.NetworkControllerConfigStatus{}
	api := fake.NewClientBuilder().WithScheme(a.statusWriter.Scheme()).WithStatusSubresource(&cvk.NetworkController{}, &configapi.NetworkControllerConfig{}).WithObjects(unowned, config).Build()
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	observer := *a
	observer.statusReader = api
	observer.statusWriter = api
	if err := observer.publishDiscoveryConfigCompletion(ctx, config); err != nil {
		t.Fatal("unowned native completion failed", err)
	}
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	current, err := observer.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status.Configuration != nil || !discoveryDeletionCompleted(current, config) {
		t.Fatal("unowned completion claimed ownership")
	}
	save("unowned-completion-status.json", config.Status)
	t.Log("native closed and unowned completion proofs verified; status only, finalizers retained", id)
}
