// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryCredentialPublicEffect(t *testing.T) {
	public := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "userNameList": []string{"operator", "admin"}}
	got := discoveryMetadata{ID: "job-id", Name: "fixture", Type: "RANGE", Addresses: "192.0.2.1-192.0.2.1", Protocol: "ssh", Usernames: []string{"admin", "operator"}}
	effect, err := checkedDiscoveryCredentialEffect(public, got)
	if err != nil {
		t.Fatal("canonical native effect", err)
	}
	key := discoveryCredentialEffectReceiptKey(discoveryPublicDigest("fixture"), got.ID, effect)
	identity, err := parseDiscoveryCredentialReceipt(key)
	if err != nil || identity.legacy || identity.id != got.ID || identity.effect != effect {
		t.Fatal("native effect key roundtrip", err)
	}
	for _, field := range discoveryCredentialFields {
		t.Run(field.model, func(t *testing.T) {
			candidate := map[string]any{}
			for k, v := range public {
				candidate[k] = v
			}
			candidate[field.api] = discoveryCredentialMarker
			if value, err := discoveryCredentialEffect(candidate); err == nil || value.digest != "" {
				t.Fatal("credential-bearing payload was fingerprinted")
			}
		})
	}
	for _, field := range []string{"name", "ipAddressList", "snmpVersion"} {
		t.Run("nested-"+field, func(t *testing.T) {
			candidate := map[string]any{}
			for k, v := range public {
				candidate[k] = v
			}
			candidate[field] = map[string]any{"password": discoveryCredentialMarker}
			if value, err := discoveryCredentialEffect(candidate); err == nil || value.digest != "" {
				t.Fatal("nested secret object entered public fingerprint")
			}
		})
	}
	for _, bad := range []string{strings.Replace(key, ":v2:", ":v9:", 1), strings.Replace(key, ":100f:", ":0100f:", 1), strings.Replace(key, ":100f:", ":100000:", 1), key + ":extra"} {
		if _, err := parseDiscoveryCredentialReceipt(bad); err == nil {
			t.Fatal("invalid effect receipt accepted")
		}
	}
	got.Usernames = []string{"different"}
	if _, err := checkedDiscoveryCredentialEffect(public, got); err == nil {
		t.Fatal("changed username scope accepted")
	}
	t.Run("empty-native-filter", func(t *testing.T) {
		got.Usernames = []string{"operator", "admin"}
		public["ipFilterList"] = []string{}
		var baseline discoveryCredentialPublicEffect
		for i, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`""`), json.RawMessage(`[]`)} {
			got.Filters = raw
			effect, err := checkedDiscoveryCredentialEffect(public, got)
			if err != nil {
				t.Fatal("native empty filter equivalence lost", err)
			}
			if i == 0 {
				baseline = effect
			} else if effect != baseline {
				t.Fatal("equivalent native empty filter changed effect")
			}
		}
	})
}

type effectPromotionClient struct {
	ctrlclient.Client
	mode, legacy string
	attempts     *int
}
type effectPromotionWriter struct {
	ctrlclient.SubResourceWriter
	client effectPromotionClient
}

func (c effectPromotionClient) Status() ctrlclient.SubResourceWriter {
	return effectPromotionWriter{c.Client.Status(), c}
}
func (w effectPromotionWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.ActionApplications) != 1 || !strings.HasPrefix(nc.Status.Configuration.ActionApplications[0].Resource, discoveryCredentialEffectReceiptPrefix) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.client.attempts++
	if w.client.mode == "conflict" {
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("injected conflict"))
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.client.mode == "lost-ack" {
		return errors.New("injected lost effect acknowledgement")
	}
	if w.client.mode == "pruned" {
		var stored cvk.NetworkController
		if err := w.client.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &stored); err != nil {
			return err
		}
		stored.Status.Configuration.ActionApplications[0].Resource = w.client.legacy
		return w.client.Client.Status().Update(ctx, &stored)
	}
	return nil
}

func TestDiscoveryCredentialLegacyEffectPromotion(t *testing.T) {
	for _, mode := range []string{"complete", "lost-ack", "conflict", "pruned", "native-drift", "foreign-uuid", "active", "missing-versions", "unknown-version", "changed-mask", "changed-digest", "forged-public", "busy", "capacity"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialEffectPromotionScenario(t, mode, nil) })
	}
}

func discoveryCredentialEffectPromotionScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, source, p, op := discoveryCredentialCompletionFixture(t, install)
	// Simulate an already acknowledged v1 input from an older worker.
	legacyNC, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyBase, _, ok := discoveryCredentialInputParts(op.Resource)
	if !ok {
		t.Fatal("bad fixture input")
	}
	legacyNC.Status.Configuration.Operation.Resource = legacyBase
	op.Resource = legacyBase
	if err := a.statusWriter.Status().Update(ctx, legacyNC); err != nil {
		t.Fatal(err)
	}
	legacyPhase := false
	posts, taskReplays := 0, 0
	got := discoveryMetadata{ID: "job-id", Name: "fixture", Type: "RANGE", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" {
			posts++
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case taskPath + "original-task":
			if legacyPhase {
				taskReplays++
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": op.SubmittedAt.UnixMilli(), "endTime": op.SubmittedAt.UnixMilli(), "isError": false, "data": `{"discoveryId":"job-id"}`}})
		case discoveryPath + "/1/100":
			json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{got}})
		default:
			t.Error("unexpected API read", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	if err := a.recordDiscoveryCredentialCreate(ctx, c, source, p, 0, op); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := parseDiscoveryCredentialReceipt(nc.Status.Configuration.ActionApplications[0].Resource)
	if err != nil || identity.legacy || identity.effect.mask == 0 {
		t.Fatal("new creation did not retain native public effect", err)
	}
	oldKey := discoveryCredentialReceiptPrefix + identity.name + ":" + identity.id
	nc.Status.Configuration.ActionApplications[0].Resource = oldKey
	if mode == "missing-versions" {
		nc.Status.Configuration.ActionApplications[0].Operation.CredentialVersions = nil
		if install != nil {
			invalid := nc.DeepCopy()
			invalid.Status.Configuration.CredentialApplications[0].Versions = nil
			if err := a.statusWriter.Status().Update(ctx, invalid); !apierrors.IsInvalid(err) {
				t.Fatal("API accepted an application with missing required versions", err)
			}
		}
		// A downlevel schema may drop the entire optional receipt. Do not
		// weaken the real API's required versions rule to construct the fault.
		nc.Status.Configuration.CredentialApplications = nil
	}
	if mode == "unknown-version" || mode == "changed-mask" || mode == "changed-digest" {
		key := discoveryCredentialEffectReceiptKey(identity.name, identity.id, identity.effect)
		switch mode {
		case "unknown-version":
			key = strings.Replace(key, ":v2:", ":v9:", 1)
		case "changed-mask":
			key = strings.Replace(key, ":100f:", ":f:", 1)
		case "changed-digest":
			key = strings.Replace(key, identity.effect.digest, strings.Repeat("a", 64), 1)
		}
		nc.Status.Configuration.ActionApplications[0].Resource = key
	}
	if mode == "busy" {
		nc.Status.Configuration.Operation = op.DeepCopy()
		nc.Status.Configuration.Operation.ID = strings.Repeat("b", 32)
		nc.Status.Configuration.Operation.Phase = "Unknown"
		nc.Status.Configuration.Operation.TaskID = ""
	}
	if mode == "capacity" {
		fillDiscoveryEffectAuditBoundary(t, nc.Status.Configuration, (256<<10)-30)
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	legacyPhase = true
	switch mode {
	case "native-drift":
		got.Usernames = []string{"different"}
	case "foreign-uuid":
		got.ID = "foreign"
	case "active":
		got.Condition = "Running"
	}
	base := a.statusWriter
	attempts := 0
	a.statusWriter = effectPromotionClient{Client: base, mode: mode, legacy: oldKey, attempts: &attempts}
	defer func() { a.statusWriter = base }()
	if mode == "forged-public" {
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
		if err != nil {
			t.Fatal(err)
		}
		request, input, err := projection.bindRequest(nc, c, source, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.public["ipAddressList"] = "192.0.2.2"
		got.Addresses = "192.0.2.2"
		if err := a.promoteDiscoveryCredentialEffect(ctx, c, source, nc, before.ActionApplications[0], request, input, got); err == nil {
			t.Fatal("scope outside original input was promoted")
		}
	} else {
		for i := 0; i < 3; i++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
		}
	}
	after := journalState(t, a)
	if posts != 0 || taskReplays != 0 {
		t.Fatal("promotion mutated native state or replayed an expired task", posts, taskReplays)
	}
	success := mode == "complete" || mode == "lost-ack"
	if success {
		if attempts != 1 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || after.Operation != nil || !reflect.DeepEqual(before.CredentialApplications, after.CredentialApplications) {
			t.Fatal("status-only promotion failed", attempts, c.Status.Phase)
		}
		receipt := after.ActionApplications[0]
		if !reflect.DeepEqual(before.ActionApplications[0].Operation, receipt.Operation) || !reflect.DeepEqual(before.ActionApplications[0].VerifiedAt, receipt.VerifiedAt) {
			t.Fatal("promotion relabeled delivery operation or verification time")
		}
		parsed, err := parseDiscoveryCredentialReceipt(receipt.Resource)
		if err != nil || parsed.legacy || parsed.effect != identity.effect || !discoveryCredentialReceiptPairPresent(after, &receipt.Operation) {
			t.Fatal("promoted proof invalid", err)
		}
	} else if mode == "capacity" {
		if !reflect.DeepEqual(before, after) || attempts != 0 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("optional promotion exceeded capacity or regressed legacy read-only no-op")
		}
	} else if !reflect.DeepEqual(before, after) || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("unqualified promotion changed evidence or reported convergence", mode, c.Status.Phase)
	}
}

// Build a schema-valid historical audit just below the existing soft byte cap.
// The v2 key's added public fingerprint must not push it over that cap.
func fillDiscoveryEffectAuditBoundary(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, target int) {
	t.Helper()
	for i := 0; i < 255; i++ {
		raw, _ := json.Marshal(state.ActionApplications)
		remaining := target - len(raw)
		if remaining == 0 {
			return
		}
		entry := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: fmt.Sprintf("history-%03d", i), Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), ConfigUID: "historical", ConfigName: "historical", ConfigGeneration: 1, ControllerGeneration: 1, Section: "sites", Resource: "x", Action: "Create", Phase: "Verified"}}
		probe := append(append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...), entry)
		encoded, _ := json.Marshal(probe)
		minimum := len(encoded) - len(raw)
		maximum := minimum + (512 - 1) + (512 - len(entry.Resource))
		if remaining < minimum {
			t.Fatal("cannot represent audit boundary")
		}
		amount := maximum
		if remaining <= maximum {
			amount = remaining
		} else if remaining < maximum+minimum {
			amount = remaining - minimum
		}
		padding := amount - minimum
		opPadding := padding
		if opPadding > 511 {
			opPadding = 511
		}
		entry.Operation.Resource += strings.Repeat("x", opPadding)
		padding -= opPadding
		entry.Resource += strings.Repeat("x", padding)
		state.ActionApplications = append(state.ActionApplications, entry)
	}
	t.Fatal("audit boundary exceeds entry cap")
}
