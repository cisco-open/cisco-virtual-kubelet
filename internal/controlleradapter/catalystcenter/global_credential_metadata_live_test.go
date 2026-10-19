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
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type liveNoAuthCredentialTrace struct {
	base                   http.RoundTripper
	name, username, target string
	writes                 int
}

func (trace *liveNoAuthCredentialTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		if req.Method == http.MethodDelete && trace.target != "" && req.URL.Path == globalCredentialsPath+"/"+trace.target {
			return trace.base.RoundTrip(req)
		}
		if req.URL.Path != globalCredentialsPath || req.Method != http.MethodPost && req.Method != http.MethodPut {
			return nil, errors.New("isolated SNMP metadata test refused an out-of-scope mutation")
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil || len(body) != 1 {
			return nil, errors.New("invalid isolated SNMP metadata envelope")
		}
		var payload map[string]any
		expected := map[string]any{"description": trace.name, "username": trace.username, "snmpMode": "NOAUTHNOPRIV"}
		if req.Method == http.MethodPost {
			var list []map[string]any
			if trace.writes != 0 || json.Unmarshal(body["snmpV3"], &list) != nil || len(list) != 1 {
				return nil, errors.New("isolated SNMP create replay refused")
			}
			payload = list[0]
		} else {
			if trace.target == "" || json.Unmarshal(body["snmpV3"], &payload) != nil {
				return nil, errors.New("isolated SNMP update identity unavailable")
			}
			expected["id"] = trace.target
		}
		if !reflect.DeepEqual(expected, payload) {
			return nil, errors.New("isolated SNMP payload differs from authorized metadata")
		}
		trace.writes++
	}
	return trace.base.RoundTrip(req)
}

func TestLiveGlobalCredentialNoAuthManifest(t *testing.T) {
	if os.Getenv("CATC_NOAUTH_CREDENTIAL_EXECUTION") != "isolated-unassigned-credential" {
		t.Skip("isolated unassigned NOAUTHNOPRIV execution not enabled")
	}
	qualifyLiveGlobalCredentialNoAuthManifest(t, nil)
}

func TestLiveGlobalCredentialMetadataKnownTaskRecovery(t *testing.T) {
	if os.Getenv("CATC_NOAUTH_CREDENTIAL_RECOVERY") != "isolated-unassigned-credential" {
		t.Skip("isolated metadata recovery disabled")
	}
	qualifyLiveGlobalCredentialNoAuthManifest(t, nil, true)
}

func qualifyLiveGlobalCredentialNoAuthManifest(t *testing.T, api ctrlclient.Client, recovery ...bool) {
	t.Helper()
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	before, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal("cannot generate isolated SNMP fixture")
	}
	name := "CVK-NoAuth-" + hex.EncodeToString(nonce)
	if _, exists := before["snmpv3_credentials"][name]; exists {
		t.Fatal("isolated SNMP name is already occupied")
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveNoAuthCredentialTrace{base: base, name: name, username: "cvkNoAuthUser"}
	previousWrap := c.wrapTransport
	wrap := func(base http.RoundTripper) http.RoundTripper {
		if previousWrap != nil {
			base = previousWrap(base)
		}
		if base == nil {
			base = http.DefaultTransport
		}
		trace.base = base
		return trace
	}
	c.mu.Lock()
	rest.HTTPClient.Transport = wrap(rest.HTTPClient.Transport)
	c.wrapTransport = wrap
	c.mu.Unlock()
	a, config := noAuthCredentialConfigurationFixture(t)
	if api != nil {
		seedLiveOwnedMetadataAPI(t, a, config, api, c.cfg.Endpoint)
	}
	a.client = c
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if api == nil {
		nc.Spec.Endpoint = c.cfg.Endpoint
		if err := a.statusWriter.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	} else if nc.Spec.Endpoint != c.cfg.Endpoint {
		t.Fatal("private API controller must be configured before pinning worker identity")
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: noAuthCredentialModel(name, trace.username)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	recoverTasks := len(recovery) > 0 && recovery[0]
	if recoverTasks {
		dir, err := os.MkdirTemp("/private/tmp", "cvk-metadata-task-recovery-")
		if err != nil {
			t.Fatal(err)
		}
		baseClient := a.statusWriter
		checkpoint := func() error {
			var current cvk.NetworkController
			var cfg configapi.NetworkControllerConfig
			if err := baseClient.Get(ctx, a.key, &current); err != nil {
				return err
			}
			if err := baseClient.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &cfg); err != nil {
				return err
			}
			raw, err := json.Marshal(struct {
				Controller cvk.NetworkController
				Config     configapi.NetworkControllerConfig
			}{current, cfg})
			if err != nil {
				return err
			}
			tmp := filepath.Join(dir, "checkpoint.tmp")
			if err := os.WriteFile(tmp, raw, 0600); err != nil {
				return err
			}
			return os.Rename(tmp, filepath.Join(dir, "checkpoint.json"))
		}
		a.statusWriter = &siteLifecycleCheckpointClient{Client: baseClient, checkpoint: checkpoint}
		if err := checkpoint(); err != nil {
			t.Fatal(err)
		}
		t.Log("private metadata recovery checkpoint", dir)
	}
	recoveredTasks := 0
	// Cleanup requires the independently verified receipt and exact UUID. An
	// uncertain task or lost acknowledgement retains the fixture for inspection.
	t.Cleanup(func() {
		state := journalState(t, a)
		if state == nil || state.Operation != nil || len(state.ActionApplications) != 1 || trace.target == "" {
			if state != nil && state.Operation != nil {
				t.Log("unsettled SNMP fixture retained", "operation", state.Operation.ID, "task", state.Operation.TaskID, "phase", state.Operation.Phase)
			}
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		actual, err := c.globalCredentials(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		got, exists := actual["snmpv3_credentials"][name]
		if !exists || got.ID != trace.target || got.Username != trace.username || got.Mode != "NOAUTHNOPRIV" || got.AuthType != "" || got.PrivacyType != "" {
			t.Error("SNMP cleanup refused missing, changed or replaced fixture")
			return
		}
		submitted := time.Now()
		raw, err := c.mutate(cleanupCtx, http.MethodDelete, globalCredentialsPath+"/"+url.PathEscape(trace.target), nil)
		if err != nil {
			t.Error(err)
			return
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Error("SNMP cleanup acknowledgement unavailable")
			return
		}
		for {
			done, err := c.configurationTaskTreeComplete(cleanupCtx, ack.Response.ID, submitted, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			if done {
				break
			}
			select {
			case <-cleanupCtx.Done():
				t.Error("SNMP cleanup task did not complete")
				return
			case <-time.After(time.Second):
			}
		}
		after, err := c.globalCredentials(cleanupCtx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("SNMP cleanup did not restore complete non-secret credential baseline", err)
			return
		}
		t.Log("isolated SNMP credential removed; complete non-secret baseline restored", "target", trace.target, "cleanupTask", ack.Response.ID)
	})
	initialOwnedDigest := ""
	for step := 0; step < 2; step++ {
		if step == 1 {
			trace.username = "cvkNoAuthUpdated"
			config.Generation++
			config.Spec.Source.Inline.Raw = noAuthCredentialModel(name, trace.username)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		markedUnknown := false
		deadline := time.Now().Add(2 * time.Minute)
		for {
			if api != nil {
				if _, err := a.currentController(ctx); err != nil {
					t.Fatal("private API worker identity changed", err)
				}
				if config.Namespace != a.key.Namespace || config.Spec.ControllerRef.Name != a.key.Name {
					t.Fatal("private API config does not target the pinned worker")
				}
			}
			noAuthCredentialTick(t, a, config)
			state := journalState(t, a)
			if recoverTasks && !markedUnknown && state.Operation != nil && state.Operation.Phase == "Accepted" {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				markedUnknown = true
				recoveredTasks++
				t.Log("original metadata task retained as Unknown", state.Operation.TaskID)
			}
			if api != nil && config.Status.ObservedGeneration == 0 {
				t.Fatal("private API reconcile returned without observing the current config")
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("SNMP manifest did not converge; retained journal requires inspection", config.Status.Conditions)
			}
			select {
			case <-ctx.Done():
				t.Fatal("SNMP qualification deadline exceeded")
			case <-time.After(time.Second):
			}
		}
		if recoverTasks && recoveredTasks != step+1 {
			t.Fatal("missing original-task recovery")
		}
		state := journalState(t, a)
		if state.Operation != nil || len(state.CredentialApplications) != 0 || len(state.ActionApplications) != 1 || trace.writes != step+1 {
			t.Fatal("SNMP execution did not use exactly one non-secret receipt")
		}
		target, err := metadataCredentialReceiptTarget(&state.ActionApplications[0], time.Now())
		if err != nil || trace.target != "" && target != trace.target {
			t.Fatal("SNMP exact native identity changed", err)
		}
		trace.target = target
		for range 3 {
			noAuthCredentialTick(t, a, config)
		}
		if trace.writes != step+1 {
			t.Fatal("restarted SNMP no-op replayed a request")
		}
		if os.Getenv("CATC_WIRED_BULK_OWNED_METADATA_EXECUTION") == "native-public-manifest-simulated-kubernetes" {
			digest := qualifyLiveWiredBulkOwnedMetadata(t, a, ctx, config)
			if step == 0 {
				initialOwnedDigest = digest
			} else if digest == initialOwnedDigest {
				t.Fatal("metadata update retained original bulk prerequisite identity")
			}
		}
		t.Log("NOAUTHNOPRIV public manifest phase verified", "step", step, "target", target, "task", state.ActionApplications[0].Operation.TaskID)
	}
}
