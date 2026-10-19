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
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type discoveryCredentialCompletionClient struct {
	ctrlclient.Client
	mode     string
	attempts *int
}
type discoveryCredentialCompletionWriter struct {
	ctrlclient.SubResourceWriter
	client discoveryCredentialCompletionClient
}

func (c discoveryCredentialCompletionClient) Status() ctrlclient.SubResourceWriter {
	return discoveryCredentialCompletionWriter{c.Client.Status(), c}
}
func (w discoveryCredentialCompletionWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verified" {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.client.attempts++
	if !discoveryCredentialReceiptPairPresent(nc.Status.Configuration, nc.Status.Configuration.Operation) {
		return errors.New("attempted non-atomic discovery credential publication")
	}
	if w.client.mode == "conflict" {
		return apierrors.NewConflict(schema.GroupResource{Group: "config.cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("injected conflict"))
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.client.mode == "lost-ack" {
		return errors.New("injected lost publication acknowledgement")
	}
	if strings.HasPrefix(w.client.mode, "pruned-") || w.client.mode == "wrong-target" {
		var stored cvk.NetworkController
		if err := w.client.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &stored); err != nil {
			return err
		}
		switch w.client.mode {
		case "pruned-credentials":
			stored.Status.Configuration.CredentialApplications = nil
		case "pruned-action":
			stored.Status.Configuration.ActionApplications = nil
		case "pruned-operation-versions":
			stored.Status.Configuration.Operation.CredentialVersions = nil
		case "pruned-action-versions":
			stored.Status.Configuration.ActionApplications[0].Operation.CredentialVersions = nil
		case "wrong-target":
			stored.Status.Configuration.CredentialApplications[0].TargetID = "different-job"
		}
		return w.client.Client.Status().Update(ctx, &stored)
	}
	return nil
}

func discoveryCredentialCompletionFixture(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *discoveryCredentialProjection, *cvk.NetworkControllerConfigurationOperation) {
	t.Helper()
	ctx := context.Background()
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, nc, config, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if install != nil {
		install(a, config)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		var err error
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := a.prepareDiscoveryCredentialProjection(nc, config, source)
	if err != nil {
		t.Fatal(err)
	}
	request, input, err := p.bindRequest(nc, config, source, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := configurationResourceIdentity{credentials: request.versions}
	op, err := a.prepareConfiguration(ctx, config, source, "inventory", input, "Create", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, source, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "original-task"); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, p, nc.Status.Configuration.Operation.DeepCopy()
}

func TestDiscoveryCredentialAtomicCompletion(t *testing.T) {
	for _, mode := range []string{"complete", "lost-ack", "conflict", "pruned-credentials", "pruned-action", "pruned-operation-versions", "pruned-action-versions", "wrong-target", "failed-task", "pending-task", "wrong-job"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialAtomicCompletionScenario(t, mode, nil) })
	}
}

func discoveryCredentialAtomicCompletionScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, source, p, op := discoveryCredentialCompletionFixture(t, install)
	if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("generic verification bypassed receipt pair")
	}
	if err := a.recordConfigurationCredentialApplication(ctx, c, source, op.ID, "job-id"); err == nil {
		t.Fatal("generic credential receipt bypassed creator evidence")
	}
	reads, writes := 0, 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			w.WriteHeader(500)
			return
		}
		reads++
		switch r.URL.Path {
		case taskPath + "original-task":
			end := op.SubmittedAt.UnixMilli()
			if mode == "pending-task" {
				end = 0
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": op.SubmittedAt.UnixMilli(), "endTime": end, "isError": mode == "failed-task", "data": `{"discoveryId":"job-id"}`}})
		case discoveryPath + "/1/100":
			id := "job-id"
			if mode == "wrong-job" {
				id = "other-job"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{{ID: id, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}}})
		default:
			t.Error("unexpected API path", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	base := a.statusWriter
	attempts := 0
	a.statusWriter = discoveryCredentialCompletionClient{Client: base, mode: mode, attempts: &attempts}
	plan := configurationApplyPlan{section: "inventory", receiptOperation: func(candidate *cvk.NetworkControllerConfigurationOperation) bool {
		_, valid := discoveryCredentialOperationInput(candidate)
		return valid
	}, recordCompletion: func(ctx context.Context, candidate *cvk.NetworkControllerConfigurationOperation) error {
		return a.recordDiscoveryCredentialCreate(ctx, c, source, p, 0, candidate)
	}}
	err := plan.completeWithReceipt(ctx, a, op)
	if (err == nil) != (mode == "complete") || writes != 0 {
		t.Fatal("unexpected completion", err, writes)
	}
	a.statusWriter = base
	state := journalState(t, a)
	if mode == "complete" || mode == "lost-ack" {
		if attempts != 1 || !discoveryCredentialReceiptPairPresent(state, state.Operation) {
			t.Fatal("receipt pair not atomically retained")
		}
		before := state.DeepCopy()
		restarted := *a
		nc, err := restarted.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		freshProjection, err := restarted.prepareDiscoveryCredentialProjection(nc, c, source)
		if err != nil {
			t.Fatal(err)
		}
		previousReads := reads
		if err := restarted.recordDiscoveryCredentialCreate(ctx, c, source, freshProjection, 0, op); err != nil {
			t.Fatal("retained completion did not recover", err)
		}
		if reads != previousReads || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("completion recovery replayed or rewrote evidence")
		}
		if err := restarted.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
		state = journalState(t, a)
		if state.Operation != nil || len(state.ActionApplications) != 1 || len(state.CredentialApplications) != 1 {
			t.Fatal("release lost paired evidence")
		}
		encoded, _ := json.Marshal(state)
		if strings.Contains(string(encoded), discoveryCredentialMarker) {
			t.Fatal("credential entered journal")
		}
	} else {
		if err := a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationBusy) {
			t.Fatal("incomplete evidence released journal", err)
		}
		if mode == "conflict" || mode == "failed-task" || mode == "pending-task" || mode == "wrong-job" {
			if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 || len(state.CredentialApplications) != 0 {
				t.Fatal("failed verification published partial evidence")
			}
		}
	}
}

func TestDiscoveryCredentialCapacityBeforePreparation(t *testing.T) {
	for _, mode := range []string{"actions-full", "credentials-full", "action-bytes", "same-name", "legacy-creator", "existing-version-receipt", "missing-versions", "altered-version", "wrong-action", "malformed-input", "unknown-version"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
			a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if err != nil {
				t.Fatal(err)
			}
			r, input, err := p.bindRequest(nc, c, source, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			state := &cvk.NetworkControllerConfigurationStatus{}
			name := discoveryPublicDigest("fixture")
			action := "Create"
			switch mode {
			case "actions-full":
				state.ActionApplications = make([]cvk.NetworkControllerConfigurationActionApplication, 256)
			case "credentials-full":
				state.CredentialApplications = make([]cvk.NetworkControllerConfigurationCredentialApplication, 256)
			case "action-bytes":
				for i := 0; i < 230; i++ {
					state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: fmt.Sprint(i) + strings.Repeat("x", 500), Operation: cvk.NetworkControllerConfigurationOperation{Resource: strings.Repeat("x", 500), SourceVersion: strings.Repeat("x", 128), ConfigUID: strings.Repeat("x", 128)}})
				}
			case "same-name":
				state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: discoveryCredentialReceiptPrefix + name + ":foreign"}}
			case "legacy-creator":
				state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: ownedDiscoveryReceiptPrefix + name + ":legacy"}}
			case "existing-version-receipt":
				state.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "inventory", Resource: input, ConfigUID: "other"}}
			case "missing-versions":
				r.versions = nil
			case "altered-version":
				r.versions[0].Version++
			case "wrong-action":
				action = "Delete"
			case "malformed-input":
				input += "extra"
			case "unknown-version":
				input = strings.Replace(input, ":v3:", ":v9:", 1)
			}
			nc.Status.Configuration = state
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			before := state.DeepCopy()
			if _, err := a.prepareConfiguration(ctx, c, source, "inventory", input, action, configurationResourceIdentity{credentials: r.versions}); err == nil {
				t.Fatal("unsafe creator prepared")
			}
			if !reflect.DeepEqual(before, journalState(t, a)) {
				t.Fatal("failed preparation changed journal or ownership")
			}
		})
	}
}

func TestDiscoveryCredentialReceiptPairRefusesMissingOrEditedEvidence(t *testing.T) {
	a, _, _, _, op := discoveryCredentialCompletionFixture(t, nil)
	op.Phase = "Verified"
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	name, _ := discoveryCredentialOperationInput(op)
	state := journalState(t, a)
	state.Operation = op
	effect, scoped := discoveryCredentialRetainedEffect(op.Resource)
	if !scoped {
		t.Fatal("fixture lacks pre-submission scope")
	}
	state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: discoveryCredentialEffectReceiptKey(name, "job-id", effect), Operation: *op.DeepCopy(), VerifiedAt: &stamp}}
	state.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "inventory", Resource: op.Resource, ConfigUID: op.ConfigUID, TargetID: "job-id", OperationID: op.ID, Versions: op.DeepCopy().CredentialVersions}}
	if !discoveryCredentialReceiptPairPresent(state, op) {
		t.Fatal("valid synthetic receipt pair refused")
	}
	for _, mode := range []string{"no-action", "no-credentials", "no-op-versions", "both-pruned", "wrong-target", "wrong-operation", "duplicate-action", "duplicate-credentials", "foreign-creator", "wrong-name", "legacy-scope-downgrade", "old-time", "future-time", "missing-task", "changed-counter", "unknown-leaf", "cross-job-path", "no-owner", "foreign-owner", "overlapping-owner", "duplicate-owner"} {
		t.Run(mode, func(t *testing.T) {
			s := state.DeepCopy()
			switch mode {
			case "no-action":
				s.ActionApplications = nil
			case "no-credentials":
				s.CredentialApplications = nil
			case "no-op-versions":
				s.Operation.CredentialVersions = nil
			case "both-pruned":
				s.Operation.CredentialVersions = nil
				s.CredentialApplications[0].Versions = nil
				s.ActionApplications[0].Operation.CredentialVersions = nil
			case "wrong-target":
				s.CredentialApplications[0].TargetID = "other-job"
			case "wrong-operation":
				s.CredentialApplications[0].OperationID = strings.Repeat("b", 32)
			case "duplicate-action":
				s.ActionApplications = append(s.ActionApplications, *s.ActionApplications[0].DeepCopy())
			case "duplicate-credentials":
				s.CredentialApplications = append(s.CredentialApplications, *s.CredentialApplications[0].DeepCopy())
			case "foreign-creator":
				s.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "wrong-name":
				s.ActionApplications[0].Resource = discoveryCredentialEffectReceiptKey(strings.Repeat("b", 64), "job-id", effect)
			case "legacy-scope-downgrade":
				s.ActionApplications[0].Resource = discoveryCredentialReceiptPrefix + name + ":job-id"
			case "old-time":
				s.ActionApplications[0].VerifiedAt = &metav1.Time{Time: time.Unix(1, 0)}
			case "future-time":
				s.ActionApplications[0].VerifiedAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
			case "missing-task":
				s.Operation.TaskID = ""
				s.ActionApplications[0].Operation.TaskID = ""
			case "changed-counter":
				s.Operation.CredentialVersions[0].Version++
				s.ActionApplications[0].Operation.CredentialVersions = s.Operation.DeepCopy().CredentialVersions
				s.CredentialApplications[0].Versions = s.Operation.DeepCopy().CredentialVersions
			case "unknown-leaf":
				s.Operation.CredentialVersions[0].Path = "/discovery/0/unknown"
			case "cross-job-path":
				s.Operation.CredentialVersions = append(s.Operation.CredentialVersions, cvk.NetworkControllerConfigurationCredentialVersion{Path: "/discovery/1/snmp_ro_community", Version: 1, SourceAlias: "alias", SecretName: "secret", SecretKey: "key"})
			case "no-owner":
				s.Owners = nil
			case "foreign-owner":
				s.Owners[0].ConfigUID = "foreign"
			case "overlapping-owner":
				s.Owners = append(s.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigUID: "foreign", ConfigName: "foreign"})
			case "duplicate-owner":
				s.Owners = append(s.Owners, s.Owners[0])
			}
			if discoveryCredentialReceiptPairPresent(s, s.Operation) {
				t.Fatal("incomplete or edited pair accepted")
			}
		})
	}
}

func TestDiscoveryCredentialPermitRechecksCapacity(t *testing.T) {
	for _, mode := range []string{"full", "foreign-creator", "pruned-versions"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, nc, c, source, _ := discoveryProjectionFixture(t, discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false), map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if err != nil {
				t.Fatal(err)
			}
			r, input, err := p.bindRequest(nc, c, source, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			identity := configurationResourceIdentity{credentials: r.versions}
			op, err := a.prepareConfiguration(ctx, c, source, "inventory", input, "Create", identity)
			if err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "full":
				nc.Status.Configuration.ActionApplications = make([]cvk.NetworkControllerConfigurationActionApplication, 256)
			case "foreign-creator":
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Resource: discoveryCredentialReceiptPrefix + discoveryPublicDigest("fixture") + ":foreign"}}
			case "pruned-versions":
				nc.Status.Configuration.Operation.CredentialVersions = nil
				identity.credentials = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, c, source, op.ID, identity); err == nil {
				t.Fatal("unsafe submission permit granted")
			}
			if journalState(t, a).Operation.Phase != "Prepared" {
				t.Fatal("refused permit consumed operation")
			}
		})
	}
}
