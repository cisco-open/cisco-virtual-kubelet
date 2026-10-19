// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func exerciseNetworkTaskRecovery(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
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
	var sourceMap *corev1.ConfigMap
	if mode == "configmap" || mode == "configmap-race" {
		if err := corev1.AddToScheme(a.statusWriter.Scheme()); err != nil {
			t.Fatal(err)
		}
		sourceMap = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings-source", Namespace: config.Namespace}, Data: map[string]string{"model.json": string(config.Spec.Source.Inline.Raw)}}
		if install == nil {
			sourceMap.UID = types.UID("fixture-settings-source")
		}
		if err := a.statusWriter.Create(ctx, sourceMap); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: sourceMap.Name, Key: "model.json"}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	prepared := journalState(t, a)
	if prepared == nil || prepared.Operation == nil {
		t.Fatal("original setting submission did not create an operation")
	}
	op := prepared.Operation.DeepCopy()
	if op.Phase != "Accepted" || op.TaskID != "task" {
		t.Fatal("original request not acknowledged")
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
		t.Fatal(err)
	}
	changeJournal := func(change func(*cvk.NetworkController)) {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		change(nc)
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	switch mode {
	case "root-running", "root-failed", "foreign-root", "stale-root", "child-running", "child-failed", "disconnected", "foreign-tree":
		f.taskState = mode
	case "missing-task":
		changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Operation.TaskID = "" })
	case "undeclared":
		changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Operation.Resource = "Global/Lab#aaa" })
	case "wrong-action":
		changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Operation.Action = "Create" })
	case "wrong-target":
		changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Operation.TargetID = "foreign" })
	case "native-mismatch", "propagation":
		delete(f.data, "area/"+networkFixtureAPIKind(kind))
	case "native-race":
		f.afterTree = func() { delete(f.data, "area/"+networkFixtureAPIKind(kind)) }
	case "replacement-race":
		f.afterTree = func() { f.sites[1].ID = "replacement"; f.sites[2].ParentID = "replacement" }
	case "source-race":
		f.afterTree = func() {
			current := &configapi.NetworkControllerConfig{}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), current); err != nil {
				t.Error(err)
				return
			}
			current.Spec.Source.Inline.Raw = networkSingleSettingModel(t, "ntp")
			if kind == "ntp" {
				current.Spec.Source.Inline.Raw = networkSingleSettingModel(t, "dns")
			}
			current.Generation++
			if err := a.statusWriter.Update(ctx, current); err != nil {
				t.Error(err)
			}
		}
	case "configmap-race":
		f.afterTree = func() {
			var current corev1.ConfigMap
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(sourceMap), &current); err != nil {
				t.Error(err)
				return
			}
			current.Data["model.json"] += "\n"
			if err := a.statusWriter.Update(ctx, &current); err != nil {
				t.Error(err)
			}
		}
	case "owner-race":
		f.afterTree = func() {
			changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Owners[0].ConfigUID = "foreign" })
		}
	case "journal-race":
		f.afterTree = func() {
			changeJournal(func(nc *cvk.NetworkController) { nc.Status.Configuration.Operation.TaskID = "foreign" })
		}
	}
	f.mu.Unlock()
	before := journalState(t, a).Operation.DeepCopy()
	tick()
	after := journalState(t, a).Operation
	valid := mode == "complete" || mode == "configmap"
	if valid {
		expected := before.DeepCopy()
		expected.Phase = "Verifying"
		if !reflect.DeepEqual(after, expected) {
			t.Fatal("original task did not enter verification")
		}
		for range 5 {
			tick()
		}
		if state := journalState(t, a); state.Operation != nil || len(state.Owners) != 1 {
			t.Fatal("recovered operation did not settle")
		}
	} else {
		if after == nil || after.Phase != "Unknown" {
			t.Fatal("invalid observation advanced uncertain operation")
		}
		if mode != "journal-race" && !reflect.DeepEqual(after, before) {
			t.Fatal("recovery rewrote original acknowledgement")
		}
		if mode == "propagation" {
			f.mu.Lock()
			f.data["area/"+networkFixtureAPIKind(kind)] = networkFixturePayload(t, e.bindings[0].settings[0])
			f.mu.Unlock()
			for range 6 {
				tick()
			}
			if journalState(t, a).Operation != nil {
				t.Fatal("eventually matching task/readback did not recover")
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("recovery resubmitted native write", f.writes)
	}
	if (valid || mode == "propagation") && f.treeReads < 1 {
		t.Fatal("recovery skipped complete original task tree")
	}
}

func TestNetworkAcknowledgedTaskRecovery(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "root-running", "root-failed", "foreign-root", "stale-root", "child-running", "child-failed", "disconnected", "foreign-tree", "missing-task", "undeclared", "wrong-action", "wrong-target", "native-mismatch", "propagation", "native-race", "replacement-race", "source-race", "owner-race", "journal-race", "configmap", "configmap-race"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkTaskRecovery(t, kind, mode, nil) })
		}
	}
}

func TestNetworkTaskRecoveryEnvelope(t *testing.T) {
	e, _ := networkSubmissionFixtureExecutor(t, "ntp")
	a := &adapter{client: e.client}
	now := metav1.NewTime(time.Now().UTC().Add(-time.Minute))
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_settings", Resource: "Global/Lab#ntp", Action: "Update", TargetID: "area", Phase: "Unknown", TaskID: "task", SubmittedAt: &now, ConfigName: "source", ConfigUID: "uid", ConfigGeneration: 1, ControllerGeneration: 1}
	for _, mode := range []string{"valid", "undeclared", "no-time", "no-task", "bad-id", "accepted", "create", "parent", "physical", "credentials", "history", "cancelled", "wrong-kind"} {
		t.Run(mode, func(t *testing.T) {
			plan := configurationApplyPlan{section: "network_settings", executor: &networkCredentialConfigurationExecutor{network: &networkSectionExecutor{settings: e}}}
			a.bindNetworkSettingTaskRecovery(&configapi.NetworkControllerConfig{}, &configSourceSnapshot{}, &plan)
			current := op.DeepCopy()
			switch mode {
			case "undeclared":
				current.Resource = "Global/Other#ntp"
			case "no-time":
				current.SubmittedAt = nil
			case "no-task":
				current.TaskID = ""
			case "bad-id":
				current.ID = "foreign"
			case "accepted":
				current.Phase = "Accepted"
			case "create":
				current.Action = "Create"
			case "parent":
				current.ParentID = "parent"
			case "physical":
				current.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
			case "credentials":
				current.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			case "history":
				current.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{}}
			case "cancelled":
				current.CancellationRequestedAt = &now
			case "wrong-kind":
				current.Resource = "Global/Lab#aaa"
			}
			if plan.canObserveUnknownTask(current) != (mode == "valid") {
				t.Fatal("invalid compiled task selection")
			}
		})
	}
}
