// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const aaaRegistrationFixture = `{"response":[{"instanceUuid":"aaa-server","ipAddress":"192.0.2.10","isIseEnabled":false,"protocol":"TACACS"}]}`

func TestAAASettingsRequireRegisteredServers(t *testing.T) {
	for _, mode := range []string{"valid", "dual-protocol", "missing", "wrong-protocol", "ise-record", "unknown-type", "secondary-missing", "reader-failure", "replacement-before-submit"} {
		t.Run(mode, func(t *testing.T) {
			e := aaaExecutorFixture(t)
			registry := aaaRegistrationFixture
			switch mode {
			case "dual-protocol":
				registry = strings.Replace(registry, `"TACACS"`, `"RADIUS_TACACS"`, 1)
			case "missing":
				registry = `{"response":[]}`
			case "wrong-protocol":
				registry = strings.Replace(registry, `"TACACS"`, `"RADIUS"`, 1)
			case "ise-record":
				registry = strings.Replace(registry, `false`, `true`, 1)
			case "unknown-type":
				registry = strings.Replace(registry, `"isIseEnabled":false,`, ``, 1)
			case "secondary-missing":
				e.bindings[0].settings[0].aaa["aaaNetwork"]["secondaryServerIp"] = "192.0.2.11"
			}
			writes := 0
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == authenticationPolicyServersPath:
					if mode == "reader-failure" {
						w.WriteHeader(400)
						return
					}
					fmt.Fprint(w, registry)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/aaaSettings"):
					fmt.Fprint(w, `{"response":{"aaaNetwork":null,"aaaClient":null}}`)
				case r.Method == http.MethodPut && r.URL.Path == sitesPath+"/root/aaaSettings":
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Error("unexpected API")
					w.WriteHeader(400)
				}
			})
			write, err := e.next(context.Background())
			valid := mode == "valid" || mode == "dual-protocol" || mode == "replacement-before-submit"
			if !valid {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("unregistered or incompatible AAA reference released a write")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("valid registration blocked", err)
			}
			if mode == "replacement-before-submit" {
				registry = strings.Replace(registry, `aaa-server`, `replacement`, 1)
			}
			_, err = write.submit(context.Background())
			if (err == nil) != (mode != "replacement-before-submit") {
				t.Fatal("unexpected dispatch result", err)
			}
			expected := 1
			if mode == "replacement-before-submit" {
				expected = 0
			}
			if writes != expected {
				t.Fatal("stale registration dispatched")
			}
			if _, err := write.submit(context.Background()); err == nil || writes != expected {
				t.Fatal("attempted AAA plan replayed")
			}
		})
	}
}

func TestAAASettingsVerificationRefusesMissingRegistration(t *testing.T) {
	e := aaaExecutorFixture(t)
	_, versions, err := resolveVersionedConfigurationCredentials(e.secrets, e.owner, "network_settings", e.bindings[0].settings[0].aaaCredentials)
	if err != nil {
		t.Fatal(err)
	}
	e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case authenticationPolicyServersPath:
			fmt.Fprint(w, `{"response":[]}`)
		default:
			t.Error("missing registration should fence verification before native settings read")
			w.WriteHeader(400)
		}
	})
	op := &ciskov1.NetworkControllerConfigurationOperation{Section: "network_settings", ConfigUID: e.owner, Action: "Update", Resource: "Global#aaa", TargetID: "root", CredentialVersions: versions}
	if _, ok, err := e.verifyCredential(context.Background(), op); err == nil || ok {
		t.Fatal("missing dependency certified AAA settings")
	}
}
