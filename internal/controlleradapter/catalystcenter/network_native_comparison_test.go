// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"path/filepath"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
)

func configureNetworkNativeComparisonFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "comparison", Name: "network-native-comparison", Key: "key"}}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Use: "PrivateComparisonKey", Section: "network_settings", Path: networkComparisonScope, Source: "comparison"}}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	relative, err := contract.IntentSecretRelativePath(contract.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: networkComparisonScope, SourceAlias: "comparison", SecretName: "network-native-comparison", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded := []byte(hex.EncodeToString(key))
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path, encoded
}

func exerciseNetworkNativeComparison(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, "telemetry")
	a.client = e.client
	var initial map[string]any
	if json.Unmarshal([]byte(telemetryResponse), &initial) != nil {
		t.Fatal("fixture")
	}
	f.data["area/telemetry"] = initial
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: telemetryPartialModel(t, map[string]any{"wired_data_collection": true}, "Global/Lab")}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	keyPath, key := configureNetworkNativeComparisonFixture(t, a, config)
	privateRefs := append([]configapi.NetworkControllerSecretRef{}, config.Spec.SecretRefs...)
	if mode == "legacy" {
		config.Spec.SecretRefs = nil
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "missing-state" {
		f.data = map[string]map[string]any{}
	}
	if mode == "unapproved-key" {
		config.Spec.SecretRefs[0].Source = "unapproved"
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "noop" {
		f.data["area/telemetry"]["wiredDataCollection"].(map[string]any)["enableWiredDataCollection"] = true
	}
	if mode == "lost-ack" {
		f.lostAck = true
	}
	if install != nil {
		install(a, config)
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
	}
	injected := false
	var post []byte
	originalResource := ""
	originalTask := ""
	for step := 0; step < 45; step++ {
		tick()
		state := journalState(t, a)
		if (mode == "missing-state" || mode == "unapproved-key") && step >= 5 {
			if f.writes != 0 || state != nil && (state.Operation != nil || len(state.ActionApplications) != 0) || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("unavailable state/authority granted a private operation")
			}
			if mode == "unapproved-key" && f.siteReads != 0 {
				t.Fatal("unapproved key reached native inventory")
			}
			return
		}
		if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !injected {
			if mode != "legacy" {
				if _, private := networkPrivateParts(state.Operation.Resource); !private {
					t.Fatal("private telemetry lost v2 proof")
				}
			}
			originalResource, originalTask = state.Operation.Resource, state.Operation.TaskID
			post, _ = json.Marshal(f.data["area/telemetry"])
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			injected = true
			switch mode {
			case "companion", "restore-companion":
				f.data["area/telemetry"]["wirelessTelemetry"].(map[string]any)["enableWirelessTelemetry"] = false
			case "key", "restore-key":
				if err := os.WriteFile(keyPath, []byte(strings.Repeat("cd", 32)), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			case "proof", "downgrade":
				nc, _ := a.currentController(ctx)
				parts, ok := networkPrivateParts(nc.Status.Configuration.Operation.Resource)
				if !ok {
					t.Fatal("v2 proof missing")
				}
				if mode == "proof" {
					parts[3] = strings.Repeat("f", 64)
					nc.Status.Configuration.Operation.Resource = networkPrivateUpdatePrefix + strings.Join(parts, ":")
				} else {
					nc.Status.Configuration.Operation.Resource = networkSettingUpdatePrefix + strings.Join(parts[:2], ":")
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
		}
		failure := mode == "companion" || mode == "restore-companion" || mode == "key" || mode == "restore-key" || mode == "missing-key" || mode == "proof" || mode == "downgrade" || mode == "lost-ack"
		if failure && step >= 8 {
			state = journalState(t, a)
			if f.writes != 1 || state.Operation == nil || state.Operation.Phase != "Unknown" || len(state.ActionApplications) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("unproven native baseline settled or replayed")
			}
			if (state.Operation.TaskID == "") != (mode == "lost-ack") {
				t.Fatal("taskless ambiguity gained task identity")
			}
			if mode == "restore-companion" {
				var restored map[string]any
				_ = json.Unmarshal(post, &restored)
				f.data["area/telemetry"] = restored
				mode = "complete"
			} else if mode == "restore-key" {
				if err := os.WriteFile(keyPath, key, 0600); err != nil {
					t.Fatal(err)
				}
				mode = "complete"
			} else {
				return
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			continue
		}
		state = journalState(t, a)
		if mode == "noop" {
			if f.writes != 0 || len(state.ActionApplications) != 0 || state.Operation != nil {
				t.Fatal("private no-op invented baseline or effects")
			}
			return
		}
		if !injected || f.writes != 1 || state.Operation != nil || len(state.ActionApplications) != 1 {
			t.Fatal("private baseline did not settle")
		}
		row := state.ActionApplications[0]
		if row.Operation.Resource != originalResource || row.Operation.TaskID != originalTask {
			t.Fatal("original task proof was rebased")
		}
		if mode != "legacy" && !strings.HasPrefix(row.Resource, networkPrivateAppliedPrefix) {
			t.Fatal("private completion downgraded result identity")
		}
		status, _ := json.Marshal(state)
		if strings.Contains(string(status), string(key)) || strings.Contains(string(status), "192.0.2.90") || strings.Contains(string(status), "192.0.2.1") {
			t.Fatal("key/native values leaked into public status")
		}
		before := state.DeepCopy()
		switch mode {
		case "drift":
			f.data["area/telemetry"]["wirelessTelemetry"].(map[string]any)["enableWirelessTelemetry"] = false
			for range 4 {
				tick()
			}
			if f.writes != 1 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || !reflect.DeepEqual(before.ActionApplications, journalState(t, a).ActionApplications) {
				t.Fatal("latest native baseline silently rebased")
			}
			return
		case "drop-key":
			config.Spec.SecretRefs = nil
			config.Generation++
		case "legacy":
			config.Spec.SecretRefs = privateRefs
			config.Generation++
		case "replace":
			config.Spec.Source.Inline.Raw = telemetryPartialModel(t, map[string]any{"wired_data_collection": false}, "Global/Lab")
			config.Generation++
		case "retarget":
			for i := range f.sites {
				if f.sites[i].ID == "area" {
					f.sites[i].Name = "Renamed"
					f.sites[i].NameHierarchy = "Global/Renamed"
				} else if strings.HasPrefix(f.sites[i].NameHierarchy, "Global/Lab/") {
					f.sites[i].NameHierarchy = strings.Replace(f.sites[i].NameHierarchy, "Global/Lab/", "Global/Renamed/", 1)
				}
			}
			config.Spec.Source.Inline.Raw = telemetryPartialModel(t, map[string]any{"wired_data_collection": false}, "Global/Renamed")
			config.Generation++
		default:
			for range 3 {
				tick()
			}
			if f.writes != 1 || journalState(t, a).Operation != nil {
				t.Fatal("private no-op replayed")
			}
			return
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 15 {
			tick()
		}
		current := journalState(t, a)
		if mode == "replace" {
			if f.writes != 2 || current.Operation != nil || len(current.ActionApplications) != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("private replacement did not converge")
			}
			newer := current.ActionApplications[0]
			if newer.Operation.TaskID == row.Operation.TaskID || newer.Operation.Resource == row.Operation.Resource || !strings.HasPrefix(newer.Resource, networkPrivateAppliedPrefix) || newer.Operation.TargetID != row.Operation.TargetID {
				t.Fatal("replacement lost full native/task/target lineage")
			}
		} else if f.writes != 1 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || !reflect.DeepEqual(before.ActionApplications, current.ActionApplications) {
			t.Fatal("key removal or legacy adoption bypassed baseline custody")
		}
		return
	}
	t.Fatal("private network flow did not converge")
}

func TestNetworkNativeComparisonPublic(t *testing.T) {
	for _, mode := range []string{"complete", "companion", "restore-companion", "key", "restore-key", "missing-key", "proof", "downgrade", "lost-ack", "noop", "drift", "drop-key", "legacy", "replace", "retarget", "missing-state", "unapproved-key"} {
		t.Run(mode, func(t *testing.T) { exerciseNetworkNativeComparison(t, mode, nil) })
	}
}

func TestNetworkNativeMACBindings(t *testing.T) {
	e, f := networkSubmissionFixtureExecutor(t, "telemetry")
	var state map[string]any
	if json.Unmarshal([]byte(telemetryResponse), &state) != nil {
		t.Fatal("fixture")
	}
	f.data["area/telemetry"] = state
	observed, err := e.observeSubmission(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}
	var want networkSetting
	for _, candidate := range e.bindings[0].settings {
		if candidate.kind == "telemetry" {
			want = candidate
		}
	}
	submission, err := resolveTelemetrySubmission(want, observed.effective["Global/Lab#telemetry"])
	if err != nil {
		t.Fatal(err)
	}
	resource, err := networkPrivateResource(context.Background(), comparison, "Global/Lab", strings.Repeat("b", 64), observed, submission)
	if err != nil {
		t.Fatal(err)
	}
	observed.effective["Global/Lab#telemetry"] = networkObservation{telemetry: cloneNetworkTelemetry(submission.telemetry)}
	for _, mode := range []string{"same", "companion", "target", "ancestor", "cohort", "key", "scope", "invalid-key-id"} {
		t.Run(mode, func(t *testing.T) {
			fresh := networkSubmissionObservation{sites: map[string]networkSubmissionSite{}, effective: map[string]networkObservation{"Global/Lab#telemetry": cloneNetworkObservation(observed.effective["Global/Lab#telemetry"])}}
			for path, site := range observed.sites {
				fresh.sites[path] = site
			}
			cmp := *comparison
			selected := resource
			path := "Global/Lab"
			switch mode {
			case "companion":
				fresh.effective["Global/Lab#telemetry"].telemetry["wirelessTelemetry"]["enableWirelessTelemetry"] = false
			case "target":
				site := fresh.sites[path]
				site.id = "replaced"
				fresh.sites[path] = site
			case "ancestor":
				root := fresh.sites["Global"]
				root.id = "replaced"
				fresh.sites["Global"] = root
				child := fresh.sites[path]
				child.parent = root.id
				fresh.sites[path] = child
			case "cohort":
				parts, _ := networkPrivateParts(resource)
				parts[1] = strings.Repeat("c", 64)
				selected = networkPrivateUpdatePrefix + strings.Join(parts, ":")
			case "key":
				cmp.key = []byte(strings.Repeat("z", 32))
			case "scope":
				path = "Global/Other"
			case "invalid-key-id":
				cmp.keyID = "invalid"
			}
			matched, err := networkMatchPrivate(context.Background(), &cmp, selected, path, fresh)
			if mode == "same" {
				if err != nil || !matched {
					t.Fatal("identical complete state did not match", err)
				}
			} else if err == nil && matched {
				t.Fatal("private proof accepted changed authority/native context")
			}
		})
	}
	for _, bad := range []string{networkPrivateUpdatePrefix + "bad", resource + ":extra", strings.Replace(resource, "v2:", "v3:", 1)} {
		if _, _, ok := networkSettingUpdateParts(bad); ok {
			t.Fatal("malformed/future private envelope accepted")
		}
	}
}

func TestNetworkNativeComparisonExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/telemetry-native-baseline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var example configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &example); err != nil {
		t.Fatal(err)
	}
	if !networkComparisonConfig(&example) || !supportedConfigurationApply(&example) {
		t.Fatal("example violates comparison contract")
	}
	a, config := journalFixture(t)
	config.Spec = example.Spec
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	configureNetworkNativeComparisonFixture(t, a, config)
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.compileNetworkComparisonApply(nc, config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
	if err != nil || networkSettingsExecutor(plan.executor).comparison == nil {
		t.Fatal("example does not compile using approved projection", err)
	}
}
