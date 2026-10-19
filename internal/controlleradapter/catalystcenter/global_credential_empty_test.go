// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func emptyCredentialModel(groups string) []byte {
	return []byte(`{"catalyst_center":{"network_settings":{"device_credentials":` + groups + `}}}`)
}
func TestGlobalCredentialEmptyCollections(t *testing.T) {
	groups := []string{`{}`}
	for _, kind := range globalCredentialKinds {
		groups = append(groups, `{"`+kind.model+`":[]}`)
	}
	for _, group := range groups {
		t.Run(group, func(t *testing.T) {
			raw := emptyCredentialModel(group)
			if _, err := parseIPAMPruneIntent(raw); err == nil {
				t.Fatal("empty credential selection entered IPAM deletion executor")
			}
			for _, execution := range []bool{false, true} {
				wants, err := parseGlobalCredentials(raw, execution)
				if err != nil || len(wants) != 0 {
					t.Fatal("empty selection rejected", execution, err)
				}
			}
			wants, rest, err := splitNetworkCredentialConfiguration(raw)
			if err != nil || len(wants) != 0 || rest != nil {
				t.Fatal("empty split failed", err)
			}
			plans, err := (&client{}).compileConfigReports(raw, []string{"network_settings"})
			if err != nil || len(plans) != 1 || plans[0].count != 0 {
				t.Fatal("empty report failed", err)
			}
		})
	}
	for _, group := range []string{`null`, `[]`, `{"cli_credentials":null}`, `{"cli_credentials":{}}`, `{"unknown":[]}`, `{"snmpv3_credentials":[],"cli_credentials":[{"name":"bad"}]}`} {
		for _, execution := range []bool{false, true} {
			if _, err := parseGlobalCredentials(emptyCredentialModel(group), execution); err == nil {
				t.Fatal("malformed or hidden invalid selection accepted", group)
			}
		}
	}
}
func TestGlobalCredentialEmptyRetainAndRecovery(t *testing.T) {
	exerciseEmptyCredentialRetain(t, nil)
}
func exerciseEmptyCredentialRetain(t *testing.T, install func(*testing.T, *adapter, *configapi.NetworkControllerConfig)) {
	for _, phase := range []string{"idle", "Accepted", "Unknown", "Verifying"} {
		t.Run(phase, func(t *testing.T) {
			a, config := noAuthCredentialConfigurationFixture(t)
			if install != nil {
				install(t, a, config)
			}
			wants, _, err := splitNetworkCredentialConfiguration(config.Spec.Source.Inline.Raw)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			receipt := noAuthCredentialReceipt(wants[0], string(config.UID), config.Name)
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_settings", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{receipt}}
			if phase != "idle" {
				nc.Status.Configuration.Operation = receipt.Operation.DeepCopy()
				nc.Status.Configuration.Operation.Phase = phase
				nc.Status.Configuration.Operation.ID = strings.Repeat("b", 32)
			}
			if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
				t.Fatal(err)
			}
			before := nc.Status.Configuration.DeepCopy()
			config.Generation++
			config.Spec.Source.Inline.Raw = emptyCredentialModel(`{"snmpv3_credentials":[]}`)
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet {
					writes++
					t.Error("empty Retain selection mutated native state")
				}
				if r.URL.Path == globalCredentialsPath {
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": []any{}}})
					return
				}
				http.NotFound(w, r)
			})
			for range 4 {
				noAuthCredentialTick(t, a, config)
			}
			after := journalState(t, a)
			if writes != 0 || !reflect.DeepEqual(before, after) {
				t.Fatal("empty selection changed retained ownership or operation")
			}
			if (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (phase == "idle") {
				t.Fatal("empty selection released outstanding work", phase, config.Status.Conditions)
			}
		})
	}
}
