// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLiveCatalystCenterAAAOwnedReset(t *testing.T) {
	if os.Getenv("CATC_AAA_RESET_EXECUTION") != "owned-empty-area" {
		t.Skip("owned empty-area AAA reset not enabled")
	}
	TestLiveCatalystCenterAAASettingsManifest(t)
}

// Native reset qualification follows public positive application. This does not
// publish reset manifest syntax or claim common-journal cleanup finalization.
func qualifyLiveOwnedAAAReset(t *testing.T, c *client, ctx context.Context, a *adapter, config *configapi.NetworkControllerConfig, trace *liveAAASettingsTrace, checkScope func(context.Context)) {
	t.Helper()
	checkScope(ctx)
	state := journalState(t, a)
	if len(state.CredentialApplications) != 1 || state.Operation != nil {
		t.Fatal("reset needs verified parent ownership")
	}
	parentReceipt := state.CredentialApplications[0]
	parentPath := strings.TrimSuffix(parentReceipt.Resource, "#aaa")
	parentPolicy, err := c.readNetworkSetting(ctx, parentReceipt.TargetID, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	trace.childName = "AAA-Reset-Child"
	childPath := parentPath + "/" + trace.childName
	childDesired, err := parseSites([]byte(`{"catalyst_center":{"sites":{"areas":[{"name":"AAA-Reset-Child","parent_name":` + strconv.Quote(parentPath) + `}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range sites {
		if site.NameHierarchy == childPath {
			t.Fatal("reset child already exists")
		}
	}
	mutation, err := nextSiteMutation(childDesired, sites)
	if err != nil || mutation == nil {
		t.Fatal("reset child plan", err)
	}
	submittedAt := time.Now()
	task, err := c.submitSiteMutation(ctx, mutation)
	if err != nil {
		t.Fatal("reset child creation uncertain", err)
	}
	if err := waitSiteCredentialTask(ctx, c, task, submittedAt); err != nil {
		t.Fatal(err)
	}
	sites, err = c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range sites {
		if site.NameHierarchy == childPath && site.ParentID == parentReceipt.TargetID && site.Type == "area" {
			trace.childSiteID = site.ID
		}
	}
	if !validAPIID(trace.childSiteID) {
		t.Fatal("reset child identity missing")
	}
	t.Log("owned reset child created", childPath, trace.childSiteID)
	checkHierarchy := func(checkCtx context.Context) error {
		sites, err := c.ListSites(checkCtx)
		if err != nil {
			return err
		}
		found := 0
		for _, site := range sites {
			if site.ID == parentReceipt.TargetID || site.NameHierarchy == parentPath {
				if site.ID != parentReceipt.TargetID || site.NameHierarchy != parentPath || site.Type != "area" {
					return errConfigurationStale
				}
				found++
			}
			if strings.HasPrefix(site.NameHierarchy, parentPath+"/") {
				if site.ID != trace.childSiteID || site.NameHierarchy != childPath || site.ParentID != parentReceipt.TargetID || site.Type != "area" {
					return errConfigurationStale
				}
				found++
			}
		}
		if found != 2 {
			return errConfigurationStale
		}
		for _, id := range []string{parentReceipt.TargetID, trace.childSiteID} {
			assigned, err := c.assignedDevices(checkCtx, id)
			if err != nil {
				return err
			}
			if len(assigned) != 0 {
				return errConfigurationOwned
			}
		}
		current, err := c.readNetworkSetting(checkCtx, parentReceipt.TargetID, "aaa")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, parentPolicy) {
			return errConfigurationStale
		}
		return nil
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("reset child retained", childPath, trace.childSiteID)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := checkHierarchy(cleanup); err != nil {
			t.Error("reset child cleanup scope", err)
			return
		}
		submitted := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, "/dna/intent/api/v1/areas/"+trace.childSiteID, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("reset child cleanup receipt")
			return
		}
		if err := waitSiteCredentialTask(cleanup, c, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		sites, err := c.ListSites(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		for _, site := range sites {
			if site.ID == trace.childSiteID || site.NameHierarchy == childPath {
				t.Error("reset child deletion not verified")
				return
			}
		}
		t.Log("exact reset child UUID deletion verified")
	})
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal(config.Spec.Source.Inline.Raw, &root) != nil {
		t.Fatal("reset parent source")
	}
	areas := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["areas"].([]any)
	settings := root["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
	profiles := settings["aaa_servers"].([]any)
	encoded, err := json.Marshal(profiles[0])
	if err != nil {
		t.Fatal(err)
	}
	var childProfile map[string]any
	if json.Unmarshal(encoded, &childProfile) != nil {
		t.Fatal("child AAA profile")
	}
	childProfile["name"] = "child-owned"
	childProfile["network_aaa"].(map[string]any)["primary_ip"] = "192.0.2.241"
	childProfile["client_and_endpoint_aaa"].(map[string]any)["primary_ip"] = "192.0.2.240"
	settings["aaa_servers"] = append(profiles, childProfile)
	for _, group := range aaaGroups {
		alias := strings.ToLower(group.api)
		pointer := "/aaa_servers/1/" + group.model + "/shared_secret"
		config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: pointer, Source: alias})
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: pointer, SourceAlias: alias, SecretName: "isolated-aaa", SecretKey: alias})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(a.intentSecretPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, versionedSecretFixture(t, 2, trace.secrets[group.api]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	areas = append(areas, map[string]any{"name": trace.childName, "parent_name": parentPath, "network_settings": map[string]any{"aaa_servers": "child-owned"}})
	root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["areas"] = areas
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = raw
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	beforeChild := trace.writes.Load()
	converged := false
	for step := 0; step < 90; step++ {
		if err := checkHierarchy(ctx); err != nil {
			t.Fatal("reset hierarchy preflight", err)
		}
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		state = journalState(t, a)
		if step%20 == 0 {
			phase, task := "none", ""
			if state.Operation != nil {
				phase, task = state.Operation.Phase, state.Operation.TaskID
			}
			t.Log("child AAA reconciliation", step, config.Status.Phase, phase, task)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			converged = true
			break
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed || state.Operation != nil && (state.Operation.Phase == "Unknown" || state.Operation.Phase == "Failed") {
			t.Fatal("child AAA application retained; no reset")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !converged || state.Operation != nil || len(state.CredentialApplications) != 2 || trace.writes.Load() != beforeChild+1 {
		t.Fatal("child AAA ownership did not converge without parent replay")
	}
	var receipt ciskov1.NetworkControllerConfigurationCredentialApplication
	for _, record := range state.CredentialApplications {
		if record.TargetID == trace.childSiteID {
			receipt = record
		}
		if record.TargetID == parentReceipt.TargetID && !reflect.DeepEqual(record, parentReceipt) {
			t.Fatal("parent receipt changed")
		}
	}
	if receipt.TargetID == "" {
		t.Fatal("child AAA receipt missing")
	}
	ownedReceipts := state.DeepCopy().CredentialApplications
	config.Generation++
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	guard := func(checkCtx context.Context) error {
		if err := checkHierarchy(checkCtx); err != nil {
			return err
		}
		var current configapi.NetworkControllerConfig
		if err := a.statusReader.Get(checkCtx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
			return err
		}
		if current.UID != config.UID || current.Generation != config.Generation || !reflect.DeepEqual(current.Spec, config.Spec) {
			return errConfigurationStale
		}
		nc, err := a.currentController(checkCtx)
		if err != nil {
			return err
		}
		state := nc.Status.Configuration
		if state == nil || state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].Section != "network_settings" || state.Owners[0].ConfigUID != string(config.UID) || !reflect.DeepEqual(state.CredentialApplications, ownedReceipts) {
			return errConfigurationOwned
		}
		return nil
	}
	path := receipt.Resource[:len(receipt.Resource)-len("#aaa")]
	for index, want := range []aaaResetIntent{
		{site: path, network: aaaResetInherit, client: aaaResetInherit},
	} {
		before := trace.writes.Load()
		write, err := c.planOwnedAAAReset(ctx, config, want, receipt, guard)
		if err != nil || write == nil || len(write.identity.credentials) != 0 {
			t.Fatal("owned reset planning", err)
		}
		trace.reset = &want
		submitted := metav1.NewTime(time.Now().UTC())
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal("owned reset uncertain; retain without replay", err)
		}
		if trace.writes.Load() != before+1 {
			t.Fatal("reset request count")
		}
		if _, err := write.submit(ctx); err == nil || trace.writes.Load() != before+1 {
			t.Fatal("native reset replayed")
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Section: "network_settings", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, Phase: "Accepted", Resource: write.resource, Action: "Update", TargetID: receipt.TargetID, TaskID: task.ID, SubmittedAt: &submitted}
		raw, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		// JSON reconstruction is not Kubernetes journal/admission qualification.
		var restored ciskov1.NetworkControllerConfigurationOperation
		if json.Unmarshal(raw, &restored) != nil {
			t.Fatal("reset acknowledgement reconstruction")
		}
		verified := false
		for step := 0; step < 45; step++ {
			ok, err := c.verifyOwnedAAAReset(ctx, config, want, receipt, &restored, guard, time.Now().UTC())
			if err != nil {
				t.Fatal("owned reset task/readback verification; retain", err)
			}
			if ok {
				verified = true
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !verified {
			t.Fatal("native reset representation did not converge; retain")
		}
		if next, err := c.planOwnedAAAReset(ctx, config, want, receipt, guard); err != nil || next != nil {
			t.Fatal("verified native reset did not no-op", err)
		}
		if !reflect.DeepEqual(journalState(t, a).CredentialApplications, ownedReceipts) {
			t.Fatal("reset discarded or fabricated credential ownership")
		}
		t.Log("owned native AAA reset task/readback/serialized observation/no-op verified", index+1, want.network, want.client)
	}
	trace.reset = nil
	t.Log("native reset kept original credential receipt; public cleanup journal routing and finalization remain pending")
}
