// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var siteRetirementBindingModes = []string{"stable", "latest", "retain", "delete-config", "legacy", "wrong-key", "same-source", "declared", "foreign-owner", "foreign-receipt", "missing-creator", "bad-latest", "effect-row", "credential-row", "pending", "native-drift", "missing", "dispatch-source", "dispatch-owner", "dispatch-history", "dispatch-key", "dispatch-pause", "dispatch-target", "dispatch-resource", "dispatch-task", "dispatch-phase", "dispatch-plan", "dispatch-caller-memory", "dispatch-late-owner", "dispatch-late-native", "dispatch-late-phase"}

func TestSiteRetirementBinding(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range siteRetirementBindingModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSiteRetirementBinding(t, mode, cm, nil) })
		}
	}
}

func exerciseSiteRetirementBinding(t *testing.T, mode string, configMap bool, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	if install != nil {
		install(a, config)
	}
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	config.Generation++
	const empty = `{"catalyst_center":{"sites":{"areas":[]}}}`
	cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "retirement-model", Namespace: config.Namespace}, Data: map[string]string{"model.json": empty}}
	oldVersion := ""
	if configMap {
		if install == nil {
			cm.UID = "source"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		oldVersion = cm.ResourceVersion
		cm.Data["marker"] = "new source observation"
		if err := a.statusWriter.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
	} else {
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(empty)}
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	keyPath, _ := configureSiteNativeComparisonFixture(t, a, config)
	// The configured projection changes the controller generation in a real
	// API. Start the fixture actor at that new identity, as a new worker would.
	var configured cvk.NetworkController
	if err := a.statusReader.Get(ctx, a.key, &configured); err != nil {
		t.Fatal(err)
	}
	a.generation = configured.Generation
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	target := Site{ID: siteOwnedTestUUID, Name: "Probe", NameHierarchy: "Global/Probe", ParentID: "root", Type: "area"}
	extra := ""
	recovery := &siteRetirementRecoveryFixture{mode: strings.TrimPrefix(mode, "recovery-")}
	dispatch := &siteRetirementDispatchFixture{mode: strings.TrimPrefix(mode, "write-")}
	inject := false
	writes := 0
	injected := false
	updateState := func(change func(*cvk.NetworkController)) {
		t.Helper()
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		change(current)
		if err := a.statusWriter.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if dispatch.active && dispatch.handle(t, w, r) {
			return
		}
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(500)
			return
		}
		if recovery.active && recovery.handle(t, w, r) {
			return
		}
		if serveEmptySiteRetirementConsumers(t, w, r, target.ID) {
			return
		}
		if inject && !injected && r.URL.Path == sitesPath {
			injected = true
			switch mode {
			case "dispatch-late-owner":
				updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Owners[0].ConfigUID = "foreign" })
			case "dispatch-late-phase":
				updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" })
			case "dispatch-late-native":
				extra = "private mutation"
			}
		}
		if r.URL.Path == pnpDevicesPath {
			fmt.Fprint(w, `[]`)
			return
		}
		rows := []any{}
		switch r.URL.Path {
		case sitesPath:
			rows = append(rows, Site{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"})
			if mode != "missing" {
				raw, _ := json.Marshal(target)
				var native map[string]any
				json.Unmarshal(raw, &native)
				if extra != "" {
					native["nativePrivateField"] = extra
				}
				rows = append(rows, native)
			}
		case assignedDevicesPath, networkProfilesPath, wirelessProfilesPath, fabricSitesPath, fabricZonesPath, sitePoolsPath:
		default:
			t.Errorf("unexpected retirement binding read %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	actual, err := a.client.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creator := siteCreatorTestRecord()
	creator.Operation.ConfigName = config.Name
	creator.Operation.ConfigUID = string(config.UID)
	creator.Operation.ControllerGeneration = nc.Generation
	creator.Operation.ConfigGeneration = config.Generation - 1
	if configMap {
		creator.Operation.ConfigGeneration = config.Generation
		creator.Operation.SourceUID = string(cm.UID)
		creator.Operation.SourceVersion = oldVersion
	}
	creator.Operation.Resource = sitePrivateCreateResource(desiredSite{kind: "area", path: target.NameHierarchy}, config, comparison)
	native, err := a.client.siteNativeFootprint(ctx, actual, target.NameHierarchy, false)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := siteNativeMAC(comparison, creator.Operation.Resource, native)
	if err != nil {
		t.Fatal(err)
	}
	creator.Resource = siteCreatedResource(&creator.Operation, "area", target.ID, mac)
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "sites", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	if strings.Contains(mode, "child") {
		childID := "22222222-2222-4222-8222-222222222222"
		child := retirementDependentTestRows(t, state.Owners, creator, target.ID, childID, target.NameHierarchy+"/Child", "e", "f", config.Generation, -80*time.Millisecond)
		if mode == "write-live-child" {
			child = child[:1]
		}
		state.ActionApplications = append(state.ActionApplications, child...)
		if strings.Contains(mode, "grandchild") {
			grand := retirementDependentTestRows(t, state.Owners, creator, childID, "33333333-3333-4333-8333-333333333333", target.NameHierarchy+"/Child/Grandchild", "8", "9", config.Generation, -200*time.Millisecond)
			state.ActionApplications = append(state.ActionApplications, grand...)
		}
	}
	if mode == "latest" || mode == "bad-latest" || mode == "recovery-latest" || mode == "recovery-latest-pruned" || mode == "recovery-receipt-latest" || mode == "write-latest" || mode == "write-recreate-latest" {
		extra = "private applied field"
		actual, err = a.client.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		native, err = a.client.siteNativeFootprint(ctx, actual, target.NameHierarchy, false)
		if err != nil {
			t.Fatal(err)
		}
		mac, err = siteNativeMAC(comparison, creator.Operation.Resource, native)
		if err != nil {
			t.Fatal(err)
		}
		op := creator.Operation.DeepCopy()
		op.ID = strings.Repeat("c", 32)
		op.Action = "Update"
		op.TargetID = target.ID
		op.TaskID = "latest-task"
		stamp := metav1.NewTime(time.Now().Add(-100 * time.Millisecond))
		op.SubmittedAt = &stamp
		op.Resource = sitePrivateUpdatePrefix + strings.TrimPrefix(creator.Operation.Resource, sitePrivateCreatePrefix) + ":" + siteReceiptDigest(creator)
		records, err := siteCreatedRecords(state)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := siteCreatorForResource(records, op.Resource)
		row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteAppliedResult(op, original, mac), Operation: *op, VerifiedAt: &stamp}
		if mode == "bad-latest" {
			row.Operation.TaskID = ""
		}
		state.ActionApplications = append(state.ActionApplications, row)
	}
	switch mode {
	case "legacy":
		row := creator.DeepCopy()
		row.Operation.Resource = target.NameHierarchy
		row.Resource = siteCreatedResource(&row.Operation, "area", target.ID)
		state.ActionApplications[0] = *row
	case "wrong-key":
		row := creator.DeepCopy()
		row.Operation.Resource = strings.Replace(row.Operation.Resource, comparison.keyID, strings.Repeat("e", 64), 1)
		row.Resource = siteCreatedResource(&row.Operation, "area", target.ID, mac)
		state.ActionApplications[0] = *row
	case "foreign-owner":
		state.Owners[0].ConfigUID = "foreign"
	case "foreign-receipt":
		state.ActionApplications[0].Operation.ConfigUID = "foreign"
	case "missing-creator":
		state.ActionApplications = nil
	case "same-source":
		state.ActionApplications[0].Operation.ConfigGeneration = config.Generation
		state.ActionApplications[0].Operation.SourceVersion = source.version
	case "effect-row":
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: "retained-other-effect", Operation: cvk.NetworkControllerConfigurationOperation{TargetID: target.ID}})
	case "credential-row":
		state.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: "credential", ConfigUID: string(config.UID), TargetID: target.ID}}
	case "pending":
		state.Operation = &cvk.NetworkControllerConfigurationOperation{Phase: "Unknown"}
	case "native-drift":
		extra = "private drift"
	}
	if mode == "retain" {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if mode == "delete-config" {
		config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	if mode == "declared" {
		if configMap {
			cm.Data["model.json"] = `{"catalyst_center":{"sites":{"areas":[{"name":"Probe","parent_name":"Global"}]}}}`
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"sites":{"areas":[{"name":"Probe","parent_name":"Global"}]}}}`)
		}
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	source, err = loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(mode, "write-") {
		dispatch.extra = extra
		dispatch.execute(t, a, config, source, keyPath, cm, updateState)
		return
	}
	candidate, err := a.bindSiteRetirement(ctx, nc, config, source, target.ID)
	success := mode == "stable" || mode == "latest" || strings.HasPrefix(mode, "dispatch-") || strings.HasPrefix(mode, "recovery-")
	if (err == nil) != success {
		t.Fatal("retirement candidate binding mismatch", mode, err)
	}
	if !success {
		if writes != 0 {
			t.Fatal("binding mutated native state")
		}
		return
	}
	if !supportedConfigurationApply(config) {
		t.Fatal("qualified private site lifecycle lost manifest admission")
	}
	if strings.Contains(candidate.resource, "Probe") {
		t.Fatal("private site path published")
	}
	op, err := a.prepareConfiguration(ctx, config, source, "sites", candidate.resource, "Delete", candidate.identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, source, op.ID, candidate.identity); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "dispatch-source":
		if configMap {
			cm.Data["marker"] = "changed"
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
	case "dispatch-owner":
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Owners[0].ConfigUID = "foreign" })
	case "dispatch-history":
		updateState(func(n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed"
		})
	case "dispatch-key":
		if err := os.WriteFile(keyPath, []byte(strings.Repeat("f", 64)), 0600); err != nil {
			t.Fatal(err)
		}
	case "dispatch-pause":
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		current.Spec.Paused = true
		if err := a.statusWriter.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	case "dispatch-target":
		updateState(func(n *cvk.NetworkController) {
			n.Status.Configuration.Operation.TargetID = "22222222-2222-4222-8222-222222222222"
		})
	case "dispatch-resource":
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Resource += "x" })
	case "dispatch-task":
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.TaskID = "unrelated-task" })
	case "dispatch-phase":
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" })
	case "dispatch-plan":
		candidate.identity.target = "22222222-2222-4222-8222-222222222222"
	case "dispatch-caller-memory":
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
		source.raw = []byte(`corrupted caller memory`)
		nc.Status.Configuration.ActionApplications = nil
	}
	inject = true
	err = candidate.check(ctx)
	valid := mode == "stable" || mode == "latest" || mode == "dispatch-caller-memory" || strings.HasPrefix(mode, "recovery-")
	if (err == nil) != valid {
		t.Fatal("retirement submission fence mismatch", mode, err)
	}
	if writes != 0 {
		t.Fatal("binding/preflight performed native mutation")
	}
	if strings.HasPrefix(mode, "recovery-") {
		recovery.verify(t, a, config, source, keyPath, cm, updateState)
		if writes != 0 {
			t.Fatal("recovery performed native mutation")
		}
	}
}

func TestSiteRetirementSelector(t *testing.T) {
	base := siteRetirementPrefix + strings.Join([]string{"area", strings.Repeat("1", 64), "i", strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64)}, ":")
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("2", 64), check: func(context.Context) error { return nil }}
	mac, err := siteRetirementMAC(comparison, base, []byte("private native attributes"))
	if err != nil {
		t.Fatal(err)
	}
	ancestryMAC, err := siteRetirementAncestryMAC(comparison, base+":"+mac, []byte("private ancestor identities"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "configmap", "legacy-version", "wrong-version", "wrong-kind", "wrong-mode", "upper-digest", "truncated", "suffix", "source-pair", "target", "parent", "action", "prepared", "credentials", "physical", "bad-task"} {
		t.Run(mode, func(t *testing.T) {
			op := &cvk.NetworkControllerConfigurationOperation{Section: "sites", Action: "Delete", Resource: base + ":" + mac + ":" + ancestryMAC, TargetID: siteOwnedTestUUID, ParentID: "root"}
			switch mode {
			case "configmap":
				op.Resource = strings.Replace(op.Resource, ":i:", ":c:", 1)
				op.SourceUID, op.SourceVersion = "source", "revision"
			case "legacy-version":
				op.Resource = strings.Replace(op.Resource, ":v2:", ":v1:", 1)
			case "wrong-version":
				op.Resource = strings.Replace(op.Resource, ":v2:", ":v3:", 1)
			case "wrong-kind":
				op.Resource = strings.Replace(op.Resource, ":area:", ":device:", 1)
			case "wrong-mode":
				op.Resource = strings.Replace(op.Resource, ":i:", ":unknown:", 1)
			case "upper-digest":
				op.Resource = base + ":" + strings.Repeat("A", 64)
			case "truncated":
				op.Resource = base
			case "suffix":
				op.Resource += ":extra"
			case "source-pair":
				op.SourceUID = "unbound"
			case "target":
				op.TargetID = "invalid"
			case "parent":
				op.ParentID = op.TargetID
			case "action":
				op.Action = "Update"
			case "prepared":
				stamp := metav1.Now()
				op.PreparedAt = &stamp
			case "credentials":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			case "physical":
				op.TargetSerial = "FOC12345678"
			case "bad-task":
				op.TaskID = "bad task"
			}
			if siteRetirementOperation(op) != (mode == "valid" || mode == "configmap") {
				t.Fatal("retirement selector shape accepted incorrectly")
			}
		})
	}
	for _, mode := range []string{"same", "native", "creator", "key", "family"} {
		t.Run("mac-"+mode, func(t *testing.T) {
			selected, native, cmp := base, []byte("private native attributes"), *comparison
			switch mode {
			case "native":
				native = []byte("changed private attributes")
			case "creator":
				selected = strings.Replace(selected, strings.Repeat("3", 64), strings.Repeat("5", 64), 1)
			case "key":
				cmp.key = []byte(strings.Repeat("z", 32))
			case "family":
				selected = strings.Replace(selected, "site-retire:", "site-private-update:", 1)
			}
			got, err := siteRetirementMAC(&cmp, selected, native)
			if (err == nil && got == mac) != (mode == "same") {
				t.Fatal("retirement selector lost domain or predecessor binding")
			}
		})
	}
	candidate := &siteRetirementCandidate{resource: "private-selector", identity: configurationResourceIdentity{target: "private-target"}}
	if strings.Contains(fmt.Sprintf("%+v %#v", candidate, candidate), "private-selector") {
		t.Fatal("candidate leaked private internals")
	}
}
