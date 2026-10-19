//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Read-only native refusal qualification. Synthetic projected credentials must
// never be sent to the appliance; an existing job cannot establish their use.
func TestLiveDiscoveryCredentialExistingJobRefusal(t *testing.T) {
	liveDiscoveryCredentialRefusal(t, false)
}

func TestLiveDiscoveryCredentialCompositionRefusal(t *testing.T) {
	liveDiscoveryCredentialRefusal(t, true)
}

func liveDiscoveryCredentialRefusal(t *testing.T, composition bool) {
	t.Helper()
	if os.Getenv("CATC_DISCOVERY_CREDENTIAL_READONLY") != "existing-lab" {
		t.Skip("explicit read-only discovery qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	observer := liveClient(t)
	session, _, err := observer.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := session.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	session.HTTPClient.Transport = &siteRetainedReadOnlyTransport{base: base}
	before, err := observer.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := before["Switches"]; !present {
		t.Fatal("required existing lab job is absent")
	}
	devices, err := observer.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(rows []Device) map[string]string {
		out := map[string]string{}
		for _, row := range rows {
			out[row.ID] = row.Serial + "/" + row.ManagementIP
		}
		return out
	}
	raw := discoveryCredentialSource(t, map[string]any{"name": "Switches", "ip_address_list": "198.51.100.101", "user_name_list": []any{"cvk-unused-refusal-fixture"}, "password_list_version": 1}, false)
	if composition {
		const candidate = "CVK-Qualification-NoCreate"
		if _, present := before[candidate]; present {
			t.Fatal("qualification candidate must be absent before read-only test")
		}
		catalog, err := observer.globalCredentials(ctx)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]int{}
		for _, group := range catalog {
			for name := range group {
				names[name]++
			}
		}
		unique := []string{}
		for name, count := range names {
			if count == 1 {
				unique = append(unique, name)
			}
		}
		sort.Strings(unique)
		if len(unique) == 0 {
			t.Fatal("an existing unambiguous global credential reference is required")
		}
		var model map[string]any
		if json.Unmarshal(raw, &model) != nil {
			t.Fatal("invalid fixture")
		}
		inventory := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
		inventory["discovery"] = append(inventory["discovery"].([]any), map[string]any{"name": candidate, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": []any{unique[0]}})
		raw, err = json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
	}
	a, nc, c, _, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	nc.Spec.Endpoint = os.Getenv("CATC_ENDPOINT")
	if nc.Spec.Endpoint == "" {
		nc.Spec.Endpoint = "https://127.0.0.1:19443"
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.client = observer
	for i := 0; i < 3; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := nc.Status.Configuration
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || state != nil && (state.Operation != nil || len(state.ActionApplications) > 0 || len(state.CredentialApplications) > 0) {
			t.Fatal("existing native job authorized credential adoption or submission")
		}
	}
	after, err := observer.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterDevices, err := observer.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(identity(devices), identity(afterDevices)) {
		t.Fatal("native baseline or inventory identity changed during refusal qualification")
	}
	t.Logf("Composition=%t: three reconstructed public reconciles refused unowned existing-job adoption and partial creation before operation/permit creation; shared ownership bookkeeping is allowed in fixture Kubernetes state. Read-only transport enforced; %d discovery jobs and %d device identities preserved. No credential delivery or installed rollout qualified.", composition, len(after), len(afterDevices))
}
