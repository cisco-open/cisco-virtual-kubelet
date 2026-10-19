// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const combinedModel = `{"catalyst_center":{"sites":{"areas":[{"name":"Lab","parent_name":"Global","network_settings":{"network":"base"}}]},"network_settings":{"network":[{"name":"base","ntp_servers":["192.0.2.1"]}]}}}`

func TestConfigReportMultipleSections(t *testing.T) {
	for _, mode := range []string{"match", "drift", "later-failure", "invalid-later-intent", "unmanaged-source"} {
		t.Run(mode, func(t *testing.T) {
			r, config, _ := reportFixture(t)
			ctx := context.Background()
			key := ctrlclient.ObjectKeyFromObject(config)
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			config.Spec.ManagedSections = []string{"sites", "network_settings"}
			config.Spec.Source.Inline.Raw = []byte(combinedModel)
			if mode == "invalid-later-intent" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(combinedModel, `"ntp_servers"`, `"unknown"`, 1))
			}
			if mode == "unmanaged-source" {
				config.Spec.ManagedSections = []string{"sites"}
			}
			if err := r.a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			calls := 0
			r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if req.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				if mode == "later-failure" {
					w.WriteHeader(500)
					return
				}
				if mode == "drift" {
					fmt.Fprint(w, `{"response":{"ntp":null}}`)
					return
				}
				fmt.Fprint(w, `{"response":{"ntp":{"servers":["192.0.2.1"]}}}`)
			})
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if mode == "match" || mode == "drift" {
				if len(config.Status.SectionStatus) != 2 || config.Status.SectionStatus[0].State != configapi.NetworkControllerSectionStateInSync {
					t.Fatal("per-section state lost")
				}
				expected := configapi.NetworkControllerConfigPhaseInSync
				if mode == "drift" {
					expected = configapi.NetworkControllerConfigPhaseDrifted
				}
				if config.Status.Phase != expected {
					t.Fatalf("phase=%v", config.Status.Phase)
				}
			} else {
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || config.Status.LastControllerCheck != nil || len(config.Status.Drift) != 0 {
					t.Fatal("partial success published")
				}
			}
			if (mode == "invalid-later-intent" || mode == "unmanaged-source") && calls != 0 {
				t.Fatal("invalid model contacted API")
			}
		})
	}
}
