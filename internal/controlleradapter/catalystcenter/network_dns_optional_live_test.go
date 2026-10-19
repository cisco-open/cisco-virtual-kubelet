// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLiveCatalystCenterDNSOptional(t *testing.T) {
	if os.Getenv("CATC_DNS_OPTIONAL_QUALIFICATION") != "isolated-domain-only" {
		t.Skip("isolated DNS qualification disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

func qualifyLiveDNSOptional(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, s := range sites {
		if s.NameHierarchy == root && s.Type == "area" {
			target = s.ID
		}
	}
	if !validAPIID(target) {
		t.Fatal("isolated area missing")
	}

	// Seed resolvers only on this generated, empty area. This is fixture setup;
	// all subsequent domain-only/reset changes use the public reconciler.
	*settled = false
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+target+"/dnsSettings", []byte(`{"dns":{"domainName":"initial.example.test","dnsServers":["192.0.2.13","192.0.2.14"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
		t.Fatal("missing original setup task")
	}
	if err := waitTask(ctx, ack.Response, submitted); err != nil {
		t.Fatal(err)
	}
	got, err := c.readNetworkSetting(ctx, target, "dns")
	if err != nil || got.domain != "initial.example.test" || !reflect.DeepEqual(got.servers, []string{"192.0.2.13", "192.0.2.14"}) {
		t.Fatal("setup readback failed", err)
	}
	*settled = true
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.ConfigMapRef = nil
	trace := &metadataNoDispatchTrace{a: a, section: "network_settings", preflightPath: sitesPath, injected: map[string]bool{}, retired: map[string]bool{}, allowMutation: func(req *http.Request) bool {
		return req.Method == http.MethodPut && req.URL.Path == sitesPath+"/"+target+"/dnsSettings"
	}}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	previousTask := ""
	for index, mode := range []string{"omitted", "clear", "null"} {
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		config.Spec.Mode = configapi.NetworkControllerApplyModeApply
		config.Spec.ManagedSections = []string{"network_settings"}
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: dnsOptionalModel(t, mode, root)}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		*settled = false
		recovered := false
		converged := false
		for range 80 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
				if state.Operation.TargetID != target || !networkSettingResourceMatches(state.Operation.Resource, networkSettingIdentity(root, "dns")) {
					t.Fatal("operation escaped isolated DNS target")
				}
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered = true
			}
			if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("public DNS flow failed; retain hierarchy")
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				records, err := networkSettingApplicationRecords(state)
				if err != nil || len(records) != 1 || !recovered || state.Operation != nil || trace.mutations != index+1 {
					t.Fatal("DNS original task/application lifecycle incomplete", err)
				}
				for _, row := range records {
					if row.Operation.TargetID != target || row.Operation.ConfigGeneration != config.Generation || row.Operation.TaskID == previousTask || trace.recoveryTrees[row.Operation.TaskID] < 1 {
						t.Fatal("new source/task/target receipt mismatch")
					}
					previousTask = row.Operation.TaskID
				}
				got, err := c.readNetworkSetting(ctx, target, "dns")
				if err != nil {
					t.Fatal(err)
				}
				domain := "changed.example.test"
				if mode == "null" {
					domain = "null.example.test"
				}
				expected := []string{}
				if mode == "omitted" {
					expected = []string{"192.0.2.13", "192.0.2.14"}
				}
				if got.inherited || got.unset || got.domain != domain || !reflect.DeepEqual(got.servers, expected) {
					t.Fatal("native DNS domain/resolver readback mismatch")
				}
				for range 2 {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				if trace.mutations != index+1 || journalState(t, a).Operation != nil {
					t.Fatal("DNS no-op replayed native mutation")
				}
				t.Logf("public DNS mode=%s task=%s original Unknown recovery, latest receipt, native resolver count=%d and repeated no-op verified", mode, previousTask, len(got.servers))
				*settled = true
				converged = true
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !converged {
			t.Fatal("DNS did not converge; retain hierarchy")
		}
	}
	if len(trace.injected) != 3 || len(trace.retired) != 3 || len(trace.recoveryTrees) != 3 || trace.pending != "" {
		t.Fatal("original preflight/refusal/task traces incomplete")
	}
	t.Log("three public DNS revisions, three one-use no-dispatch retirements, three original task recoveries, one latest application; native API with simulated Kubernetes storage/actors")
}
