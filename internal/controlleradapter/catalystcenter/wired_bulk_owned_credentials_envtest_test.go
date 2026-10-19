// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Private real API server and local HTTPS only. Published ownership/task
// evidence is synthetic; no installed controller, credentials or device is used.
func TestEnvtestWiredBulkOwnedCredentialDependencies(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, mode := range []string{"versions", "metadata", "configmap"} {
		t.Run(mode, func(t *testing.T) {
			namespace := "owned-bulk-" + mode
			if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
				t.Fatal(err)
			}
			doc := wiredBulkOwnedCredentialDocument(t)
			if mode == "metadata" {
				doc = wiredBulkOwnedMetadataDocument(t)
			}
			raw, _ := json.Marshal(doc)
			source := &configSourceSnapshot{raw: raw}
			model, err := compileWiredBulkSource(source)
			if err != nil {
				t.Fatal(err)
			}
			credentials := newWiredBulkCredentialFixture()
			ipam := &wiredBulkIPAMFixture{pools: fixturePools(), reservations: fixtureReservations()}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if credentials.reply(t, w, r) || ipam.reply(t, w, r) {
					return
				}
				if r.Method == "GET" && r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				t.Error("API persistence qualification attempted unexpected native operation")
				w.WriteHeader(403)
			})
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: namespace}, Spec: cvk.NetworkControllerSpec{Type: TypeName, Endpoint: "https://cc.example.invalid", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
			owner := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "credential-owner", Namespace: namespace}, Spec: configapi.NetworkControllerConfigSpec{ControllerRef: configapi.NetworkControllerRef{Name: nc.Name}, ManagedSections: []string{"network_settings"}, Scope: "global", Mode: configapi.NetworkControllerApplyModeApply, ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}}}
			versions := map[string][]cvk.NetworkControllerConfigurationCredentialVersion{}
			for _, want := range model.network.credentials {
				metadata := want.metadata
				metadata.ID = "credential-" + want.kind.api
				credentials.metadata[want.kind.api] = []globalCredentialMetadata{metadata}
				for _, leaf := range want.credentials {
					alias := fmt.Sprintf("source-%d", len(nc.Spec.IntentSecretSources))
					nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: "credential-sources", Key: alias})
					owner.Spec.SecretRefs = append(owner.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: alias})
					versions[want.resource()] = append(versions[want.resource()], cvk.NetworkControllerConfigurationCredentialVersion{Path: leaf.path, Version: leaf.version, SourceAlias: alias, SecretName: "credential-sources", SecretKey: alias})
				}
			}
			delete(doc["catalyst_center"].(map[string]any), "inventory")
			ownerRaw, _ := json.Marshal(doc)
			var cm *core.ConfigMap
			if mode == "configmap" {
				cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "owned-intent", Namespace: namespace}, Data: map[string]string{"model.json": string(ownerRaw)}}
				if err := api.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				owner.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
			} else {
				owner.Spec.Source.Inline = &runtime.RawExtension{Raw: ownerRaw}
			}
			if err := api.Create(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := api.Create(ctx, owner); err != nil {
				t.Fatal(err)
			}
			owner.Status.Phase = configapi.NetworkControllerConfigPhaseInSync
			owner.Status.ObservedGeneration = owner.Generation
			if err := api.Status().Update(ctx, owner); err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_settings", ConfigName: owner.Name, ConfigUID: string(owner.UID)}}}
			for _, want := range model.network.credentials {
				if want.metadataOnly() {
					receipt := noAuthCredentialReceipt(want, string(owner.UID), owner.Name)
					receipt.Resource = want.metadataKey() + ":credential-" + want.kind.api
					receipt.Operation.ConfigGeneration = owner.Generation
					receipt.Operation.ControllerGeneration = nc.Generation
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, receipt)
				} else {
					leaves := versions[want.resource()]
					sort.Slice(leaves, func(i, j int) bool { return leaves[i].Path < leaves[j].Path })
					nc.Status.Configuration.CredentialApplications = append(nc.Status.Configuration.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "network_settings", Resource: want.resource(), ConfigUID: string(owner.UID), TargetID: "credential-" + want.kind.api, OperationID: strings.Repeat("c", 32), Versions: leaves})
				}
			}
			if err := api.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			a := &adapter{client: c, statusReader: api, statusWriter: api, key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, intentSecretPath: "/nonexistent-owned-credential-projections"}
			observers, err := a.wiredBulkDependencies(ctx, source)
			if err != nil || len(observers) != 1 {
				t.Fatal("real API owner/version composition unavailable", err)
			}
			before, err := observers[0](ctx)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stale := fresh.DeepCopy()
			if mode == "metadata" {
				fresh.Status.Configuration.ActionApplications[0].Operation.ID = strings.Repeat("d", 32)
			} else {
				fresh.Status.Configuration.CredentialApplications[0].OperationID = strings.Repeat("d", 32)
			}
			if err := api.Status().Update(ctx, fresh); err != nil {
				t.Fatal(err)
			}
			if err := api.Status().Update(ctx, stale); !apierrors.IsConflict(err) {
				t.Fatal("stale API status write replaced prerequisite receipt", err)
			}
			after, err := observers[0](ctx)
			if err != nil || after == before {
				t.Fatal("API receipt replacement retained original identity", err)
			}
			// Invalid versions/generations must be rejected by the actual CRD,
			// not silently pruned into an apparently successful dependency.
			invalid := fresh.DeepCopy()
			if mode == "metadata" {
				invalid.Status.Configuration.ActionApplications[0].Operation.ConfigGeneration = 0
			} else {
				invalid.Status.Configuration.CredentialApplications[0].Versions[0].Version = 0
			}
			if err := api.Status().Update(ctx, invalid); !apierrors.IsInvalid(err) {
				t.Fatal("invalid prerequisite receipt admitted by real schema", err)
			}
			invalidAlias := fresh.DeepCopy()
			invalidAlias.Spec.IntentSecretSources = append(invalidAlias.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: "BadAlias", Name: "credential-sources", Key: "value"})
			if err := api.Update(ctx, invalidAlias); !apierrors.IsInvalid(err) {
				t.Fatal("real API admitted invalid source authorization alias", err)
			}
			if cm != nil {
				cm.Data["revision.txt"] = "new-source-revision"
				if err := api.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
				updated, err := observers[0](ctx)
				if err != nil || updated == after {
					t.Fatal("real ConfigMap revision change reused old dependency identity", err)
				}
			}
			immutable := owner.DeepCopy()
			immutable.Spec.Scope = "changed-scope"
			if err := api.Update(ctx, immutable); !apierrors.IsInvalid(err) {
				t.Fatal("real API admitted a scope change", err)
			}
			owner.Spec.TaskTimeout = &metav1.Duration{Duration: time.Hour}
			if err := api.Update(ctx, owner); err != nil {
				t.Fatal(err)
			}
			if owner.Generation != 2 {
				t.Fatal("real API did not advance prerequisite config generation")
			}
			if _, err := observers[0](ctx); err == nil {
				t.Fatal("unreconciled owner generation accepted")
			}
			originalUID := owner.UID
			if err := api.Delete(ctx, owner); err != nil {
				t.Fatal(err)
			}
			replacement := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: owner.Name, Namespace: namespace}, Spec: *owner.Spec.DeepCopy()}
			if err := api.Create(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			replacement.Status.Phase = configapi.NetworkControllerConfigPhaseInSync
			replacement.Status.ObservedGeneration = replacement.Generation
			if err := api.Status().Update(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			if replacement.UID == originalUID {
				t.Fatal("real API reused deleted credential owner UID")
			}
			if _, err := observers[0](ctx); err == nil {
				t.Fatal("recreated owner inherited old applied receipt")
			}
			var secrets core.SecretList
			if err := api.List(ctx, &secrets, ctrlclient.InNamespace(namespace)); err != nil || len(secrets.Items) != 0 {
				t.Fatal("qualification unexpectedly required Secret values", err)
			}
			t.Log("real API owner UID, canonical receipt persistence, status CAS, schema refusal and generation/source invalidation verified; task/worker evidence synthetic; no Secret values or installed cluster used")
		})
	}
}
