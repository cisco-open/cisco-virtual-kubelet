//go:build live && envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Opt-in lab qualifier using isolated Kubernetes persistence and the existing
// product dispatcher, client and manager. Device authentication is not inferred.
func TestLiveNoAuthGeneratedNativeExecution(t *testing.T) {
	if os.Getenv("CATC_NOAUTH_NATIVE_EXECUTION") != "approved-lab-101" {
		t.Skip("explicit .101 job qualification required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	observer := liveClient(t)
	before, err := observer.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := observer.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	approved := false
	for _, d := range devices {
		if d.ManagementIP == "198.51.100.101" {
			approved = true
		}
	}
	if !approved {
		t.Fatal("approved .101 target absent from inventory; no mutation permitted")
	}
	name := fmt.Sprintf("CVK-NoAuth-Q-%d", time.Now().UnixNano())
	user := "cvkNoAuthQualification"
	if _, exists := before[name]; exists {
		t.Fatal("generated name already exists")
	}
	raw := discoveryCredentialSource(t, map[string]any{"name": name, "ip_address_list": "198.51.100.101", "snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": user}, false)
	a, nc, c, _, _ := discoveryProjectionFixture(t, raw, nil)
	nc.Spec.Endpoint = observer.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	installWirelessFeatureQualificationAPI(t, api, "noauth-native-generated")(a, c)
	protectCredentialFixtureController(t, a)
	admin := a.statusWriter
	key := ctrlclient.ObjectKeyFromObject(c)
	a.client = observer
	session, _, err := observer.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := session.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	posts, deletes, attempts := 0, 0, 0
	nativeID := ""
	allowDelete := false
	session.HTTPClient.Transport = discoveryCleanupRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet || req.Method == http.MethodPost && req.URL.Path == authPath {
			return base.RoundTrip(req)
		}
		attempts++
		if req.Method == http.MethodPost && req.URL.Path == discoveryPath && posts == 0 {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
			var actual map[string]any
			if json.Unmarshal(body, &actual) != nil {
				return nil, errors.New("invalid qualification payload")
			}
			expected := map[string]any{"name": name, "discoveryType": "SINGLE", "ipAddressList": "198.51.100.101", "protocolOrder": "SSH", "snmpVersion": "v3", "snmpMode": "NoAuthNoPriv", "snmpUserName": user}
			if !reflect.DeepEqual(actual, expected) {
				return nil, errors.New("qualification refused unexpected target, fields or credentials")
			}
			posts++
			return base.RoundTrip(req)
		}
		if req.Method == http.MethodDelete && allowDelete && nativeID != "" && deletes == 0 && req.URL.Path == discoveryPath+"/"+url.PathEscape(nativeID) {
			deletes++
			return base.RoundTrip(req)
		}
		return nil, errors.New("qualification refused unrelated or repeated native mutation")
	})
	dir := os.Getenv("CATC_NOAUTH_NATIVE_EVIDENCE")
	if dir == "" {
		t.Fatal("private evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	save := func(file string, value any) { writeLiveDiscoveryPublicRecord(t, dir, file, value) }
	save("baseline.json", map[string]any{"name": name, "target": "198.51.100.101", "discoveryJobs": len(before), "devices": len(devices), "deviceAuthenticationQualified": false})
	unused := 0
	enrollPublicCredentialFixture(t, a, c, "inline", &unused)
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	success, unknown := false, false
	issue := ""
	for i := 0; i < 90; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			issue = err.Error()
			break
		}
		if err := admin.Get(ctx, key, c); err != nil {
			issue = err.Error()
			break
		}
		state := journalState(t, a)
		if state != nil && state.Operation != nil {
			op := state.Operation
			save("operation.json", op)
			if op.Action == "Create" && op.Phase == "Accepted" && !unknown {
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					issue = err.Error()
					break
				}
				unknown = true
			}
			if op.Action == "Create" && validAPIID(op.TaskID) && op.SubmittedAt != nil {
				id, done, e := observer.completedDiscoveryTaskResource(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
				if e == nil && done {
					rows, e := observer.discoveries(ctx)
					if e != nil {
						issue = e.Error()
						break
					}
					row, present := rows[name]
					if present && row.ID == id && row.Addresses == "198.51.100.101" && discoveryType(row.Type) == "SINGLE" {
						nativeID = id
					}
					save("native-readback.json", map[string]any{"name": name, "nativeID": id, "createdTaskID": op.TaskID, "present": present, "condition": row.Condition, "status": row.Status, "snmpVersionPresent": row.SNMPVersion != "", "snmpModePresent": row.SNMPMode != "", "snmpUserNamePresent": row.SNMPUser != ""})
					if nativeID != "" && row.Condition == "Complete" && row.Status == "Inactive" && (row.SNMPVersion == "" || row.SNMPMode == "" || row.SNMPUser == "") {
						issue = "native list readback omits required NoAuth metadata"
						break
					}
				}
			}
		}
		if state != nil && state.Operation == nil && len(state.ActionApplications) == 1 && c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			history, e := discoveryCredentialLineages(state, string(c.UID))
			if e != nil {
				issue = e.Error()
				break
			}
			creator := history.latest[discoveryPublicDigest(name)]
			if creator == nil || len(state.CredentialApplications) != 0 {
				issue = "NoAuth creator missing or fake credential delivery"
				break
			}
			nativeID = creator.identity.id
			success = true
			break
		}
		time.Sleep(time.Second)
	}
	if !success && issue == "" {
		issue = "native execution did not reach InSync within bounded observation"
	}
	save("before-cleanup.json", map[string]any{"success": success, "issue": issue, "originalUnknownRecovered": unknown, "nativeID": nativeID, "posts": posts, "deletes": deletes, "attempts": attempts, "configStatus": c.Status, "journal": journalState(t, a)})
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cleanupCancel()
	allowDelete = nativeID != ""
	cleaned := false
	supervised := false
	if success {
		if err := admin.Delete(cleanupCtx, c); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 60; i++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(cleanupCtx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(cleanupCtx, key, c); err != nil {
				t.Fatal(err)
			}
			current, e := a.currentController(cleanupCtx)
			if e != nil {
				t.Fatal(e)
			}
			if discoveryDeletionCompleted(current, c) {
				cleaned = true
				break
			}
			time.Sleep(time.Second)
		}
		if cleaned {
			manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
			if _, err := manager.Reconcile(cleanupCtx, ctrl.Request{NamespacedName: a.key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(cleanupCtx, key, c); err != nil {
				t.Fatal(err)
			}
			if hasDiscoveryConfigFinalizer(c) {
				t.Fatal("manager did not release verified cleanup")
			}
		}
	} else if nativeID != "" {
		// Exact generated fixture only, linked through its original completed task.
		// This cannot publish a product receipt or qualify the failed lifecycle.
		rows, e := observer.discoveries(cleanupCtx)
		if e != nil {
			t.Fatal(e)
		}
		row, present := rows[name]
		if !present || row.ID != nativeID || row.Addresses != "198.51.100.101" {
			t.Fatal("uncertain cleanup identity; retained proof requires inspection")
		}
		submitted := time.Now()
		task, e := observer.deleteDiscovery(cleanupCtx, nativeID)
		if e != nil {
			t.Fatal("cleanup outcome uncertain; no replay", e)
		}
		if e := waitSiteCredentialTask(cleanupCtx, observer, task, submitted); e != nil {
			t.Fatal(e)
		}
		cleaned = true
		supervised = true
	}
	after, e := observer.discoveries(cleanupCtx)
	if e != nil {
		t.Fatal(e)
	}
	afterDevices, e := observer.ListDevices(cleanupCtx)
	if e != nil {
		t.Fatal(e)
	}
	identity := func(rows []Device) map[string]string {
		m := map[string]string{}
		for _, d := range rows {
			m[d.ID] = d.Serial + "/" + d.ManagementIP
		}
		return m
	}
	restored := reflect.DeepEqual(before, after) && reflect.DeepEqual(identity(devices), identity(afterDevices))
	save("result.json", map[string]any{"qualified": success && cleaned && unknown && restored, "issue": issue, "posts": posts, "deletes": deletes, "mutationAttempts": attempts, "originalUnknownRecovered": unknown, "cleanupCompleted": cleaned, "cleanupSupervised": supervised, "baselineRestored": restored, "nativeDeviceOperationSubmitted": false, "installedActors": false, "deviceAuthenticationQualified": false, "nativeID": nativeID})
	if !success || !cleaned || !unknown || !restored || posts != 1 || deletes != 1 || attempts != 2 {
		t.Fatal("native NoAuth qualification incomplete", issue, "created", posts, "deleted", deletes, "baseline restored", restored, "supervised cleanup", supervised)
	}
	t.Log("generated NoAuth job create, original Unknown recovery, receipt-only metadata, config Delete and manager release passed; native baseline restored; no device authentication claim")
}
