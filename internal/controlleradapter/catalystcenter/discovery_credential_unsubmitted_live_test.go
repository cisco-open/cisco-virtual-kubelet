//go:build live && envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type credentialCancellationReadOnlyTransport struct {
	base             http.RoundTripper
	reads, mutations atomic.Int64
	refuseRead       func(*http.Request) bool
}

func (t *credentialCancellationReadOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		t.reads.Add(1)
		if t.refuseRead != nil && t.refuseRead(req) {
			return nil, errors.New("qualification-injected native read failure before dispatch")
		}
	} else if req.Method != http.MethodPost || req.URL.Path != authPath {
		t.mutations.Add(1)
	}
	return t.base.RoundTrip(req)
}

// Actual Catalyst Center reads and real, isolated Kubernetes persistence; no
// user's cluster, device credentials, native discovery mutation or scan.
func TestLiveDiscoveryCredentialUnsubmittedDeletion(t *testing.T) {
	liveDiscoveryCredentialUnsubmittedDeletion(t, false, false)
}

// Uses the same mutation-refusing transport and real isolated API as the
// versioned case. No actual discovery job or credential delivery is permitted.
func TestLiveDiscoveryInitialNoAuthUnsubmittedDeletion(t *testing.T) {
	liveDiscoveryCredentialUnsubmittedDeletion(t, true, false)
}

func TestLiveDiscoveryNoAuthRetainPreflightRefusal(t *testing.T) {
	liveDiscoveryCredentialUnsubmittedDeletion(t, true, true)
}

func liveDiscoveryCredentialUnsubmittedDeletion(t *testing.T, metadata, retain bool) {
	flag := "CATC_DISCOVERY_CREDENTIAL_UNSUBMITTED"
	deadline := 3 * time.Minute
	if metadata {
		flag = "CATC_DISCOVERY_INITIAL_NOAUTH_UNSUBMITTED"
		deadline = 10 * time.Minute
	}
	if retain {
		flag = "CATC_DISCOVERY_NOAUTH_RETAIN"
		deadline = 10 * time.Minute
	}
	if os.Getenv(flag) != "read-only-real-api" {
		t.Skip("explicit read-only unsubmitted qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	observer := liveClient(t)
	session, _, err := observer.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := session.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transport := &credentialCancellationReadOnlyTransport{base: &siteRetainedReadOnlyTransport{base: base}}
	session.HTTPClient.Transport = transport
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	identities := func(rows []Device) map[string]string {
		out := map[string]string{}
		for _, device := range rows {
			out[device.ID] = device.Serial + "/" + device.ManagementIP
		}
		return out
	}
	modes := []string{"inline", "configmap", "lost-retirement-ack", "lost-completion-ack", "proven-preflight-refusal"}
	if retain {
		modes = []string{"proven-preflight-refusal"}
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			before, err := observer.discoveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			devices, err := observer.ListDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("CVK-Unsubmitted-%d", time.Now().UnixNano())
			if _, exists := before[name]; exists {
				t.Fatal("qualification name is already present")
			}
			fields := map[string]any{"name": name, "user_name_list": []any{"cvk-unused-cancellation-fixture"}, "password_list_version": 1}
			values := map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}
			if metadata {
				fields = map[string]any{"name": name, "snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "cvk-unused-cancellation-fixture"}
				values = nil
			}
			raw := discoveryCredentialSource(t, fields, false)
			a, nc, c, _, _ := discoveryProjectionFixture(t, raw, values)
			nc.Spec.Endpoint = observer.cfg.Endpoint
			if err := a.statusWriter.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			installWirelessFeatureQualificationAPI(t, api, "credential-native-"+mode)(a, c)
			protectCredentialFixtureController(t, a)
			admin := a.statusWriter
			key := ctrlclient.ObjectKeyFromObject(c)
			if mode == "configmap" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unsubmitted-source", Namespace: c.Namespace}, Data: map[string]string{"model": string(raw)}}
				if err := admin.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				c.Spec.Source.Inline = nil
				c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
				if err := admin.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
			}
			a.client = observer
			unusedPosts := 0
			if retain {
				c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
				c.Finalizers = nil
				if err := admin.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
			} else {
				enrollPublicCredentialFixture(t, a, c, mode, &unusedPosts)
			}
			preparationHits := 0
			a.statusWriter = publicCredentialPreparedFaultClient{statusOnlyFeatureClient{Client: admin}, &preparationHits}
			for i := 0; i < 12; i++ {
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
				if err := admin.Get(ctx, key, c); err != nil {
					t.Fatal(err)
				}
				state := journalState(t, a)
				if state != nil && state.Operation != nil && state.Operation.Phase == "Prepared" {
					break
				}
			}
			state := journalState(t, a).DeepCopy()
			if preparationHits != 1 || state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.SubmittedAt != nil || state.Operation.TaskID != "" || transport.mutations.Load() != 0 {
				t.Fatal("failed to stop before native submission")
			}
			original := state.Operation.DeepCopy()
			if retain && (hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c)) {
				t.Fatal("Retain path acquired cleanup enrollment")
			}
			if metadata && (discoveryCredentialInputScopePrefix(original.Resource) != discoveryCredentialMetadataScopePrefix || len(original.CredentialVersions) != 0 || len(c.Spec.SecretRefs) != 0) {
				t.Fatal("NoAuth qualification did not select explicit zero-version v4 metadata")
			}
			if mode == "proven-preflight-refusal" {
				var injected atomic.Bool
				refusedID := ""
				transport.refuseRead = func(req *http.Request) bool {
					if req.URL.Path != discoveryPath+"/1/100" {
						return false
					}
					if injected.Load() {
						return true
					}
					current, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						return false
					}
					op := current.Status.Configuration.Operation
					if op != nil && op.Phase == "Submitting" && injected.CompareAndSwap(false, true) {
						refusedID = op.ID
						return true
					}
					return false
				}
				a.statusWriter = statusOnlyFeatureClient{Client: admin}
				for i := 0; i < 8 && !injected.Load(); i++ {
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal(err)
					}
				}
				transport.refuseRead = nil
				if err := admin.Get(ctx, key, c); err != nil {
					t.Fatal(err)
				}
				refused := journalState(t, a)
				ready := meta.FindStatusCondition(c.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
				if !injected.Load() || refusedID == "" || transport.mutations.Load() != 0 || refused.Operation != nil || len(refused.ActionApplications) != 0 || len(refused.CredentialApplications) != 0 || !reflect.DeepEqual(refused.Owners, state.Owners) || ready == nil || ready.Reason != "NativePreflightRefused" {
					t.Fatal("live read failure did not retire only the proven unsubmitted operation", c.Status.Conditions)
				}
				after, err := observer.discoveries(ctx)
				if err != nil {
					t.Fatal(err)
				}
				afterDevices, err := observer.ListDevices(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(identities(devices), identities(afterDevices)) {
					t.Fatal("read-only proof qualification changed native inventory")
				}
				if dir := os.Getenv("CATC_DISCOVERY_UNSUBMITTED_EVIDENCE"); dir != "" {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					writeLiveDiscoveryPublicRecord(t, dir, mode+".json", map[string]any{"originalPreparedOperation": original, "provenUndispatchedOperationID": refusedID, "nativeReadFailureInjected": true, "nativeMutationAttempts": transport.mutations.Load(), "nativeDiscoveryJobs": len(after), "nativeDevices": len(afterDevices), "operationRetired": true, "credentialDelivery": false, "installedActors": false, "realIsolatedKubernetesAPI": true})
				}
				t.Log("real API/native read path: original Prepared safely retired before fresh planning; injected final post-permit read failure proved no dispatch; only operation retired, inventory owner retained, zero mutation attempts; no credential delivery or automatic retry attempted")
				return
			}
			if err := admin.Delete(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			worker := statusOnlyFeatureClient{Client: admin}
			a.statusWriter = worker
			faultHits := 0
			if mode == "lost-retirement-ack" {
				a.statusWriter = credentialUnsubmittedFaultClient{Client: worker, mode: "drain-prepared-lost-retirement-ack", hits: &faultHits}
			}
			if mode == "lost-completion-ack" {
				a.statusWriter = credentialCompletionFaultClient{worker, "completion-lost-ack", &faultHits}
			}
			complete := false
			for i := 0; i < 12; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
				if err := admin.Get(ctx, key, c); err != nil {
					t.Fatal(err)
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if discoveryDeletionCompleted(nc, c) {
					complete = true
					break
				}
			}
			if !complete || transport.mutations.Load() != 0 {
				t.Fatal("native read-only cancellation did not complete", c.Status.Conditions)
			}
			record, err := discoveryCredentialUnsubmittedRecord(nc, c)
			if err != nil || !reflect.DeepEqual(record.UnsubmittedOperation, original) || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.ActionApplications) != 0 || len(nc.Status.Configuration.CredentialApplications) != 0 || !reflect.DeepEqual(nc.Status.Configuration.Owners, state.Owners) {
				t.Fatal("lost witness or fabricated native/credential effect", err)
			}
			if mode != "inline" && mode != "configmap" && faultHits != 1 {
				t.Fatal("lost acknowledgement was not exercised")
			}
			reads := transport.reads.Load()
			manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
			if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || c.Finalizers[0] != "example.com/keeper" || transport.reads.Load() != reads || !reflect.DeepEqual(nc.Status.Configuration, journalState(t, a)) {
				t.Fatal("manager changed native work or retained evidence")
			}
			after, err := observer.discoveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			afterDevices, err := observer.ListDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(identities(devices), identities(afterDevices)) || transport.mutations.Load() != 0 {
				t.Fatal("native baseline changed or mutation attempted")
			}
			if dir := os.Getenv("CATC_DISCOVERY_UNSUBMITTED_EVIDENCE"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				writeLiveDiscoveryPublicRecord(t, dir, filepath.Base(mode)+".json", map[string]any{"mode": mode, "nativeDiscoveryJobs": len(after), "nativeDevices": len(afterDevices), "nativeMutationAttempts": transport.mutations.Load(), "retained": record, "configStatus": c.Status, "managerReleased": true, "realIsolatedKubernetesAPI": true, "installedActors": false, "credentialDelivery": false})
			}
			t.Logf("%s: real Kubernetes API and native absence reads; original preparation retained, manager released, %d native jobs/%d devices preserved; zero native mutation attempts and no credential delivery", mode, len(after), len(afterDevices))
		})
	}
}
