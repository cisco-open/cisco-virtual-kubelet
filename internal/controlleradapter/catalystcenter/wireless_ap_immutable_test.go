// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestWirelessAPRemoteWorkerModeIsCreateOnly(t *testing.T) {
	for _, mode := range []string{"enable", "disable", "unknown", "same-mode-update"} {
		t.Run(mode, func(t *testing.T) {
			desired, _ := parseWirelessAPProfiles([]byte(wirelessAPModel))
			actual := cloneWirelessAPFields(desired[0].metadata)
			actual["id"] = "ap-id"
			switch mode {
			case "enable":
				desired[0].metadata["remoteWorkerEnabled"] = true
			case "disable":
				actual["remoteWorkerEnabled"] = true
			case "unknown":
				delete(actual, "remoteWorkerEnabled")
			case "same-mode-update":
				actual["clientLimit"] = int64(10)
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					t.Error("preflight submitted a mutation")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
			})
			write, err := (&wirelessAPExecutor{client: c, desired: desired}).next(context.Background())
			if mode == "same-mode-update" {
				if err != nil || write == nil {
					t.Fatal("mutable field update refused", err)
				}
			} else {
				if err == nil || write != nil {
					t.Fatal("immutable/unknown mode update admitted")
				}
				if mode != "unknown" && !errors.Is(err, errWirelessAPImmutable) {
					t.Fatal("immutable field diagnosis missing", err)
				}
			}
			if writes != 0 {
				t.Fatal("preflight issued a native write")
			}
		})
	}
}

func TestWirelessAPImmutableModeBlocksPowerPrerequisite(t *testing.T) {
	raw := `{"catalyst_center":{"wireless":{"power_profiles":[{"name":"POWER","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"1000MBPS"}]}],"ap_profiles":[{"name":"AP-LAB","remote_worker_enabled":true,"power_profile":"POWER"}]}}}`
	base, _ := parseWirelessAPProfiles([]byte(wirelessAPModel))
	actual := cloneWirelessAPFields(base[0].metadata)
	actual["id"] = "ap-id"
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			t.Error("invalid later AP released prerequisite")
		}
		if r.URL.Path == wirelessPowerPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if r.URL.Path != wirelessAPPath {
			t.Error("unexpected request", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
	})
	e, err := c.compileWirelessConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	write, err := e.next(context.Background())
	if !errors.Is(err, errWirelessAPImmutable) || write != nil || writes != 0 {
		t.Fatal("whole-model immutable preflight failed", err)
	}
	// A new named remote-worker profile remains a supported create intent.
	desired, err := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"NEW","remote_worker_enabled":true`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	write, err = (&wirelessAPExecutor{client: c, desired: desired}).next(context.Background())
	if err != nil || write == nil || write.action != "Create" || write.identity.target != "" {
		t.Fatal("new remote profile create refused", err)
	}
}

func TestWirelessAPImmutableManifestReportsActionableFailure(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB","remote_worker_enabled":true`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	baseline, _ := parseWirelessAPProfiles([]byte(wirelessAPModel))
	actual := cloneWirelessAPFields(baseline[0].metadata)
	actual["id"] = "ap-id"
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			t.Error("immutable manifest submitted mutation")
		}
		if r.URL.Path != wirelessAPPath {
			t.Error("unexpected native request", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
	})
	for range 2 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	var current configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, condition := range current.Status.Conditions {
		if condition.Reason == "ImmutableAPProfileField" && strings.Contains(condition.Message, "different profile name") {
			found = true
		}
	}
	if current.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || !found || writes != 0 || journalState(t, a).Operation != nil {
		t.Fatal("immutable field did not stop before journaling with clear status", current.Status)
	}
}
