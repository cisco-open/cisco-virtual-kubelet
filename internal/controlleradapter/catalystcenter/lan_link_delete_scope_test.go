// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestLANLinkDeleteVersionedDeclarationBindings(t *testing.T) {
	want := lanLinkIntent{name: "link", action: "DELETE_LINK", primaryIP: "192.0.2.10", secondaryIP: "192.0.2.11", primaryInterface: "TenGigabitEthernet1/0/1", secondaryInterface: "TenGigabitEthernet1/0/2", pool: "access"}
	hash := strings.Repeat("a", 64)
	raw := lanLinkDeclarationDigest(want)
	scope := lanLinkDeclarationScope(want)
	legacy := lanLinkDeletePrefix + strings.Join([]string{hash, hash, hash, raw, hash}, ":")
	current := lanLinkDeleteScopedPrefix + strings.Join([]string{hash, hash, hash, raw, scope, hash}, ":")
	old, err := parseLANLinkDeleteResource(legacy, "Update")
	if err != nil {
		t.Fatal(err)
	}
	next, err := parseLANLinkDeleteResource(current, "Update")
	if err != nil {
		t.Fatal(err)
	}
	if old.scope != "" || next.scope != scope || !exactLANLinkDeleteDeclaration(want, old) || !exactLANLinkDeleteDeclaration(want, next) {
		t.Fatal("raw binding not preserved")
	}
	want.pool = ""
	if observedLANLinkDeleteDeclaration(want, old) || exactLANLinkDeleteDeclaration(want, next) || !observedLANLinkDeleteDeclaration(want, next) {
		t.Fatal("post-completion semantics leaked into exact dispatch/source binding")
	}
	for _, value := range []string{current + ":extra", strings.Replace(current, scope, "bad", 1), strings.Replace(current, "v2:", "v3:", 1)} {
		if validLANLinkMutationResource(value, "Update") {
			t.Fatal("malformed/unknown version admitted")
		}
	}
	// Raw identity remains part of cancellation scope. New and old formats cannot
	// be silently interchanged to retire an unused operation's missing metadata.
	oldScope, err := parseLANLinkPreparationScope(legacy, "Update")
	if err != nil {
		t.Fatal(err)
	}
	newScope, err := parseLANLinkPreparationScope(current, "Update")
	if err != nil {
		t.Fatal(err)
	}
	if oldScope == newScope {
		t.Fatal("legacy scope was invented")
	}
}

func TestLANLinkDeleteCompletedPoolDeclarationMigration(t *testing.T) {
	for _, mode := range []string{"explicit-to-omitted", "omitted-to-explicit", "wrong-pool", "changed-interface"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, want, f := ownedLANDeletedFixture(t, true, mode == "omitted-to-explicit")
			switch mode {
			case "explicit-to-omitted":
				want.pool = ""
			case "omitted-to-explicit":
				want.pool = "access"
			case "wrong-pool":
				want.pool = "other-pool"
			case "changed-interface":
				want.primaryInterface = "TenGigabitEthernet1/0/20"
			}
			updateLANIntentFixture(t, a, config, want)
			reads := f.usageReads
			f.poolChanged = true
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			success := mode == "explicit-to-omitted" || mode == "omitted-to-explicit"
			if (published.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != success {
				t.Fatal("incorrect semantic deleted-state convergence", published.Status.Phase)
			}
			state := journalState(t, a)
			if state.Operation != nil || len(state.ActionApplications) != 2 || f.posts != 2 || f.usageReads != reads {
				t.Fatal("post-completion observation adopted, replayed or queried capacity")
			}
		})
	}
}

func TestLANLinkDeleteScopedBindingDoesNotMigrateInFlightSource(t *testing.T) {
	for _, mode := range []string{"generation", "source-version-only"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, want, f, _ := dispatchedLANLinkDeleteFixture(t, false)
			original := journalState(t, a).Operation.DeepCopy()
			want.pool = ""
			updateLANIntentFixture(t, a, config, want)
			if mode == "source-version-only" {
				// Isolate the source-version fence from generation fencing in fake K8s.
				config.Generation = original.ConfigGeneration
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			for i := range f.ports {
				f.ports[i].IPv4, f.ports[i].Mask = "", ""
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state.Operation == nil || state.Operation.Phase != "Accepted" || state.Operation.ID != original.ID || state.Operation.Resource != original.Resource || len(state.ActionApplications) != 1 || f.posts != 2 {
				t.Fatal("semantic receipt identity rewrote or completed an in-flight request")
			}
			assertLANClaimsRetained(t, a, false)
		})
	}
}
