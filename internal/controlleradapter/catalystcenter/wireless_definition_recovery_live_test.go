// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type definitionRecoveryTrace struct {
	base                           http.RoundTripper
	path, name, id, nameField      string
	posts, puts, deletes, attempts int
	allowDelete                    bool
	beforeRead                     func(*http.Request) bool
}

func (g *definitionRecoveryTrace) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet || r.URL.Path == authPath {
		if r.Method == http.MethodGet && g.beforeRead != nil && g.beforeRead(r) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"isolated qualification read failure"}`)), Request: r}, nil
		}
		return g.base.RoundTrip(r)
	}
	g.attempts++
	if r.Method == http.MethodDelete {
		if !g.allowDelete || g.id == "" || g.deletes != 0 || r.URL.Path != g.path+"/"+url.PathEscape(g.id) {
			return nil, errors.New("qualification refused unrelated/repeated deletion")
		}
		g.deletes++
	} else {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 131072))
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil || body[g.nameField] != g.name {
			return nil, errors.New("qualification refused unrelated payload")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == g.path && g.posts == 0 && g.id == "":
			g.posts++
		case r.Method == http.MethodPut && g.id != "" && r.URL.Path == g.path+"/"+url.PathEscape(g.id) && g.puts == 0:
			g.puts++
		default:
			return nil, errors.New("qualification refused repeated or unrelated mutation")
		}
	}
	return g.base.RoundTrip(r)
}

func definitionRecoveryNativeSnapshot(ctx context.Context, c *client, kind string) (map[string]string, any, error) {
	ids := map[string]string{}
	switch kind {
	case "ap_profiles":
		v, err := c.wirelessAPMetadataInventory(ctx)
		for k, row := range v {
			ids[k] = row.id
		}
		return ids, v, err
	case "anchor_groups":
		v, err := c.wirelessAnchorGroups(ctx)
		for k, row := range v {
			ids[k] = row.ID
		}
		return ids, v, err
	case "interfaces":
		v, err := c.wirelessInterfaces(ctx)
		for k, row := range v {
			ids[k] = row.ID
		}
		return ids, v, err
	case "power_profiles":
		v, err := c.wirelessPowerProfiles(ctx)
		for k, row := range v {
			ids[k] = row.ID
		}
		return ids, v, err
	case "dot11be_profiles":
		v, err := c.wirelessDot11beProfiles(ctx)
		for k, row := range v {
			ids[k] = row.ID
		}
		return ids, v, err
	case "security_settings":
		v, err := c.wirelessACLs(ctx)
		for k, row := range v {
			ids[k] = row.ID
		}
		return ids, v, err
	case "rf_profiles":
		v, err := c.wirelessRFProfiles(ctx)
		for k, row := range v {
			ids[k] = row.id
		}
		return ids, v, err
	}
	return nil, nil, errConfigurationTransition
}

// Real native APIs and public reconciliation; Kubernetes persistence is simulated.
// Acknowledged Unknown is injected after receipt persistence, not a taskless write.
func TestLiveWirelessDefinitionRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_DEFINITION_RECOVERY") != "isolated-definitions" {
		t.Skip("explicit isolated definition qualification not enabled")
	}
	entries := definitionRecoveryCases
	if os.Getenv("CATC_WIRELESS_ACCEPTED_METADATA_CREATE") == "isolated-definitions" {
		entries = append(append([]definitionRecoveryCase{}, entries...), wirelessAPDispatchCase)
	} else if os.Getenv("CATC_WIRELESS_AP_DISPATCH") == "isolated-definitions" {
		entries = []definitionRecoveryCase{wirelessAPDispatchCase}
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			c := liveClient(t)
			baselineIDs, baseline, err := definitionRecoveryNativeSnapshot(ctx, c, entry.name)
			if err != nil {
				t.Fatal("baseline", err)
			}
			name := fmt.Sprintf("CVK_DR_%012d", time.Now().UnixNano()%1_000_000_000_000)
			if entry.name == "security_settings" {
				name = strings.Replace(name, "CVK_DR_", "CVK_DR_443_", 1)
			}
			if entry.name == "rf_profiles" {
				name = strings.Replace(name, "CVK_DR_", "CVK_DR_100_", 1)
			}
			if _, exists := baselineIDs[name]; exists {
				t.Fatal("fixture already exists")
			}
			evidence, err := os.MkdirTemp("", "cvk-definition-recovery-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("fixture", name, "evidence", evidence)
			save := func(file string, v any) {
				t.Helper()
				raw, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(evidence, file), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			rest, _, err := c.session(ctx)
			if err != nil {
				t.Fatal(err)
			}
			fields := map[string]string{"ap_profiles": "apProfileName", "anchor_groups": "anchorGroupName", "interfaces": "interfaceName", "power_profiles": "profileName", "dot11be_profiles": "profileName", "security_settings": "aclListName", "rf_profiles": "rfProfileName"}
			trace := &definitionRecoveryTrace{base: rest.HTTPClient.Transport, path: entry.path, name: name, nameField: fields[entry.name]}
			rest.HTTPClient.Transport = trace
			defer func() { rest.HTTPClient.Transport = trace.base }()
			a, config := journalFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"wireless"}
			models := []string{entry.model, strings.Replace(entry.model, entry.from, entry.to, 1)}
			for i, model := range models {
				model = strings.ReplaceAll(model, "CVK_LAB", name)
				if entry.name == "security_settings" {
					model = strings.Replace(model, `"source_address":`, `"source_ports":"1-65535","source_address":`, 1)
				}
				models[i] = model
			}
			key := ctrlclient.ObjectKeyFromObject(config)
			injected := false
			staleRefused := false
			proveStale := os.Getenv("CATC_WIRELESS_DEFINITION_STALE") == "isolated-definitions"
			proveNoDispatch := os.Getenv("CATC_WIRELESS_NO_DISPATCH") == "isolated-definitions"
			refusalInjected, noDispatchRetired := false, false
			tasks := map[string]string{}
			createRecovery := os.Getenv("CATC_WIRELESS_DEFINITION_CREATE_RECOVERY") == "acknowledged-retain-only"
			createInjected := false
			for stage := range 2 {
				if err = a.statusReader.Get(ctx, key, config); err != nil {
					t.Fatal(err)
				}
				config.Spec.ManagedSections = []string{"wireless"}
				config.Generation++
				raw := models[stage]
				config.Spec.Source.Inline.Raw = []byte(raw)
				if err = a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				if stage == 1 && proveNoDispatch {
					trace.beforeRead = func(req *http.Request) bool {
						if refusalInjected || req.URL.Path != entry.path {
							return false
						}
						nc, readErr := a.currentController(req.Context())
						if readErr != nil {
							t.Error(readErr)
							return false
						}
						if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
							refusalInjected = true
							return true
						}
						return false
					}
				}
				var stale *configurationWrite
				if stage == 1 && proveStale {
					compiled, compileErr := c.compileWirelessConfiguration([]byte(raw))
					if compileErr != nil {
						t.Fatal(compileErr)
					}
					stale, err = compiled.next(ctx)
					if err != nil || stale == nil || stale.action != "Update" || stale.identity.target != trace.id {
						t.Fatal("stale qualification plan", name, err)
					}
				}
				converged := false
				for range 100 {
					restarted := *a
					if _, err = (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal("reconcile; retain fixture", name, trace.id, err)
					}
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if nc.Status.Configuration != nil {
						op := nc.Status.Configuration.Operation
						if op != nil && op.Phase == "Accepted" {
							tasks[op.Action] = op.TaskID
							if op.Action == "Create" && createRecovery && !createInjected {
								if op.TargetID != "" || !validAPIID(op.TaskID) {
									t.Fatal("unowned Create recovery identity invalid")
								}
								if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
									t.Fatal(err)
								}
								current, err := a.currentController(ctx)
								if err != nil {
									t.Fatal(err)
								}
								encoded, _ := json.Marshal(current)
								var restored cvk.NetworkController
								if err := json.Unmarshal(encoded, &restored); err != nil {
									t.Fatal(err)
								}
								if err := a.statusWriter.Status().Update(ctx, &restored); err != nil {
									t.Fatal(err)
								}
								createInjected = true
								t.Log("acknowledged unowned Create retained Unknown; task-tree observation only", op.TaskID)
							}
							if op.Action == "Update" && !injected {
								if op.TargetID != trace.id || !validAPIID(op.TaskID) {
									t.Fatal("retained update identity mismatch")
								}
								if err = a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
									t.Fatal(err)
								}
								nc, err = a.currentController(ctx)
								if err != nil {
									t.Fatal(err)
								}
								serialized, _ := json.Marshal(nc)
								var restored cvk.NetworkController
								if err = json.Unmarshal(serialized, &restored); err != nil {
									t.Fatal(err)
								}
								if err = a.statusWriter.Status().Update(ctx, &restored); err != nil {
									t.Fatal(err)
								}
								save("unknown-controller.json", &restored)
								injected = true
								t.Log("retained acknowledged update Unknown", trace.id, op.TaskID)
							}
						}
						save("controller.json", nc)
					}
					if err = a.statusReader.Get(ctx, key, config); err != nil {
						t.Fatal(err)
					}
					for _, condition := range config.Status.Conditions {
						if condition.Type == "Ready" && condition.Reason == "NativePreflightRefused" && !noDispatchRetired {
							if !refusalInjected || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.Owners) != 1 || trace.attempts != 1 {
								t.Fatal("no-dispatch proof did not retire exact unsubmitted operation", name)
							}
							noDispatchRetired = true
							save("no-dispatch-retired.json", nc)
							t.Log("preflight read refused; exact operation retired without mutation; owner retained", name)
						}
					}
					if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
						converged = true
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("qualification deadline; retain", name, trace.id)
					case <-time.After(100 * time.Millisecond):
					}
				}
				if !converged {
					t.Fatal("did not converge; retain", name, trace.id, config.Status.Phase)
				}
				if createRecovery {
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if !createInjected || len(current.Status.Configuration.ActionApplications) != 0 {
						t.Fatal("Create recovery fabricated ownership or skipped injection")
					}
				}
				ids, _, err := definitionRecoveryNativeSnapshot(ctx, c, entry.name)
				if err != nil {
					t.Fatal(err)
				}
				if stage == 0 {
					trace.id = ids[name]
					save("created.json", map[string]any{"name": name, "id": trace.id, "tasks": tasks})
					if !validAPIID(trace.id) {
						t.Fatal("created ID missing", name)
					}
				} else if ids[name] != trace.id || !injected {
					t.Fatal("update identity/recovery not proven", name)
				}
				if stale != nil {
					if _, err = stale.submit(ctx); !errors.Is(err, errConfigurationStale) {
						t.Fatal("stale native observation not refused", name, err)
					}
					if _, err = stale.submit(ctx); !errors.Is(err, errConfigurationTransition) {
						t.Fatal("stale closure not consumed", name, err)
					}
					staleRefused = true
					t.Log("stale observation refused and consumed before mutation", name, trace.id)
				}
				for range 3 {
					restarted := *a
					if _, err = (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal(err)
					}
				}
				if trace.posts != 1 || trace.puts != stage || trace.attempts != 1+stage {
					t.Fatal("unexpected mutation/replay", trace.posts, trace.puts, trace.attempts)
				}
			}
			// No association or device action is emitted. Verify the original unrelated
			// inventory identities before deleting only the captured generated object.
			ids, _, err := definitionRecoveryNativeSnapshot(ctx, c, entry.name)
			if err != nil {
				t.Fatal(err)
			}
			if ids[name] != trace.id {
				t.Fatal("cleanup identity changed")
			}
			delete(ids, name)
			if !reflect.DeepEqual(ids, baselineIDs) {
				t.Fatal("unrelated native identities changed; retain", name, trace.id)
			}
			trace.allowDelete = true
			submitted := time.Now()
			receipt, err := c.mutate(ctx, http.MethodDelete, entry.path+"/"+url.PathEscape(trace.id), nil)
			if err != nil {
				t.Fatal("cleanup uncertain; retain", name, trace.id, err)
			}
			var accepted struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(receipt, &accepted) != nil || !validAPIID(accepted.Response.ID) {
				t.Fatal("cleanup acknowledgement missing", name, trace.id)
			}
			tasks["Delete"] = accepted.Response.ID
			if err = waitSiteCredentialTask(ctx, c, accepted.Response, submitted); err != nil {
				t.Fatal("cleanup task uncertain", name, trace.id, err)
			}
			_, after, err := definitionRecoveryNativeSnapshot(ctx, c, entry.name)
			if err != nil || !reflect.DeepEqual(baseline, after) {
				t.Fatal("baseline not restored", name, trace.id, err)
			}
			if trace.posts != 1 || trace.puts != 1 || trace.deletes != 1 || trace.attempts != 3 {
				t.Fatal("incorrect final mutation count")
			}
			if proveStale && !staleRefused {
				t.Fatal("stale submission qualification missing")
			}
			if proveNoDispatch && (!refusalInjected || !noDispatchRetired) {
				t.Fatal("automatic no-dispatch recovery missing")
			}
			save("complete.json", map[string]any{"name": name, "id": trace.id, "tasks": tasks, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes, "attempts": trace.attempts, "recoveredUnknown": injected, "staleRefused": staleRefused, "noDispatchRetired": noDispatchRetired, "cleaned": true})
			t.Log("complete", name, trace.id, "tasks", tasks, "mutations", trace.attempts, "baseline restored")
		})
		if t.Failed() {
			break
		} // Retain one uncertain fixture rather than starting more.
	}
}

func TestLiveWirelessAPDispatchRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_AP_DISPATCH") != "isolated-definitions" {
		t.Skip("isolated AP dispatch qualification disabled")
	}
	t.Setenv("CATC_WIRELESS_DEFINITION_RECOVERY", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_DEFINITION_STALE", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_NO_DISPATCH", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_DEFINITION_CREATE_RECOVERY", "acknowledged-retain-only")
	TestLiveWirelessDefinitionRecovery(t)
}

func TestLiveWirelessAcceptedMetadataCreates(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_ACCEPTED_METADATA_CREATE") != "isolated-definitions" {
		t.Skip("isolated accepted metadata Create qualification disabled")
	}
	t.Setenv("CATC_WIRELESS_DEFINITION_RECOVERY", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_DEFINITION_STALE", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_NO_DISPATCH", "isolated-definitions")
	t.Setenv("CATC_WIRELESS_DEFINITION_CREATE_RECOVERY", "")
	TestLiveWirelessDefinitionRecovery(t)
}
