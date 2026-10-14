// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configv1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvkv1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	corev1.AddToScheme(s)
	coordinationv1.AddToScheme(s)
	cvkv1.AddToScheme(s)
	configv1.AddToScheme(s)
	return s
}

type fixtureAPI struct {
	body         map[string]any
	posts, puts  int
	loseResponse bool
}

func (f *fixtureAPI) serve(w http.ResponseWriter, r *http.Request) {
	if auth(w, r) {
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/server"):
		fmt.Fprint(w, `{"data":{"platformVersion":"20.18.2.2"}}`)
	case strings.HasSuffix(r.URL.Path, "/device"):
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/feature"):
		if f.body == nil {
			fmt.Fprint(w, `{"data":[]}`)
		} else {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"templateId": "fixture-id", "templateName": f.body["templateName"], "templateType": "cisco_banner"}}})
		}
	case r.Method == "GET":
		f.body["attachedMastersCount"] = 0
		f.body["devicesAttached"] = 0
		json.NewEncoder(w).Encode(f.body)
	case r.Method == "POST":
		f.posts++
		json.NewDecoder(r.Body).Decode(&f.body)
		if f.loseResponse {
			http.Error(w, "lost receipt", 500)
		} else {
			fmt.Fprint(w, `{"templateId":"fixture-id"}`)
		}
	case r.Method == "PUT":
		f.puts++
		json.NewDecoder(r.Body).Decode(&f.body)
		fmt.Fprint(w, `{}`)
	default:
		http.Error(w, "unsupported", 400)
	}
}
func testAdapter(t *testing.T, api *fixtureAPI) (*adapter, *configv1.NetworkControllerConfig) {
	remote := testClient(t, api.serve)
	nc := &cvkv1.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "lab", UID: "endpoint-uid", Generation: 1}, Spec: cvkv1.NetworkControllerSpec{Type: "sdwan", Endpoint: remote.Endpoint, CredentialSecretRef: cvkv1.NetworkControllerSecretReference{Name: "credentials"}}}
	cfg := &configv1.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "lab", UID: "config-uid", Generation: 1}, Spec: configv1.NetworkControllerConfigSpec{ControllerRef: configv1.NetworkControllerRef{Name: "manager"}, Scope: "global", ManagedSections: []string{"edge_feature_templates"}, Mode: "apply", ModelSource: configv1.NetworkControllerNetAsCodeModelSource{Format: "netascode-sdwan", ModelVersion: ModelVersion, Resolved: true}, Source: configv1.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"sdwan":{"edge_feature_templates":{"banner_templates":[{"name":"fixture","description":"test","login":"hello"}]}}}`)}}}}
	k := fake.NewClientBuilder().WithScheme(testScheme()).WithStatusSubresource(nc, cfg).WithObjects(nc, cfg).Build()
	a := &adapter{opts: controlleradapter.Options{Controller: nc}, remote: remote, kube: k, session: "session-1"}
	remote.Guard = a.guard
	return a, cfg
}
func reconcileFixture(t *testing.T, a *adapter, cfg *configv1.NetworkControllerConfig) *configv1.NetworkControllerConfig {
	t.Helper()
	a.reconcile(context.Background(), cfg)
	out := &configv1.NetworkControllerConfig{}
	if e := a.kube.Get(context.Background(), client.ObjectKeyFromObject(cfg), out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestReconcileApplyIdempotencyAndDrift(t *testing.T) {
	api := &fixtureAPI{}
	a, cfg := testAdapter(t, api)
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "Applying" || api.posts != 1 {
		t.Fatalf("first apply: %s posts=%d conditions=%v", cfg.Status.Phase, api.posts, cfg.Status.Conditions)
	}
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "InSync" || api.posts != 1 || api.puts != 0 {
		t.Fatal("second apply not idempotent")
	}
	api.body["templateDescription"] = "drift"
	cfg = reconcileFixture(t, a, cfg)
	if api.puts != 1 {
		t.Fatal("did not repair drift", cfg.Status.Conditions)
	}
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "InSync" {
		t.Fatal("drift verification")
	}
	// A replacement worker cannot race the live session even with the same config.
	a.session = "session-2"
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "LeaseBlocked" || api.posts != 1 || api.puts != 1 {
		t.Fatal("worker fencing")
	}
}
func TestReconcileReportNeverWrites(t *testing.T) {
	api := &fixtureAPI{}
	a, cfg := testAdapter(t, api)
	cfg.Spec.Mode = "report"
	if e := a.kube.Update(context.Background(), cfg); e != nil {
		t.Fatal(e)
	}
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "Drifted" || api.posts != 0 || api.puts != 0 {
		t.Fatal("report mutated or failed", cfg.Status.Conditions)
	}
}
func TestReconcileUnknownOutcomeNeverReplays(t *testing.T) {
	api := &fixtureAPI{loseResponse: true}
	a, cfg := testAdapter(t, api)
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "Failed" || api.posts != 1 {
		t.Fatal("lost receipt")
	}
	cfg = reconcileFixture(t, a, cfg)
	if api.posts != 1 || api.puts != 0 {
		t.Fatal("replayed ambiguous mutation")
	}
}
func TestReconcileRejectsForeignResource(t *testing.T) {
	api := &fixtureAPI{}
	a, cfg := testAdapter(t, api)
	rs, e := Compile(cfg.Spec.Source.Inline.Raw)
	if e != nil {
		t.Fatal(e)
	}
	api.body = rs[0].Body
	cfg = reconcileFixture(t, a, cfg)
	if cfg.Status.Phase != "Failed" || api.posts != 0 || api.puts != 0 {
		t.Fatal("adopted foreign resource")
	}
}
func TestReconcilePauseFencesRequests(t *testing.T) {
	api := &fixtureAPI{}
	a, cfg := testAdapter(t, api)
	nc := a.opts.Controller.DeepCopy()
	if e := a.kube.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); e != nil {
		t.Fatal(e)
	}
	nc.Spec.Paused = true
	if e := a.kube.Update(context.Background(), nc); e != nil {
		t.Fatal(e)
	}
	reconcileFixture(t, a, cfg)
	if api.posts != 0 {
		t.Fatal("paused mutation")
	}
}
