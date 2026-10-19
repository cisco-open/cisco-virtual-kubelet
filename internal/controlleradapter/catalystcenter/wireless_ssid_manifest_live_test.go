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
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"
)

type liveSSIDManifestTrace struct {
	base                           http.RoundTripper
	path, name, id, password       string
	attempts, posts, puts, deletes int
	allowDelete                    bool
	multiPasswords                 map[int]string
	emptyMultiPSK                  bool
	maxPuts                        int
}

func (trace *liveSSIDManifestTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		trace.attempts++
		if req.Method == http.MethodDelete && trace.allowDelete && trace.id != "" && req.URL.Path == trace.path+"/"+url.PathEscape(trace.id) && trace.deletes == 0 {
			trace.deletes++
		} else {
			create := req.Method == http.MethodPost && req.URL.Path == trace.path && trace.posts == 0
			update := req.Method == http.MethodPut && trace.id != "" && req.URL.Path == trace.path+"/"+url.PathEscape(trace.id) && trace.puts < trace.maxPuts
			if !create && !update {
				return nil, errors.New("SSID qualification refused unrelated or repeated mutation")
			}
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var payload map[string]any
			if json.Unmarshal(raw, &payload) != nil || payload["ssid"] != trace.name || payload["isEnabled"] != false || payload["authType"] != "WPA2_PERSONAL" || payload["passphrase"] != trace.password {
				return nil, errors.New("SSID qualification refused unexpected target or credential payload")
			}
			if trace.emptyMultiPSK {
				entries, ok := payload["multiPSKSettings"].([]any)
				if !ok || len(entries) != 0 {
					return nil, errors.New("SSID qualification requires explicit empty Multi-PSK array")
				}
			}
			if len(trace.multiPasswords) > 0 {
				entries, ok := payload["multiPSKSettings"].([]any)
				if !ok || len(entries) != len(trace.multiPasswords) {
					return nil, errors.New("SSID qualification refused incomplete multi-PSK payload")
				}
				seen := map[int]bool{}
				for _, item := range entries {
					entry, ok := item.(map[string]any)
					if !ok {
						return nil, errors.New("invalid multi-PSK payload")
					}
					number, ok := entry["priority"].(float64)
					priority := int(number)
					password, exists := trace.multiPasswords[priority]
					if !ok || float64(priority) != number || !exists || seen[priority] || entry["passphrase"] != password || entry["passphraseType"] != "ASCII" {
						return nil, errors.New("SSID qualification refused mismatched multi-PSK identity")
					}
					seen[priority] = true
				}
			}
			if create {
				trace.posts++
			} else {
				trace.puts++
			}
		}
	}
	return trace.base.RoundTrip(req)
}

// Public manifest/journal with real appliance tasks and projection files.
// Kubernetes persistence is simulated. No profile/site assignment or device
// provisioning is declared, and the newly created SSID stays disabled.
func TestLiveWirelessSSIDManifestCredentials(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_CREDENTIALS") != "isolated-disabled-definition" {
		t.Skip("isolated SSID credential manifest qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "main")
}

func TestLiveWirelessSSIDManifestMultiPSK(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_MULTIPSK") != "isolated-disabled-definition" {
		t.Skip("isolated multi-PSK manifest qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "multi")
}

func TestLiveWirelessSSIDManifestEmptyMultiPSK(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_EMPTY_MULTIPSK") != "isolated-disabled-definition" {
		t.Skip("isolated explicit-empty Multi-PSK manifest qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "empty-multi")
}

func TestLiveWirelessSSIDManifestRetirement(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_RETIREMENT") != "isolated-disabled-definition" {
		t.Skip("isolated SSID key retirement qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "retirement")
}

func TestLiveWirelessSSIDManifestRetirementRecovery(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_RETIREMENT_RECOVERY") != "isolated-disabled-definition" {
		t.Skip("isolated SSID retirement recovery qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "retirement-recovery")
}

func TestLiveWirelessSSIDManifestRetainedRetirement(t *testing.T) {
	if os.Getenv("CATC_SSID_MANIFEST_RETIREMENT_RETAINED") != "isolated-disabled-definition" {
		t.Skip("isolated retained SSID retirement qualification not enabled")
	}
	qualifyLiveSSIDManifestCredentials(t, "retirement-retained")
}

func qualifyLiveSSIDManifestCredentials(t *testing.T, mode string) {
	recoverRetirement := mode == "retirement-recovery"
	retainRetirement := mode == "retirement-retained"
	retire := mode == "retirement" || recoverRetirement || retainRetirement
	multi := mode == "multi" || retire
	emptyMulti := mode == "empty-multi"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	site, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := wirelessQualificationName(t, "CVK_PSK_")
	if _, exists := baseline[name]; exists {
		t.Fatal("SSID qualification name exists")
	}
	recovery, err := os.MkdirTemp("/private/tmp", "cvk-ssid-manifest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("isolated disabled SSID and private recovery directory", name, recovery)
	publicSSIDs := map[string]any{}
	for name, row := range baseline {
		publicSSIDs[name] = map[string]any{"id": row.id, "fields": row.fields}
	}
	publicProfiles := map[string]any{}
	for name, row := range profiles {
		publicProfiles[name] = map[string]any{"id": row.id, "fields": row.fields}
	}
	publicBaseline, _ := json.Marshal(map[string]any{"ssids": publicSSIDs, "profiles": publicProfiles})
	if err := os.WriteFile(filepath.Join(recovery, "baseline.json"), publicBaseline, 0600); err != nil {
		t.Fatal(err)
	}
	a, config := journalFixture(t)
	examplePath := "../../../examples/configs/catalyst-center/wireless-ssid-credentials-apply.yaml"
	if multi {
		examplePath = "../../../examples/configs/catalyst-center/wireless-ssid-multipsk-credentials-apply.yaml"
	}
	if emptyMulti {
		examplePath = "../../../examples/configs/catalyst-center/wireless-ssid-empty-multipsk-apply.yaml"
	}
	exampleRaw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	var example configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(exampleRaw, &example); err != nil {
		t.Fatal(err)
	}
	config.Spec = example.Spec
	expectedRefs := 1
	if multi {
		expectedRefs = 3
	}
	if len(config.Spec.SecretRefs) != expectedRefs || config.Spec.SecretRefs[0].Path != "/ssids/0/passphrase" {
		t.Fatal("credential example source binding changed")
	}
	alias := config.Spec.SecretRefs[0].Source
	var exampleModel map[string]any
	if json.Unmarshal(config.Spec.Source.Inline.Raw, &exampleModel) != nil {
		t.Fatal("credential example model unavailable")
	}
	modelRoot, _ := exampleModel["catalyst_center"].(map[string]any)
	wirelessRoot, _ := modelRoot["wireless"].(map[string]any)
	ssids, _ := wirelessRoot["ssids"].([]any)
	if len(ssids) != 1 {
		t.Fatal("credential example must isolate one SSID")
	}
	row, ok := ssids[0].(map[string]any)
	if !ok {
		t.Fatal("credential example SSID invalid")
	}
	row["name"] = name
	a.client = c
	a.intentSecretPath = t.TempDir()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: alias, Name: "ssid-qualification", Key: "passphrase"}}
	if multi {
		for _, priority := range []int{0, 4} {
			key := fmt.Sprintf("multi-%d", priority)
			nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: key, Name: "ssid-qualification", Key: key})
		}
	}

	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ControllerRef.Name = nc.Name
	mainRef := config.Spec.SecretRefs[0]
	order := []int{4, 0}
	initialRefs := append([]configapi.NetworkControllerSecretRef(nil), config.Spec.SecretRefs...)
	initialRow, _ := json.Marshal(row)
	firstModel := true
	setModel := func(version int) {
		t.Helper()
		row["passphrase_version"] = version
		if multi {
			row["passphrase_version"] = 1
			entries := []any{}
			config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{mainRef}
			for index, priority := range order {
				counter := 1
				if priority == 4 {
					counter = version
				}
				entries = append(entries, map[string]any{"priority": priority, "passphrase_type": "ASCII", "passphrase_wo_version": counter})
				config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "wireless", Path: fmt.Sprintf("/ssids/0/multi_psk_settings/%d/passphrase_wo", index), Source: fmt.Sprintf("multi-%d", priority)})
			}
			row["multi_psk_settings"] = entries
		}
		if firstModel {
			actualRow, _ := json.Marshal(row)
			if !reflect.DeepEqual(initialRefs, config.Spec.SecretRefs) || !bytes.Equal(initialRow, actualRow) {
				t.Fatal("native fixture changed the repository example model or source bindings")
			}
			firstModel = false
		}
		config.Spec.Source.Inline.Raw = ssidTestModel(row)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: "/ssids/0/passphrase", SourceAlias: alias, SecretName: "ssid-qualification", SecretKey: "passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	projection := filepath.Join(a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(projection), 0700); err != nil {
		t.Fatal(err)
	}
	values := []string{}
	newPassword := func() string {
		t.Helper()
		var raw [24]byte
		if _, err := rand.Read(raw[:]); err != nil {
			t.Fatal(err)
		}
		password := hex.EncodeToString(raw[:])
		values = append(values, password)
		return password
	}
	project := func(version int) string {
		t.Helper()
		password := newPassword()
		body, _ := json.Marshal(map[string]any{"version": version, "value": password})
		if err := os.WriteFile(projection, body, 0600); err != nil {
			t.Fatal(err)
		}
		return password
	}
	multiValues := map[int]string{}
	publishMulti := func(version int) {
		t.Helper()
		for index, priority := range order {
			key := fmt.Sprintf("multi-%d", priority)
			pointer := fmt.Sprintf("/ssids/0/multi_psk_settings/%d/passphrase_wo", index)
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: pointer, SourceAlias: key, SecretName: "ssid-qualification", SecretKey: key})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.intentSecretPath, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			counter := 1
			if priority == 4 {
				counter = version
			}
			raw, _ := json.Marshal(map[string]any{"version": counter, "value": multiValues[priority]})
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveSSIDManifestTrace{base: base, path: wirelessSSIDPath(site), name: name, emptyMultiPSK: emptyMulti, maxPuts: 1}
	if retire {
		trace.maxPuts = 4
	}
	rest.HTTPClient.Transport = trace
	qualificationIdentity := func() ssidQualificationIdentity {
		return ssidQualificationIdentity{Name: name, SiteID: site, ID: trace.id, Posts: trace.posts, Puts: trace.puts, Deletes: trace.deletes, MultiPSK: multi, Retirement: retire}
	}
	assertBaseline := func() {
		t.Helper()
		currentProfiles, err := c.wirelessProfileInventory(ctx)
		if err != nil || !reflect.DeepEqual(profiles, currentProfiles) {
			t.Fatal("profile baseline changed; retain SSID", err)
		}
		for _, profile := range currentProfiles {
			for _, collection := range []string{"ssidDetails", "apZones", "featureTemplates"} {
				entries, _ := profile.fields[collection].([]map[string]any)
				for _, entry := range entries {
					if entry["ssidName"] == name {
						t.Fatal("profile adopted SSID; retain")
					}
					if names, ok := entry["ssids"].([]string); ok {
						for _, selected := range names {
							if selected == name {
								t.Fatal("profile adopted SSID; retain")
							}
						}
					}
				}
			}
		}
		current, err := c.wirelessSSIDInventory(ctx, site)
		if err != nil {
			t.Fatal(err)
		}
		public := map[string]any{}
		for name, row := range current {
			public[name] = row.fields
		}
		encoded, _ := json.Marshal(public)
		for _, value := range values {
			if bytes.Contains(encoded, []byte(value)) {
				t.Fatal("credential entered projected public metadata")
			}
		}
		if observed, exists := current[name]; exists {
			if trace.deletes != 0 {
				t.Fatal("deleted SSID remains visible; cleanup not verified")
			}
			if trace.posts != 1 || trace.id != "" && observed.id != trace.id || observed.fields["isEnabled"] != false || observed.fields["authType"] != "WPA2_PERSONAL" {
				t.Fatal("SSID fixture identity or disabled state changed; retain")
			}
			delete(current, name)
		} else if trace.id != "" && trace.deletes == 0 {
			op := journalState(t, a).Operation
			pending := op != nil && validateSSIDQualificationRecovery(qualificationIdentity(), journalState(t, a), config.Spec.Source.Inline.Raw) == nil
			if !pending {
				t.Fatal("owned SSID disappeared without correlated pending update; retain")
			}
		}
		if !reflect.DeepEqual(baseline, current) {
			t.Fatal("unrelated SSID baseline changed; retain")
		}
	}
	sawAccepted := false
	injectedStages := map[int]bool{}
	persist := func() {
		t.Helper()
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(current.Status.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if bytes.Contains(raw, []byte(value)) {
				t.Fatal("credential entered durable journal")
			}
		}
		if err := os.WriteFile(filepath.Join(recovery, "journal.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recovery, "source.json"), config.Spec.Source.Inline.Raw, 0600); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(map[string]any{"name": name, "siteId": site, "id": trace.id, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes, "multiPSK": multi, "retirement": retire})
		if err := os.WriteFile(filepath.Join(recovery, "identity.json"), metadata, 0600); err != nil {
			t.Fatal(err)
		}
		copy := current.DeepCopy()
		if err := json.Unmarshal(raw, &copy.Status.Configuration); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		assertBaseline()
		rebuilt := *a
		if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			persist()
			t.Fatal("public SSID reconciliation refused; retain fixture", err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		current := journalState(t, a)
		if current.Operation != nil && current.Operation.Phase == "Accepted" {
			sawAccepted = true
		}
		if recoverRetirement && current.Operation != nil && current.Operation.Phase == "Accepted" && current.Operation.Action == "Update" && !injectedStages[trace.puts] {
			identity := qualificationIdentity()
			if err := validateSSIDQualificationRecovery(identity, current, config.Spec.Source.Inline.Raw); err != nil {
				t.Fatal("accepted stage recovery proof refused", trace.puts, err)
			}
			checkpoint := struct {
				Identity ssidQualificationIdentity                 `json:"identity"`
				Journal  *cvk.NetworkControllerConfigurationStatus `json:"journal"`
				Source   json.RawMessage                           `json:"source"`
			}{identity, current, config.Spec.Source.Inline.Raw}
			raw, _ := json.Marshal(checkpoint)
			if err := os.WriteFile(filepath.Join(recovery, fmt.Sprintf("accepted-stage-%d.json", trace.puts)), raw, 0600); err != nil {
				t.Fatal(err)
			}
			controller, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			controller.Status.Configuration.Operation.Phase = "Unknown"
			if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
				t.Fatal(err)
			}
			injectedStages[trace.puts] = true
			t.Log("accepted native update retained its task through simulated uncertain-phase restart", trace.puts)
		}

		persist()
	}
	settle := func() {
		t.Helper()
		for range 60 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("SSID qualification timed out; retain", ctx.Err())
			case <-time.After(time.Second):
			}
		}
		t.Fatal("SSID manifest did not converge; inspect private journal", config.Status.Phase)
	}
	assertReceipt := func(version int64) {
		t.Helper()
		current := journalState(t, a)
		if current.Operation != nil || len(current.CredentialApplications) != 1 {
			t.Fatal("credential application receipt missing")
		}
		receipt := current.CredentialApplications[0]
		if !validAPIID(receipt.TargetID) {
			t.Fatal("credential target missing")
		}
		expected := map[string]int64{"passphrase": version}
		if multi {
			expected = map[string]int64{"passphrase": 1}
			for _, priority := range order {
				counter := int64(1)
				if priority == 4 {
					counter = version
				}
				expected[fmt.Sprintf("multi-psk:%d", priority)] = counter
			}
		}
		if len(receipt.Versions) != len(expected) {
			t.Fatal("credential receipt set incomplete")
		}
		for _, observed := range receipt.Versions {
			want, exists := expected[observed.Key]
			if !exists || observed.Version != want {
				t.Fatal("credential semantic version missing")
			}
			if observed.Key == "passphrase" && observed.Path != "/ssids/0/passphrase" {
				t.Fatal("main credential source path changed")
			}
			delete(expected, observed.Key)
		}
		if len(expected) != 0 {
			t.Fatal("credential receipt set missing identity")
		}

		if trace.id != "" && receipt.TargetID != trace.id {
			t.Fatal("rotation changed SSID identity")
		}
		trace.id = receipt.TargetID
	}
	setModel(1)
	trace.password = project(1)
	if multi {
		for _, priority := range order {
			multiValues[priority] = newPassword()
		}
		trace.multiPasswords = multiValues
		publishMulti(1)
	}
	persist()
	settle()
	assertReceipt(1)
	persist()
	tick()
	if !sawAccepted || trace.posts != 1 || trace.puts != 0 {
		t.Fatal("create/restart/no-replay qualification failed")
	}
	t.Log("disabled credential SSID created and accepted-task restart verified", name, trace.id)
	if multi {
		order = []int{0, 4}
		setModel(1)
		publishMulti(1)
		attempts := trace.attempts
		settle()
		tick()
		if trace.attempts != attempts {
			t.Fatal("multi-PSK reorder replayed native mutation")
		}
		t.Log("multi-PSK priority reorder converged without native mutation")
	}
	setModel(2)
	attempts := trace.attempts
	tick()
	if trace.attempts != attempts || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("stale projection was submitted or accepted")
	}
	if multi {
		multiValues[4] = newPassword()
		publishMulti(2)
	} else {
		trace.password = project(2)
	}
	settle()
	assertReceipt(2)
	persist()
	tick()
	if trace.posts != 1 || trace.puts != 1 {
		t.Fatal("rotation/no-replay qualification failed")
	}
	t.Log("same-UUID credential rotation and stale-projection refusal verified", trace.id)
	if retire {
		// Remove priority 4's currently projected source before publishing intent.
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: "/ssids/0/multi_psk_settings/1/passphrase_wo", SourceAlias: "multi-4", SecretName: "ssid-qualification", SecretKey: "multi-4"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(a.intentSecretPath, relative)); err != nil {
			t.Fatal(err)
		}
		order = []int{0}
		setModel(2)
		publishMulti(2)
		trace.multiPasswords = map[int]string{0: multiValues[0]}
		if retainRetirement {
			for range 10 {
				tick()
				state := journalState(t, a)
				if state.Operation != nil && state.Operation.Phase == "Accepted" && trace.puts == 2 {
					if err := validateSSIDQualificationRecovery(qualificationIdentity(), state, config.Spec.Source.Inline.Raw); err != nil {
						t.Fatal("retained retirement proof refused", err)
					}
					t.Log("single-key retirement retained for exact task-bound cleanup qualification", trace.id, recovery)
					return
				}
			}
			t.Fatal("retirement was not retained at acknowledged boundary")
		}
		settle()
		assertReceipt(2)
		persist()
		if trace.puts != 2 {
			t.Fatal("single-key retirement did not complete once")
		}
		retained := journalState(t, a).CredentialApplications[0]
		if len(retained.SSIDCredentialHistory) != 2 || retained.SSIDCredentialHistory[1].Version != 2 {
			t.Fatal("retired counter not retained")
		}
		order = []int{0, 4}
		setModel(2)
		publishMulti(2)
		attempts := trace.attempts
		for range 2 {
			tick()
		}
		if trace.attempts != attempts || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("retired version reintroduced")
		}
		setModel(3)
		multiValues[4] = newPassword()
		publishMulti(3)
		trace.multiPasswords = multiValues
		settle()
		assertReceipt(3)
		persist()
		if trace.puts != 3 {
			t.Fatal("advanced key reintroduction did not complete once")
		}
		order = []int{}
		setModel(3)
		trace.multiPasswords = nil
		trace.emptyMultiPSK = true
		settle()
		assertReceipt(3)
		persist()
		tick()
		final := journalState(t, a).CredentialApplications[0]
		if trace.puts != 4 || len(final.SSIDCredentialHistory) != 2 || final.SSIDCredentialHistory[1].Version != 3 {
			t.Fatal("empty-set retirement lost version history")
		}
		if recoverRetirement && len(injectedStages) != 4 {
			t.Fatal("not every retirement stage recovered", len(injectedStages))
		}
		t.Log("single-key retirement, old-counter refusal, advanced reintroduction and explicit-empty retirement verified", trace.id)
	}

	for range 2 {
		assertBaseline()
	}
	trace.allowDelete = true
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, trace.path+"/"+url.PathEscape(trace.id), nil)
	if err != nil {
		t.Fatal("exact fixture deletion uncertain; retain", trace.id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("delete receipt invalid; retain", trace.id)
	}
	cleanupEvidence := map[string]any{"name": name, "id": trace.id, "deleteTaskId": receipt.Response.ID, "deleted": false}
	cleanupRaw, _ := json.Marshal(cleanupEvidence)
	if err := os.WriteFile(filepath.Join(recovery, "cleanup.json"), cleanupRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal("delete task unresolved", err)
	}
	waitSSIDFixtureAbsent(t, c, ctx, site, name, trace.id)
	cleanupEvidence["deleted"] = true
	cleanupRaw, _ = json.Marshal(cleanupEvidence)
	if err := os.WriteFile(filepath.Join(recovery, "cleanup.json"), cleanupRaw, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		assertBaseline()
	}
	persist()
	if trace.deletes != 1 {
		t.Fatal("exact cleanup did not execute once")
	}
	t.Log("repository credential example/public manifest/native task/JSON restart/version receipt/rotation/exact cleanup verified; no profile association or device action")
}

// Task completion is not sufficient proof that the definition disappeared.
// Poll only reads; never retry DELETE or adopt a changed identity.
func waitSSIDFixtureAbsent(t *testing.T, c *client, ctx context.Context, site, name, id string) {
	t.Helper()
	for range 30 {
		rows, err := c.wirelessSSIDInventory(ctx, site)
		if err != nil {
			t.Fatal(err)
		}
		row, exists := rows[name]
		if !exists {
			return
		}
		if row.id != id || row.fields["isEnabled"] != false {
			t.Fatal("SSID cleanup identity changed; retain")
		}
		select {
		case <-ctx.Done():
			t.Fatal("SSID cleanup absence unresolved", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("SSID deletion did not converge to absence; no retry")
}
