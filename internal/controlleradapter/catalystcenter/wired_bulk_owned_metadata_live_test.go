// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Supply the same producer with a real private API client. The caller owns
// this ephemeral server; this never reads kubeconfig or the installed cluster.
func seedLiveOwnedMetadataAPI(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, api ctrlclient.Client, endpoint string) {
	t.Helper()
	ctx := context.Background()
	const namespace = "owned-metadata-native"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	original, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: original.Name, Namespace: namespace}, Spec: *original.Spec.DeepCopy()}
	nc.Spec.Endpoint = endpoint
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	owner := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: config.Name, Namespace: namespace}, Spec: *config.Spec.DeepCopy()}
	if err := api.Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	*config = *owner
	a.statusReader, a.statusWriter = api, api
	a.key = ctrlclient.ObjectKeyFromObject(nc)
	a.uid, a.generation = nc.UID, nc.Generation
	t.Log("metadata native producer uses private real API-server objects", string(nc.UID), string(owner.UID))
}

// Reuse the real native public-manifest producer receipt. Kubernetes status
// actors remain simulated. The inventory row selects dependency context only;
// no physical device is looked up, claimed, provisioned or authenticated.
func qualifyLiveWiredBulkOwnedMetadata(t *testing.T, a *adapter, ctx context.Context, owner *configapi.NetworkControllerConfig) string {
	t.Helper()
	rest, _, err := a.client.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	trace := &wiredBulkReadOnlyTransport{base: base}
	rest.HTTPClient.Transport = trace
	defer func() {
		rest.HTTPClient.Transport = base
		if trace.attempts.Load() != 0 {
			t.Error("owned metadata dependency observer attempted native mutation")
		}
	}()
	catalog, err := a.client.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var area *Site
	for _, site := range catalog {
		if site.Type == "area" && parentSitePath(site.NameHierarchy) == "Global" {
			copy := site
			area = &copy
			break
		}
	}
	if area == nil {
		t.Fatal("native root-area context unavailable for metadata dependency qualification")
	}
	var doc map[string]any
	if err := json.Unmarshal(owner.Spec.Source.Inline.Raw, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc["catalyst_center"].(map[string]any)
	root["sites"] = map[string]any{"areas": []any{map[string]any{"name": area.Name, "parent_name": "Global"}}}
	root["inventory"] = map[string]any{"devices": []any{map[string]any{"name": "qualification-no-device", "site": area.NameHierarchy, "state": "REPROVISION"}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	observers, err := a.wiredBulkDependencies(ctx, &configSourceSnapshot{raw: raw})
	if err != nil || len(observers) != 1 {
		t.Fatal("public native metadata receipt cannot compose with bulk dependency", err)
	}
	before, err := observers[0](ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := observers[0](ctx)
	if err != nil || before != after {
		t.Fatal("unchanged public metadata receipt changed dependency identity", err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := nc.Status.Configuration.DeepCopy()
	nc.Status.Configuration.ActionApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	_, refused := observers[0](ctx)
	// Restore even on an assertion failure so the producer fixture can complete
	// its own exact-UUID cleanup and verify the complete native baseline.
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = original
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if refused == nil {
		t.Fatal("missing metadata ownership receipt accepted")
	}
	if restored, err := observers[0](ctx); err != nil || restored != before {
		t.Fatal("restored metadata receipt did not recover exact original identity", err)
	}
	t.Log("real native public metadata create/update receipt composed with private bulk dependency; missing receipt refused and restored without extra native writes; Kubernetes actors simulated")
	return before
}
