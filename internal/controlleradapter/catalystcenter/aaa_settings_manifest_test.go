// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAAASettingsManifestRotateRestart(t *testing.T) {
	for _, site := range []string{"root", "area"} {
		for _, combined := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/combined=%t", site, combined), func(t *testing.T) { testAAASettingsManifestRotateRestart(t, site, combined) })
		}
	}
}

func testAAASettingsManifestRotateRestart(t *testing.T, site string, combined bool) {
	model := aaaApplyModel()
	if site == "area" {
		model = []byte(strings.Replace(string(model), `"global":{"network_settings":{"aaa_servers":"base"}}`, `"areas":[{"name":"Lab","parent_name":"Global","network_settings":{"aaa_servers":"base"}}]`, 1))
	}
	if combined {
		model = []byte(strings.Replace(string(model), `"aaa_servers":"base"`, `"aaa_servers":"base","network":"network","telemetry":"base"`, 1))
		model = []byte(strings.Replace(string(model), `"aaa_servers":[`, `"network":[{"name":"network","ntp_servers":["192.0.2.1"]}],"aaa_servers":[`, 1))
	}
	if combined {
		model = []byte(strings.Replace(string(model), `"aaa_servers":[`, `"telemetry":[`+telemetryProfile+`],"aaa_servers":[`, 1))
	}
	if combined {
		var root, pools map[string]any
		if json.Unmarshal(model, &root) != nil || json.Unmarshal([]byte(poolModel), &pools) != nil {
			t.Fatal("invalid fixture")
		}
		cc := root["catalyst_center"].(map[string]any)
		poolCC := pools["catalyst_center"].(map[string]any)
		cc["network_settings"].(map[string]any)["ip_pools"] = poolCC["network_settings"].(map[string]any)["ip_pools"]
		sites := cc["sites"].(map[string]any)
		if site == "area" {
			sites["areas"].([]any)[0].(map[string]any)["ip_pools_reservations"] = []any{"access"}
		} else {
			sites["areas"] = poolCC["sites"].(map[string]any)["areas"]
		}
		var entry map[string]any
		if json.Unmarshal([]byte(globalCredentialFixtureEntries["cli_credentials"]), &entry) != nil {
			t.Fatal("bad credential fixture")
		}
		cc["network_settings"].(map[string]any)["device_credentials"] = map[string]any{"cli_credentials": []any{entry}}
		sites["areas"].([]any)[0].(map[string]any)["cli_credentials"] = "fixture"
		model, _ = json.Marshal(root)
	}
	ctx := context.Background()
	a, config, source, _ := credentialVersionJournalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = "https://catc.example"
	nc.Spec.CredentialSecretRef = ciskov1.NetworkControllerSecretReference{Name: "credentials"}
	nc.Spec.IntentSecretSources = nil
	config.Spec.SecretRefs = nil
	config.Spec.ManagedSections = []string{"network_settings"}
	fixture := aaaExecutorFixture(t)
	if combined {
		_, snapshot := globalCredentialFixture(t, "cli_credentials")
		fixture.secrets.values = append(fixture.secrets.values, snapshot.values...)
	}
	expectedReceipts := 1
	if combined {
		expectedReceipts = 2
	}
	paths := []string{}
	for _, value := range fixture.secrets.values {
		alias := strings.ToLower(value.ref.Source)
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: alias, Name: "credentials", Key: alias})
		config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: value.ref.Path, Source: alias})
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: value.ref.Path, SourceAlias: alias, SecretName: "credentials", SecretKey: alias})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(source.secrets.root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value.value, 0600); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(value.ref.Path, "/aaa_servers/") {
			paths = append(paths, path)
		}
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: model}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = source.secrets.root
	actual := map[string]any{"aaaNetwork": nil, "aaaClient": nil}
	var credential *globalCredentialMetadata
	credentialWrites, assignmentWrites := 0, 0
	slots := map[string]any{}
	for _, kind := range globalCredentialKinds {
		slots[credentialSlot(kind.model)] = nil
	}
	writes := 0
	ipamWrites := 0
	pools := []ipPool{}
	reservations := []ipReservation{}
	ntpWrites := 0
	telemetryWrites := 0
	telemetry := strings.Replace(telemetryResponse, `"enableWiredDataCollection":false`, `"enableWiredDataCollection":true`, 1)
	ntp := map[string]any{"ntp": nil}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == authenticationPolicyServersPath:
			fmt.Fprint(w, aaaRegistrationFixture)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		case strings.HasPrefix(r.URL.Path, taskPath):
			now := time.Now().UnixMilli()
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			if strings.HasSuffix(r.URL.Path, "/tree") {
				json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": id, "rootId": id, "startTime": now, "endTime": now, "isError": false}}})
			} else {
				fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, id, now, now)
			}
		case combined && r.URL.Path == globalCredentialsPath:
			if r.Method == http.MethodGet {
				list := []globalCredentialMetadata{}
				if credential != nil {
					list = append(list, *credential)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": list}})
			} else {
				var body map[string][]map[string]any
				if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || len(body["cliCredential"]) != 1 {
					t.Error("bad credential request")
					w.WriteHeader(400)
					return
				}
				payload := body["cliCredential"][0]
				if payload["password"] != "private-password" || payload["enablePassword"] != "private-enablePassword" {
					t.Error("wrong projected credential")
				}
				encoded, _ := json.Marshal(payload)
				credential = &globalCredentialMetadata{}
				json.Unmarshal(encoded, credential)
				credential.ID = "credential-id"
				credentialWrites++
				fmt.Fprint(w, `{"response":{"taskId":"credential-task"}}`)
			}
		case combined && r.URL.Path == siteCredentialsPath("area"):
			if r.Method == http.MethodPut {
				if credential == nil || len(journalState(t, a).CredentialApplications) != 1 {
					t.Error("assignment preceded credential receipt")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid assignment")
				}
				for k, v := range body {
					slots[k] = v
				}
				assignmentWrites++
				fmt.Fprint(w, `{"response":{"taskId":"assignment-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": slots})
			}
		case combined && r.URL.Path == globalPoolsPath:
			if r.Method == http.MethodPost {
				var p ipPool
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("invalid pool")
				}
				p.ID = "pool"
				pools = append(pools, p)
				ipamWrites++
				fmt.Fprint(w, `{"response":{"taskId":"pool-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": pools})
			}
		case combined && r.URL.Path == sitePoolsPath:
			if r.Method == http.MethodPost {
				var p ipReservation
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("invalid reservation")
				}
				if p.SiteID != "area" || p.V4 == nil || p.V4.GlobalPoolID != "pool" {
					t.Error("unresolved pool dependency")
				}
				p.ID = "reservation"
				reservations = append(reservations, p)
				ipamWrites++
				fmt.Fprint(w, `{"response":{"taskId":"reservation-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": reservations})
			}
		case combined && strings.HasSuffix(r.URL.Path, "/telemetrySettings"):
			if r.Method == http.MethodPut {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error("bad telemetry payload")
				}
				encoded, _ := json.Marshal(body)
				telemetry = string(encoded)
				telemetryWrites++
				fmt.Fprint(w, `{"response":{"taskId":"telemetry-task"}}`)
			} else {
				fmt.Fprint(w, `{"response":`+telemetry+`}`)
			}
		case combined && strings.HasSuffix(r.URL.Path, "/ntpSettings"):
			if r.Method == http.MethodPut {
				if err := json.NewDecoder(r.Body).Decode(&ntp); err != nil {
					t.Error("bad NTP payload")
				}
				ntpWrites++
				fmt.Fprint(w, `{"response":{"taskId":"ntp-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": ntp})
			}
		case r.URL.Path == sitesPath+"/"+site+"/aaaSettings" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"response": actual})
		case r.URL.Path == sitesPath+"/"+site+"/aaaSettings" && r.Method == http.MethodPut:
			var body map[string]map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad payload")
			}
			for _, g := range aaaGroups {
				if body[g.api]["sharedSecret"] != "DO-NOT-PRINT" {
					t.Error("secret missing")
				}
				delete(body[g.api], "sharedSecret")
				actual[g.api] = body[g.api]
			}
			if combined && (ntpWrites != 1 || telemetryWrites != 1 || ipamWrites != 2) {
				t.Error("AAA submitted before network settings or NTP replayed")
			}
			writes++
			fmt.Fprintf(w, `{"response":{"taskId":"aaa-task-%d"}}`, writes)
		default:
			t.Errorf("unexpected API %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	converge := func() {
		t.Helper()
		for i := 0; i < 48; i++ {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("AAA manifest did not converge", config.Status.Conditions)
	}
	converge()
	if writes != 1 || len(journalState(t, a).CredentialApplications) != expectedReceipts {
		t.Fatal("missing initial receipt")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.ReplaceAll(string(model), `"shared_secret_version":1`, `"shared_secret_version":2`))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tick()
	}
	if writes != 1 || journalState(t, a).Operation != nil {
		t.Fatal("projection lag caused write")
	}
	for _, path := range paths {
		if err := os.WriteFile(path, versionedSecretFixture(t, 2, "DO-NOT-PRINT"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	converge()
	for i := 0; i < 3; i++ {
		tick()
	}
	state := journalState(t, a)
	if writes != 2 || state.Operation != nil || len(state.CredentialApplications) != expectedReceipts {
		t.Fatal("rotation replay or missing receipt")
	}
	for _, receipt := range state.CredentialApplications {
		expectedVersion := int64(1)
		if strings.HasSuffix(receipt.Resource, "#aaa") {
			expectedVersion = 2
			if receipt.TargetID != site {
				t.Fatal("wrong AAA target")
			}
		} else if receipt.TargetID != "credential-id" {
			t.Fatal("wrong credential target")
		}
		for _, v := range receipt.Versions {
			if v.Version != expectedVersion {
				t.Fatal("incorrect recorded version")
			}
		}
	}
	if combined && (credentialWrites != 1 || assignmentWrites != 1) {
		t.Fatal("credential or assignment replayed")
	}

	if combined && (ntpWrites != 1 || telemetryWrites != 1 || ipamWrites != 2) {
		t.Fatal("network setting replayed across secret rotation")
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), "DO-NOT-PRINT") || strings.Contains(string(encoded), "private-") {
		t.Fatal("secret leaked to status")
	}
}
