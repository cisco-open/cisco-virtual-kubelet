// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const externalAuthModel = `{"catalyst_center":{"system_settings":{"external_authentication":{"enabled":false,"aaa_attribute":"CVK-Role"}}}}`

func TestExternalAuthCompilerRejectsUnsupportedIntent(t *testing.T) {
	for _, s := range []string{`{"authentication_and_policy_servers":{}}`, `{"external_authentication":{"enabled":"false"}}`, `{"external_authentication":{"aaa_attribute":""}}`, `{"external_authentication":{"password":"must-not-appear"}}`} {
		_, err := parseExternalAuth([]byte(`{"catalyst_center":{"system_settings":` + s + `}}`))
		if err == nil {
			t.Fatal("unsupported intent accepted")
		}
	}
	d, err := parseExternalAuth([]byte(`{"catalyst_center":{"system_settings":{"external_authentication":{}}}}`))
	if err != nil || d.enabled {
		t.Fatal("module default lost", err)
	}
}
func TestExternalAuthSynchronousReconcile(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"system_settings"}
	config.Spec.Source.Inline.Raw = []byte(externalAuthModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	enabled, attribute := true, "old"
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == "/dna/intent/api/v1/authentication-policy-servers" {
			fmt.Fprint(w, `{"response":[{"instanceUuid":"server","ipAddress":"192.0.2.10","protocol":"RADIUS"}]}`)
			return
		}
		if r.URL.Path != externalAuthPath && r.URL.Path != externalAAAAttributePath {
			t.Error("unexpected API/task poll", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if r.URL.Path == externalAuthPath {
				fmt.Fprintf(w, `{"response":{"external-authentication-flag":[{"enabled":%t}]}}`, enabled)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"aaa-attributes": []any{map[string]string{"attributeName": attribute}}}})
			}
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid payload")
		}
		if r.Method != "POST" || len(body) != 1 {
			t.Error("invalid submission")
		}
		if r.URL.Path == externalAuthPath {
			if v, ok := body["enable"].(bool); !ok {
				t.Error("enable missing")
			} else {
				enabled = v
			}
		} else {
			if v, ok := body["attributeName"].(string); !ok {
				t.Error("attribute missing")
			} else {
				attribute = v
			}
		}
		writes++
		fmt.Fprint(w, `{"response":{"message":"Saved"}}`)
	})
	seenVerification := false
	for i := 0; i < 12; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if op := journalState(t, a).Operation; op != nil {
			if op.TaskID != "" {
				t.Fatal("synthetic task ID")
			}
			if op.Phase == "Verifying" {
				seenVerification = true
			}
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 2 || !seenVerification {
		t.Fatal("synchronous apply did not verify exactly once", config.Status.Phase, writes)
	}
}
func TestExternalAuthRequiresSynchronousReceipt(t *testing.T) {
	for _, mode := range []string{"async", "missing", "task", "malformed", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				writes++
				switch mode {
				case "async":
					w.WriteHeader(202)
					fmt.Fprint(w, `{"response":{"message":"saved"}}`)
				case "task":
					fmt.Fprint(w, `{"response":{"message":"saved","taskId":"task"}}`)
				case "malformed":
					fmt.Fprint(w, `oops`)
				case "unauthorized":
					w.WriteHeader(401)
				default:
					fmt.Fprint(w, `{}`)
				}
			})
			if err := c.submitExternalAuth(context.Background(), externalAuthPath, map[string]any{"enable": false}); err == nil || writes != 1 {
				t.Fatal("invalid response accepted or replayed", writes, err)
			}
		})
	}
}
func TestSynchronousJournalUncertainResponseIsFenced(t *testing.T) {
	a, config := journalFixture(t)
	writes := 0
	w := &configurationWrite{resource: "singleton", action: "Update", identity: configurationResourceIdentity{target: "singleton"}, submitCompleted: func(context.Context) error { writes++; return errors.New("lost response") }}
	if err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", w); err == nil {
		t.Fatal("uncertain response accepted")
	}
	if op := journalState(t, a).Operation; op.Phase != "Unknown" || op.TaskID != "" {
		t.Fatal("uncertain write not fenced")
	}
	restarted := *a
	if err := restarted.submitConfigurationWrite(context.Background(), config, nil, "sites", w); err == nil || writes != 1 {
		t.Fatal("uncertain synchronous write replayed")
	}
}
func TestSynchronousWriteRequiresExclusiveSubmissionFunction(t *testing.T) {
	for _, both := range []bool{false, true} {
		a, config := journalFixture(t)
		w := &configurationWrite{resource: "singleton", action: "Update"}
		if both {
			w.submit = func(context.Context) (Task, error) { t.Fatal("unexpected submit"); return Task{}, nil }
			w.submitCompleted = func(context.Context) error { t.Fatal("unexpected submit"); return nil }
		}
		if err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", w); err == nil {
			t.Fatal("invalid callback contract accepted")
		}
		if state := journalState(t, a); state != nil && state.Operation != nil {
			t.Fatal("invalid closure claimed operation")
		}
	}
}

type synchronousReceiptFailureClient struct {
	ctrlclient.Client
	persist bool
}
type synchronousReceiptFailureWriter struct {
	ctrlclient.SubResourceWriter
	persist bool
}

func (c synchronousReceiptFailureClient) Status() ctrlclient.SubResourceWriter {
	return synchronousReceiptFailureWriter{c.Client.Status(), c.persist}
}
func (w synchronousReceiptFailureWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*ciskov1.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Verifying" {
		if w.persist {
			if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
				return err
			}
		}
		return errors.New("simulated synchronous receipt persistence failure")
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
func TestSynchronousJournalLostCompletionAcknowledgement(t *testing.T) {
	for _, persist := range []bool{false, true} {
		a, config := journalFixture(t)
		a.statusWriter = synchronousReceiptFailureClient{a.statusWriter, persist}
		writes := 0
		write := &configurationWrite{resource: "singleton", action: "Update", identity: configurationResourceIdentity{target: "singleton"}, submitCompleted: func(context.Context) error { writes++; return nil }}
		if err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", write); err == nil {
			t.Fatal("persistence failure lost")
		}
		op := journalState(t, a).Operation
		want := "Submitting"
		if persist {
			want = "Verifying"
		}
		if op.Phase != want || op.TaskID != "" {
			t.Fatal("wrong retained phase", op.Phase)
		}
		restarted := *a
		if err := restarted.submitConfigurationWrite(context.Background(), config, nil, "sites", write); err == nil || writes != 1 {
			t.Fatal("lost acknowledgement caused replay")
		}
		if err := restarted.finishConfiguration(context.Background(), op.ID); err == nil {
			t.Fatal("unverified operation released")
		}
	}
}

func TestExternalAuthMissingServerPreflight(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutation before policy-server prerequisite")
		}
		switch r.URL.Path {
		case externalAuthPath:
			fmt.Fprint(w, `{"response":{"external-authentication-flag":[{"enabled":false}]}}`)
		case "/dna/intent/api/v1/authentication-policy-servers":
			fmt.Fprint(w, `{"response":[]}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	})
	executor := &externalAuthExecutor{client: c, desired: externalAuthIntent{enabled: true}}
	if write, err := executor.next(context.Background()); err == nil || write != nil {
		t.Fatal("missing policy server accepted")
	}
}
