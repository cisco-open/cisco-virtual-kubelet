//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type globalRecoveryNativeTrace struct {
	base            http.RoundTripper
	expected        map[string]any
	target          string
	writes, deletes int
	maxWrites       int
	allowDelete     bool
}

func (g *globalRecoveryNativeTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.Method == http.MethodPost && req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	if req.Method == http.MethodDelete && g.allowDelete && g.deletes == 0 && g.target != "" && req.URL.Path == globalCredentialsPath+"/"+g.target && req.ContentLength <= 0 {
		g.deletes++
		return g.base.RoundTrip(req)
	}
	limit := g.maxWrites
	if limit == 0 {
		limit = 3
	}
	if req.URL.Path != globalCredentialsPath || g.writes >= limit || req.Method != http.MethodPost && req.Method != http.MethodPut || req.GetBody == nil {
		return nil, errors.New("unused SNMP recovery qualifier refused mutation scope")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var envelope map[string]json.RawMessage
	if json.NewDecoder(body).Decode(&envelope) != nil || len(envelope) != 1 {
		return nil, errInvalidResponse
	}
	var payload map[string]any
	if g.writes == 0 {
		var rows []map[string]any
		if req.Method != http.MethodPost || json.Unmarshal(envelope["snmpV3"], &rows) != nil || len(rows) != 1 {
			return nil, errConfigurationTransition
		}
		payload = rows[0]
	} else {
		if req.Method != http.MethodPut || g.target == "" || json.Unmarshal(envelope["snmpV3"], &payload) != nil || payload["id"] != g.target {
			return nil, errConfigurationTransition
		}
	}
	if !reflect.DeepEqual(payload, g.expected) {
		return nil, errors.New("unused SNMP payload differs from exact generated declaration")
	}
	g.writes++
	return g.base.RoundTrip(req)
}

func TestLiveGlobalCredentialMetadataPromotion(t *testing.T) {
	if os.Getenv("CATC_GLOBAL_METADATA_PROMOTION") != "isolated-unassigned-snmpv3" {
		t.Skip("explicit unassigned metadata promotion qualification disabled")
	}
	exerciseLiveGlobalCredentialKnownTaskRecovery(t, true, false)
}

func TestLiveGlobalCredentialKnownTaskRecovery(t *testing.T) {
	if os.Getenv("CATC_GLOBAL_CREDENTIAL_RECOVERY") != "isolated-snmpv3-promotion" {
		t.Skip("explicit unassigned SNMPv3 recovery qualification disabled")
	}
	exerciseLiveGlobalCredentialKnownTaskRecovery(t, false, false)
}

func TestLiveGlobalCredentialMetadataPrivacyPromotion(t *testing.T) {
	if os.Getenv("CATC_GLOBAL_METADATA_PROMOTION") != "isolated-unassigned-snmpv3-direct-privacy" {
		t.Skip("explicit unassigned direct privacy promotion qualification disabled")
	}
	exerciseLiveGlobalCredentialKnownTaskRecovery(t, true, true)
}

func exerciseLiveGlobalCredentialKnownTaskRecovery(t *testing.T, promoteMetadata, directPrivacy bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	random := func() string {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(buf)
	}
	name := "CVK-SNMP-Recover-" + random()[:16]
	if _, found := baseline["snmpv3_credentials"][name]; found {
		t.Fatal("generated name exists")
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-global-credential-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private unused SNMP recovery checkpoint:", dir)
	a, config, _, _ := credentialVersionJournalFixture(t)
	a.client = c
	a.intentSecretPath = filepath.Join(dir, "projection")
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	nc.Spec.CredentialSecretRef = cvk.NetworkControllerSecretReference{Name: "api-credentials"}
	nc.Spec.IntentSecretSources = nil
	full, snapshot := globalCredentialFixture(t, "snmpv3_credentials")
	paths := map[string]string{}
	refs := []configapi.NetworkControllerSecretRef{}
	authOne, authTwo, privacy := random(), random(), random()
	for _, value := range snapshot.values {
		alias := strings.ToLower(value.ref.Source)
		source := cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: "generated-unused-snmp", Key: alias}
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, source)
		ref := value.ref
		ref.Source = alias
		refs = append(refs, ref)
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: ref.Path, SourceAlias: alias, SecretName: source.Name, SecretKey: source.Key})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(a.intentSecretPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		secret := authOne
		if strings.HasSuffix(ref.Path, "/privacy_password") {
			secret = privacy
		}
		if err := os.WriteFile(path, versionedSecretFixture(t, 1, secret), 0600); err != nil {
			t.Fatal(err)
		}
		paths[ref.Path] = path
	}
	_ = full
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true, Exporter: "cvk-unused-snmp-recovery-qualification"}
	writeJSON := func(file string, value any) error {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, file+".tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(dir, file))
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
		return writeJSON("checkpoint.json", struct {
			Controller     cvk.NetworkController
			Config         configapi.NetworkControllerConfig
			ProjectionRoot string
		}{current, cfg, a.intentSecretPath})
	}
	a.statusWriter = &siteLifecycleCheckpointClient{Client: baseClient, checkpoint: checkpoint}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stages := []int{0, 1, 2}
	if promoteMetadata {
		stages = []int{-1, 0, 1, 2}
	}
	if directPrivacy {
		stages = []int{-1, 1, 2}
	}
	trace := &globalRecoveryNativeTrace{maxWrites: len(stages)}
	previous := c.wrapTransport
	wrap := func(base http.RoundTripper) http.RoundTripper {
		if previous != nil {
			base = previous(base)
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
	// Failure retains exact intent/tasks/generated projection. No deferred delete.
	tasks := []string{}
	recovered := map[string]string{}
	var origin *cvk.NetworkControllerConfigurationActionApplication
	for iteration, step := range stages {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Spec.ManagedSections = []string{"network_settings"}
		config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true, Exporter: "cvk-unused-snmp-recovery-qualification"}

		mode := "AUTHNOPRIV"
		if step < 0 {
			mode = "NOAUTHNOPRIV"
		}
		authVersion := int64(1)
		if step > 0 {
			mode = "AUTHPRIV"
		}
		if step == 2 {
			authVersion = 2
			for path, file := range paths {
				if strings.HasSuffix(path, "/auth_password") {
					if err := os.WriteFile(file, versionedSecretFixture(t, 2, authTwo), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		item := map[string]any{"name": name, "username": "cvk-unused-snmp", "snmp_mode": mode, "auth_type": "SHA", "auth_password_version": authVersion}
		if step < 0 {
			delete(item, "auth_type")
			delete(item, "auth_password_version")
		}
		if step > 0 {
			item["privacy_type"] = "AES128"
			item["privacy_password_version"] = 1
		}
		model, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": map[string]any{"snmpv3_credentials": []any{item}}}}})
		if os.Getenv("CATC_GLOBAL_CREDENTIAL_DEFAULTS") == "pinned-active-protocols" {
			model = missingCredentialDefaults(t, model)
			var overrides []byte
			if os.Getenv("CATC_GLOBAL_CREDENTIAL_DEFAULT_OVERRIDE") == "snmp-privacy-aes256" {
				overrides = []byte("defaults:\n  catalyst_center:\n    network_settings:\n      device_credentials:\n        snmpv3_credentials:\n          privacy_type: AES256\n")
			}
			resolved, meta, err := ExportCredentialDefaultsWithOverrides(model, overrides)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(fmt.Sprintf("defaults-step-%d.json", step), meta); err != nil {
				t.Fatal(err)
			}
			model = resolved
			config.Spec.ModelSource.Exporter = meta.Exporter
		}
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: model}
		config.Spec.SecretRefs = nil
		for _, ref := range refs {
			if step > 0 || step == 0 && strings.HasSuffix(ref.Path, "/auth_password") {
				config.Spec.SecretRefs = append(config.Spec.SecretRefs, ref)
			}
		}
		config.Generation++
		trace.expected = map[string]any{"description": name, "username": "cvk-unused-snmp", "snmpMode": mode, "authType": "SHA", "authPassword": authOne}
		if step < 0 {
			delete(trace.expected, "authType")
			delete(trace.expected, "authPassword")
		}
		if step == 2 {
			trace.expected["authPassword"] = authTwo
		}
		if iteration > 0 {
			trace.expected["id"] = trace.target
		}
		if step > 0 {
			trace.expected["privacyType"] = "AES128"
			if os.Getenv("CATC_GLOBAL_CREDENTIAL_DEFAULTS") == "pinned-active-protocols" && os.Getenv("CATC_GLOBAL_CREDENTIAL_DEFAULT_OVERRIDE") == "snmp-privacy-aes256" {
				trace.expected["privacyType"] = "AES256"
			}
			trace.expected["privacyPassword"] = privacy
			trace.expected["id"] = trace.target
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for tries := 0; tries < 240; tries++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && recovered[state.Operation.ID] == "" {
				op := state.Operation
				if !validAPIID(op.TaskID) || len(op.CredentialVersions) != max(0, 1+min(step, 1)) {
					t.Fatal("acknowledged version binding missing")
				}
				recovered[op.ID] = op.TaskID
				tasks = append(tasks, op.TaskID)
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				t.Log("retained original credential task as Unknown", op.Action, op.TaskID)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation && state.Operation == nil {
				break
			}
			if tries == 239 {
				t.Fatal("credential recovery did not converge; retain checkpoint", config.Status.Conditions)
			}
			select {
			case <-ctx.Done():
				t.Fatal("credential recovery expired; retain checkpoint")
			case <-time.After(200 * time.Millisecond):
			}
		}
		state := journalState(t, a)
		metadataCount := 0
		if promoteMetadata {
			metadataCount = 1
		}
		versionedCount := 1
		if step < 0 {
			versionedCount = 0
		}
		if state.Operation != nil || len(state.CredentialApplications) != versionedCount || len(state.ActionApplications) != metadataCount || trace.writes != iteration+1 || len(tasks) != iteration+1 {
			t.Fatal("duplicate write or missing credential receipt")
		}
		if step < 0 {
			origin = state.ActionApplications[0].DeepCopy()
			id, err := metadataCredentialReceiptTarget(origin, time.Now())
			if err != nil || recovered[origin.Operation.ID] != tasks[iteration] {
				t.Fatal("metadata origin task missing", err)
			}
			trace.target = id
		} else {
			receipt := state.CredentialApplications[0]
			if receipt.OperationID == "" || recovered[receipt.OperationID] != tasks[iteration] || len(receipt.Versions) != max(0, 1+min(step, 1)) {
				t.Fatal("original task/version receipt mismatch")
			}
			if iteration == 0 {
				trace.target = receipt.TargetID
			} else if trace.target != receipt.TargetID {
				t.Fatal("mode change replaced owned UUID")
			}
			if promoteMetadata && !reflect.DeepEqual(*origin, state.ActionApplications[0]) {
				t.Fatal("metadata origin changed during promotion")
			}
		}
		actual, err := c.globalCredentials(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := actual["snmpv3_credentials"][name]
		if got.ID != trace.target || got.Mode != mode {
			t.Fatal("native SNMP mode/UUID not verified")
		}
		for range 2 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
		}
		if trace.writes != iteration+1 {
			t.Fatal("restart replayed credential mutation")
		}
		public, _ := json.Marshal(state)
		for _, secret := range []string{authOne, authTwo, privacy} {
			if strings.Contains(string(public), secret) {
				t.Fatal("credential material entered journal")
			}
		}
		t.Log("original task and version receipt verified", step+1, mode)
	}
	actual, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := actual["snmpv3_credentials"][name]
	expectedPrivacy, ok := trace.expected["privacyType"].(string)
	if !ok || got.ID != trace.target || got.Username != "cvk-unused-snmp" || got.Mode != "AUTHPRIV" || got.AuthType != "SHA" || got.PrivacyType != expectedPrivacy {
		t.Fatal("exact unused cleanup identity changed")
	}
	trace.allowDelete = true
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, globalCredentialsPath+"/"+trace.target, nil)
	if err != nil {
		t.Fatal("cleanup uncertain; retain checkpoint", err)
	}
	var ack struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
		t.Fatal("cleanup acknowledgement missing")
	}
	if err := writeJSON("cleanup.json", map[string]any{"taskID": ack.Response.ID, "target": trace.target, "submittedAt": submitted}); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := c.configurationTaskTreeComplete(ctx, ack.Response.ID, submitted, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("cleanup task pending; retain checkpoint")
		case <-time.After(time.Second):
		}
	}
	after, err := c.globalCredentials(ctx)
	if err != nil || !reflect.DeepEqual(after, baseline) {
		t.Fatal("credential metadata baseline not restored", err)
	}
	if err := writeJSON("result.json", map[string]any{"tasks": tasks, "cleanupTask": ack.Response.ID, "writes": trace.writes, "deletes": trace.deletes, "sameUUID": true, "baselineRestored": true, "installed": false, "knownTaskRecoveries": len(recovered), "credentialAuthenticatedOnDevice": false, "metadataPromotion": promoteMetadata, "immutableOriginRetained": promoteMetadata, "directPrivacyPromotion": directPrivacy}); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("unused SNMP known-task cycle passed: %d writes, NOAUTH (when selected) / AUTHNOPRIV to AUTHPRIV, same UUID, version expansion/rotation, original-task recovery, no replay and supervised exact cleanup", trace.writes))
}
