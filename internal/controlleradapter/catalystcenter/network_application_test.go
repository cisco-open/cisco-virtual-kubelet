// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func networkApplicationChangedModel(t *testing.T, kind string) []byte {
	t.Helper()
	var model map[string]any
	if json.Unmarshal(networkSingleSettingModel(t, kind), &model) != nil {
		t.Fatal("model")
	}
	groups := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
	category := "network"
	if kind == "telemetry" {
		category = "telemetry"
	}
	profile := groups[category].([]any)[0].(map[string]any)
	switch kind {
	case "ntp", "dhcp":
		profile[kind+"_servers"] = []string{"192.0.2.9"}
	case "dns":
		profile["domain_name"] = "changed.example.test"
		profile["dns_servers"] = []string{"192.0.2.9"}
	case "timezone":
		profile["timezone"] = "Etc/UTC"
	case "banner":
		profile["banner"] = "PRIVATE_NEW_BANNER"
	case "telemetry":
		profile["wired_data_collection"] = true
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exerciseNetworkApplication(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, kind)
	a.client = e.client
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: networkSingleSettingModel(t, kind)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drop-companion" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "templates", Resource: "unrelated", Operation: networkApplicationForeignOperation(config, nc, "b")}}}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "preflight-history" || mode == "preflight-history-read-failure" {
		base := a.statusWriter
		injected := false
		a.statusWriter = networkSubmittingClient{Client: base, after: func() {
			if injected {
				return
			}
			injected = true
			if mode == "preflight-history-read-failure" {
				f.mu.Lock()
				f.failRead = true
				f.mu.Unlock()
			}
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: "concurrent", Operation: networkApplicationForeignOperation(config, nc, "c")})
			if err := base.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}}
		for range 4 {
			tick()
		}
		current := journalState(t, a)
		if !injected || current.Operation == nil || !networkSettingApplicationSelected(current.Operation) || current.Operation.TaskID != "" || f.writes != 0 {
			t.Fatal("lost history did not retain its original predecessor fence")
		}
		return
	}
	tick()
	state := journalState(t, a)
	if state == nil || state.Operation == nil || !networkSettingApplicationSelected(state.Operation) {
		t.Fatal("new public write lacks versioned identity")
	}
	original := state.Operation.DeepCopy()
	if mode == "legacy" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.Operation.Resource = networkSettingIdentity("Global/Lab", kind)
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "unknown" {
		if err := a.advanceConfiguration(ctx, original.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	state = journalState(t, a)
	if state.Operation.Phase != "Verifying" {
		t.Fatal("task not verifying")
	}
	if mode == "generic-verified" {
		if err := a.advanceConfiguration(ctx, original.ID, "Verified", ""); !errors.Is(err, errConfigurationTransition) {
			t.Fatal("generic transition bypassed receipt", err)
		}
	}
	if mode == "history-race" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: "concurrent", Operation: networkApplicationForeignOperation(config, nc, "c")})
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drop-receipt" || mode == "drop-seal" || mode == "drop-companion" || mode == "drop-row-prefix" || mode == "lost-ack" {
		a.statusWriter = &networkApplicationFaultClient{Client: a.statusWriter, mode: mode}
	}
	tick()
	state = journalState(t, a)
	blocked := mode == "drop-receipt" || mode == "drop-seal" || mode == "drop-companion" || mode == "drop-row-prefix" || mode == "history-race"
	if blocked {
		for range 3 {
			tick()
		}
		current := journalState(t, a)
		if current.Operation == nil {
			t.Fatal("unproven application journal cleared")
		}
		if current.Operation.Phase == "Verified" && a.finishConfiguration(ctx, current.Operation.ID) == nil {
			t.Fatal("missing receipt/cohort released journal")
		}
	} else {
		for range 5 {
			tick()
		}
		current := journalState(t, a)
		if current.Operation != nil {
			t.Fatal("receipt did not settle")
		}
		records, err := networkSettingApplicationRecords(current)
		if err != nil {
			t.Fatal(err)
		}
		expected := 1
		if mode == "legacy" {
			expected = 0
		}
		if len(records) != expected {
			t.Fatal("wrong applied record count", len(records))
		}
		if expected == 1 {
			row := records[networkSettingLogicalKey(networkSettingIdentity("Global/Lab", kind))]
			if row.Operation.ID != original.ID || row.Operation.TargetID != original.TargetID || row.Operation.TaskID != original.TaskID || row.Operation.Phase != "Verified" {
				t.Fatal("original receipt binding changed")
			}
			if mode == "replace" || mode == "replace-full" || mode == "target-replaced" || mode == "duplicate" || mode == "logical-duplicate" {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				config.Generation++
				config.Spec.Source.Inline.Raw = networkApplicationChangedModel(t, kind)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				if mode == "replace-full" {
					nc, _ := a.currentController(ctx)
					for i := 0; i < 255; i++ {
						foreign := networkApplicationForeignOperation(config, nc, "d")
						foreign.ID = fmt.Sprintf("%032x", i+1)
						nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: fmt.Sprintf("unrelated-%d", i), Operation: foreign})
					}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "target-replaced" {
					f.mu.Lock()
					f.sites[1].ID = "replacement"
					f.sites[2].ParentID = "replacement"
					f.mu.Unlock()
				}
				if mode == "duplicate" || mode == "logical-duplicate" {
					nc, _ := a.currentController(ctx)
					duplicate := *row.DeepCopy()
					if mode == "logical-duplicate" {
						key, _, _ := networkSettingUpdateParts(duplicate.Operation.Resource)
						duplicate.Resource = networkSettingAppliedPrefix + key + ":" + strings.Repeat("d", 64)
						duplicate.Operation.ID = strings.Repeat("e", 32)
					}
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, duplicate)
					err := a.statusWriter.Status().Update(ctx, nc)
					if install != nil && mode == "duplicate" {
						if !apierrors.IsInvalid(err) {
							t.Fatal("real API did not reject duplicate receipt key", err)
						}
						current, _ := a.currentController(ctx)
						records, readErr := networkSettingApplicationRecords(current.Status.Configuration)
						if readErr != nil || len(records) != 1 || f.writes != 1 {
							t.Fatal("duplicate admission changed retained history or native effects")
						}
						t.Log("actual API rejected duplicate application key before persistence")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				for range 7 {
					tick()
				}
				if mode == "replace" || mode == "replace-full" {
					newer := journalState(t, a)
					next, err := networkSettingApplicationRecords(newer)
					if err != nil || len(next) != 1 || newer.Operation != nil {
						t.Fatal("latest record replacement failed", err)
					}
					applied := next[networkSettingLogicalKey(networkSettingIdentity("Global/Lab", kind))]
					if applied.Operation.ID == row.Operation.ID || applied.Operation.TargetID != row.Operation.TargetID || applied.Operation.ConfigGeneration <= row.Operation.ConfigGeneration || applied.Operation.TaskID == row.Operation.TaskID {
						t.Fatal("latest receipt was not a new bound operation")
					}
				}
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := 1
	if mode == "replace" || mode == "replace-full" {
		writes = 2
	}
	if f.writes != writes {
		t.Fatal("unexpected native write count", f.writes)
	}
	raw, _ := json.Marshal(journalState(t, a))
	for _, private := range []string{"PRIVATE_NEW_BANNER", "Authorized lab access", "Europe/Berlin", "changed.example.test"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("native setting value leaked into receipt")
		}
	}
}

func TestNetworkApplications(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "unknown", "replace", "target-replaced", "duplicate", "logical-duplicate", "legacy", "generic-verified", "drop-receipt", "drop-seal", "drop-companion", "lost-ack", "history-race", "preflight-history", "preflight-history-read-failure", "replace-full", "drop-row-prefix"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkApplication(t, kind, mode, nil) })
		}
	}
}

type networkApplicationFaultClient struct {
	ctrlclient.Client
	mode string
	used bool
}
type networkApplicationFaultWriter struct {
	ctrlclient.SubResourceWriter
	parent *networkApplicationFaultClient
}

func (c *networkApplicationFaultClient) Status() ctrlclient.SubResourceWriter {
	return networkApplicationFaultWriter{c.Client.Status(), c}
}
func (w networkApplicationFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verified" || w.parent.used {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	w.parent.used = true
	copy := nc.DeepCopy()
	switch w.parent.mode {
	case "drop-receipt":
		copy.Status.Configuration.ActionApplications = nil
	case "drop-seal":
		for i := range copy.Status.Configuration.ActionApplications {
			if copy.Status.Configuration.ActionApplications[i].Operation.ID == copy.Status.Configuration.Operation.ID {
				key, _, _ := networkSettingUpdateParts(copy.Status.Configuration.ActionApplications[i].Operation.Resource)
				copy.Status.Configuration.ActionApplications[i].Resource = networkSettingAppliedPrefix + key
			}
		}
	case "drop-row-prefix":
		for i := range copy.Status.Configuration.ActionApplications {
			if copy.Status.Configuration.ActionApplications[i].Operation.ID == copy.Status.Configuration.Operation.ID {
				copy.Status.Configuration.ActionApplications[i].Resource = "stripped"
			}
		}
	case "drop-companion":
		rows := copy.Status.Configuration.ActionApplications[:0]
		for _, row := range copy.Status.Configuration.ActionApplications {
			if row.Section == "network_settings" {
				rows = append(rows, row)
			}
		}
		copy.Status.Configuration.ActionApplications = rows
	}
	if err := w.SubResourceWriter.Update(ctx, copy, opts...); err != nil {
		return err
	}
	if w.parent.mode == "lost-ack" {
		return errors.New("simulated lost receipt acknowledgement")
	}
	return nil
}

func TestNetworkApplicationWholeCohortCapacity(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, "ntp")
	a.client = e.client
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: allNetworkExecutionModel()}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, _ := a.currentController(ctx)
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	for i := 0; i < 251; i++ {
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: fmt.Sprintf("foreign-%d", i)})
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 0 || journalState(t, a).Operation != nil {
		t.Fatal("partial cohort submitted without complete receipt capacity")
	}
	if len(journalState(t, a).ActionApplications) != 251 {
		t.Fatal("capacity preflight changed unrelated receipts")
	}
}

func TestNetworkApplicationCohortSeal(t *testing.T) {
	// Explicit nil/empty fields normalize to the same public JSON. Values or
	// record/owner loss must still change the commit seal.
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_settings", ConfigUID: "owner"}}}
	digest := networkSettingBookkeepingDigest(state, "")
	copy := state.DeepCopy()
	copy.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{}
	if networkSettingBookkeepingDigest(copy, "") != digest {
		t.Fatal("empty JSON normalization changed seal")
	}
	copy.Owners[0].ConfigUID = "foreign"
	if reflect.DeepEqual(state, copy) || networkSettingBookkeepingDigest(copy, "") == digest {
		t.Fatal("owner loss did not change cohort seal")
	}
}

func networkApplicationForeignOperation(config *configapi.NetworkControllerConfig, nc *cvk.NetworkController, nonce string) cvk.NetworkControllerConfigurationOperation {
	at := metav1.NewTime(time.Now().UTC().Add(-time.Minute).Truncate(time.Second))
	return cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(nonce, 32), Section: "templates", Resource: "unrelated", Action: "Update", Phase: "Verified", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, TaskID: "foreign-task", TargetID: "foreign-project", SubmittedAt: &at}
}

func TestNetworkApplicationExistingConvergenceIsUnowned(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			e, f := networkSubmissionFixtureExecutor(t, kind)
			a.client = e.client
			config.Spec.ManagedSections = []string{"network_settings"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: networkSingleSettingModel(t, kind)}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			f.data["area/"+networkFixtureAPIKind(kind)] = networkFixturePayload(t, e.bindings[0].settings[0])
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			if f.writes != 0 || state.Operation != nil || len(state.ActionApplications) != 0 {
				t.Fatal("existing desired state was adopted as an executed application")
			}
		})
	}
}

func TestNetworkApplicationCompositionSelection(t *testing.T) {
	e, _ := networkSubmissionFixtureExecutor(t, "ntp")
	for _, shape := range []string{"direct", "section", "credentials", "aaa", "aaa-credentials"} {
		t.Run(shape, func(t *testing.T) {
			var executor configurationExecutor = e
			switch shape {
			case "section":
				executor = &networkSectionExecutor{settings: e}
			case "credentials":
				executor = &networkCredentialConfigurationExecutor{network: &networkSectionExecutor{settings: e}}
			case "aaa":
				executor = &aaaNetworkConfigurationExecutor{settings: &networkSectionExecutor{settings: e}}
			case "aaa-credentials":
				executor = &aaaNetworkConfigurationExecutor{settings: &networkCredentialConfigurationExecutor{network: &networkSectionExecutor{settings: e}}}
			}
			actual := networkSettingsExecutor(executor)
			if actual != e {
				t.Fatal("typed composition lost shared settings executor")
			}
			selected := networkSettingTaskSelections(executor)
			if selected[networkSettingLogicalKey(networkSettingIdentity("Global/Lab", "ntp"))] == nil {
				t.Fatal("declared setting absent from recovery/receipt selection")
			}
			if selected[networkSettingLogicalKey("Global/Lab#aaa")] != nil {
				t.Fatal("AAA credential mutation gained metadata receipt authority")
			}
		})
	}
}
