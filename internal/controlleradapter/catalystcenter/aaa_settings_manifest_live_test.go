// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type liveAAASettingsTrace struct {
	base                   http.RoundTripper
	t                      *testing.T
	siteID                 string
	writes                 atomic.Int64
	secrets                map[string]string
	redactions             []string
	registrationIDs        map[string]bool
	reset                  *aaaResetIntent
	childSiteID, childName string
}

func (trace *liveAAASettingsTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	path := sitesPath + "/" + trace.siteID + "/aaaSettings"
	if trace.childSiteID != "" {
		path = sitesPath + "/" + trace.childSiteID + "/aaaSettings"
	}
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		if req.Method == http.MethodPut && req.URL.Path == path {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
			if trace.reset != nil {
				var actual map[string]any
				expected, err := trace.reset.payload()
				if err != nil || json.Unmarshal(body, &actual) != nil || !reflect.DeepEqual(actual, expected) {
					return nil, errors.New("unexpected owned AAA reset payload")
				}
			} else {
				var groups map[string]map[string]string
				if json.Unmarshal(body, &groups) != nil || len(groups) != 2 {
					return nil, errors.New("invalid isolated AAA payload")
				}
				for _, group := range aaaGroups {
					if groups[group.api]["sharedSecret"] != trace.secrets[group.api] {
						return nil, errors.New("unexpected isolated AAA credential")
					}
				}
			}
			trace.writes.Add(1)
		} else if req.Method == http.MethodPost && req.URL.Path == "/dna/intent/api/v1/areas" && trace.childName != "" {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
			var area map[string]string
			if json.Unmarshal(body, &area) != nil || len(area) != 2 || area["name"] != trace.childName || area["parentId"] != trace.siteID {
				return nil, errors.New("unexpected AAA child creation")
			}
		} else if req.Method != http.MethodDelete || (req.URL.Path != "/dna/intent/api/v1/areas/"+trace.siteID && (trace.childSiteID == "" || req.URL.Path != "/dna/intent/api/v1/areas/"+trace.childSiteID) && !(strings.HasPrefix(req.URL.Path, authenticationPolicyServersPath+"/") && trace.registrationIDs[strings.TrimPrefix(req.URL.Path, authenticationPolicyServersPath+"/")])) {
			return nil, errors.New("isolated AAA test refused an out-of-scope write")
		}
	}
	response, err := trace.base.RoundTrip(req)
	if req.Method == http.MethodPut && req.URL.Path == path {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		trace.t.Log("isolated site AAA response", "HTTP", status, "transportError", err != nil)
		logLiveAAAValidation(trace.t, response, trace.redactions)
	}
	return response, err
}

func TestLiveCatalystCenterAAASettingsManifest(t *testing.T) {
	if os.Getenv("CATC_AAA_SETTINGS_EXECUTION") != "isolated-empty-area" {
		t.Skip("isolated empty-area AAA execution not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	areaID := ""
	registrationIDs := liveAAASettingsRegistrations(t, c, ctx, &areaID)
	path := liveCredentialAssignmentSite(t, c)
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var site Site
	globalID := ""
	for _, s := range sites {
		if s.NameHierarchy == path {
			site = s
		}
		if s.Type == "global" {
			globalID = s.ID
		}
	}
	if site.Type != "area" || !validAPIID(globalID) {
		t.Fatal("isolated AAA site identity")
	}
	id := site.ID
	areaID = id
	global, err := c.readNetworkSetting(ctx, globalID, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := c.readNetworkSetting(ctx, id, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range aaaGroups {
		if len(baseline.aaa[group.api]) != 0 {
			t.Fatal("new area has explicit unowned AAA settings; no write")
		}
	}
	checkScope := func(checkCtx context.Context) {
		t.Helper()
		current, err := c.ListSites(checkCtx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range current {
			if s.ID == id || s.NameHierarchy == site.NameHierarchy {
				if s.ID != id || s.NameHierarchy != site.NameHierarchy || s.ParentID != site.ParentID || s.Type != "area" {
					t.Fatal("AAA area identity changed; retain")
				}
				found = true
			}
			if strings.HasPrefix(s.NameHierarchy, site.NameHierarchy+"/") {
				t.Fatal("AAA area gained children; retain")
			}
		}
		if !found {
			t.Fatal("AAA area disappeared; retain")
		}
		assigned, err := c.assignedDevices(checkCtx, id)
		if err != nil || len(assigned) != 0 {
			t.Fatal("AAA area has devices or unreadable scope; retain", err)
		}
		currentGlobal, err := c.readNetworkSetting(checkCtx, globalID, "aaa")
		if err != nil || !reflect.DeepEqual(currentGlobal, global) {
			t.Fatal("global AAA metadata changed; retain", err)
		}
	}
	checkScope(ctx)
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		checkScope(cleanupCtx)
	})
	rest, token, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveAAASettingsTrace{base: base, t: t, siteID: id, secrets: map[string]string{}, redactions: []string{token, os.Getenv("CATC_PASSWORD")}, registrationIDs: registrationIDs}
	if registrationTrace, ok := base.(*liveAAARegistrationTrace); ok {
		trace.redactions = append(trace.redactions, registrationTrace.redactions...)
	}
	rest.HTTPClient.Transport = trace
	a, config, source, _ := credentialVersionJournalFixture(t)
	a.client, a.intentSecretPath = c, source.secrets.root
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	nc.Spec.CredentialSecretRef = ciskov1.NetworkControllerSecretReference{Name: "credentials"}
	nc.Spec.IntentSecretSources = nil
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	config.Spec.SecretRefs = nil
	paths := map[string]string{}
	allSecrets := []string{}
	for _, group := range aaaGroups {
		alias := strings.ToLower(group.api)
		pointer := "/aaa_servers/0/" + group.model + "/shared_secret"
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: alias, Name: "isolated-aaa", Key: alias})
		config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: pointer, Source: alias})
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: pointer, SourceAlias: alias, SecretName: "isolated-aaa", SecretKey: alias})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(a.intentSecretPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		paths[group.api] = path
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	writeProjection := func(version int64) {
		t.Helper()
		for _, group := range aaaGroups {
			nonce := make([]byte, 24)
			if _, err := rand.Read(nonce); err != nil {
				t.Fatal(err)
			}
			value := hex.EncodeToString(nonce)
			trace.secrets[group.api] = value
			trace.redactions = append(trace.redactions, value)
			allSecrets = append(allSecrets, value)
			if err := os.WriteFile(paths[group.api], versionedSecretFixture(t, version, value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	model := func(version int64) []byte {
		return []byte(fmt.Sprintf(`{"catalyst_center":{"sites":{"areas":[{"name":%q,"parent_name":"Global","network_settings":{"aaa_servers":"isolated"}}]},"network_settings":{"aaa_servers":[{"name":"isolated","network_aaa":{"server_type":"AAA","protocol":"TACACS","primary_ip":"192.0.2.240","shared_secret_version":%d},"client_and_endpoint_aaa":{"server_type":"AAA","protocol":"RADIUS","primary_ip":"192.0.2.241","shared_secret_version":%d}}]}}}`, site.Name, version, version))
	}
	tick := func() {
		t.Helper()
		checkScope(ctx)
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal("AAA public reconcile", err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
	}
	for phase := int64(0); phase < 4; phase++ {
		version := min(phase+1, int64(2))
		config.Generation++
		raw := model(version)
		if phase == 2 {
			var doc map[string]any
			if json.Unmarshal(raw, &doc) != nil {
				t.Fatal("AAA model")
			}
			profile := doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["aaa_servers"].([]any)[0].(map[string]any)
			profile["network_aaa"].(map[string]any)["secondary_ip"] = "192.0.2.241"
			profile["client_and_endpoint_aaa"].(map[string]any)["secondary_ip"] = "192.0.2.240"
			raw, _ = json.Marshal(doc)
		}
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		if phase == 0 {
			writeProjection(version)
		} else if phase == 1 {
			before := trace.writes.Load()
			tick()
			if trace.writes.Load() != before || journalState(t, a).Operation != nil {
				t.Fatal("stale projected AAA version dispatched")
			}
			t.Log("AAA version projection lag refused without a native write")
			writeProjection(version)
		}
		converged := false
		for step := 0; step < 90; step++ {
			tick()
			state := journalState(t, a)
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				if state.Operation != nil || len(state.CredentialApplications) != 1 || trace.writes.Load() != phase+1 {
					t.Fatal("AAA convergence lacks exact versioned ownership or replayed")
				}
				r := state.CredentialApplications[0]
				if r.TargetID != id || r.ConfigUID != string(config.UID) || len(r.Versions) != 2 {
					t.Fatal("AAA credential receipt binding")
				}
				for _, v := range r.Versions {
					if v.Version != version {
						t.Fatal("AAA receipt version mismatch")
					}
				}
				for i := 0; i < 2; i++ {
					tick()
				}
				if trace.writes.Load() != phase+1 || journalState(t, a).Operation != nil {
					t.Fatal("AAA restarted no-op replayed")
				}
				encoded, _ := json.Marshal(journalState(t, a))
				for _, secret := range allSecrets {
					if bytes.Contains(encoded, []byte(secret)) {
						t.Fatal("AAA secret escaped into status")
					}
				}
				t.Log("AAA public manifest reached InSync with receipt, simulated restart and no-op", "phase", phase+1, "credentialVersion", version, "secondaryServers", phase == 2)
				converged = true
				break
			}
			if state.Operation != nil && (state.Operation.Phase == "Unknown" || state.Operation.Phase == "Failed") {
				t.Fatal("AAA operation retained without replay", state.Operation.Phase, state.Operation.TaskID)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("AAA public manifest failed before convergence")
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !converged {
			t.Fatal("AAA public manifest did not converge; retain area")
		}
	}
	checkScope(ctx)
	if os.Getenv("CATC_AAA_RESET_EXECUTION") == "owned-empty-area" {
		qualifyLiveOwnedAAAReset(t, c, ctx, a, config, trace, checkScope)
	}
	t.Log("AAA create/rotation completed only on the empty area; global AAA metadata unchanged; generated credentials not logged")
}

func logLiveAAAValidation(t *testing.T, response *http.Response, redactions []string) {
	if response != nil && response.StatusCode >= 400 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), response.Body), response.Body}
		var envelope struct {
			Response struct {
				ErrorCode string `json:"errorCode"`
				Message   string `json:"message"`
			} `json:"response"`
		}
		if readErr == nil && len(body) <= 8192 && json.Unmarshal(body, &envelope) == nil {
			message := string(body)
			for _, secret := range redactions {
				if secret != "" {
					message = strings.ReplaceAll(message, secret, "<redacted>")
				}
			}
			t.Log("isolated AAA validation", message)
		}
	}
}
