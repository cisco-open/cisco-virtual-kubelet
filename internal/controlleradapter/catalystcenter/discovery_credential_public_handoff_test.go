// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialPublicHandoffModes = []string{"cleanup-inline", "configmap-cleanup", "same-name", "lost-ack", "rejected-write", "configmap-source-replan", "configmap-source-loss", "projection-loss", "missing-seal", "missing-pair", "old-config-present", "counter-reuse", "pending-operation", "controller-change", "unenrolled"}

func TestDiscoveryCredentialPublicHandoff(t *testing.T) {
	for _, mode := range credentialPublicHandoffModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-public-"+mode, nil)
		})
	}
}

type publicCredentialCustodyFaultClient struct {
	ctrlclient.Client
	mode   string
	writes *int
}
type publicCredentialCustodyFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner publicCredentialCustodyFaultClient
}

func (c publicCredentialCustodyFaultClient) Status() ctrlclient.SubResourceWriter {
	return publicCredentialCustodyFaultWriter{c.Client.Status(), c}
}
func (w publicCredentialCustodyFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	var r discoveryOwnershipRetirement
	if cond == nil || json.Unmarshal([]byte(cond.Message), &r) != nil || r.CredentialCustody == nil {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.owner.writes++
	if w.owner.mode == "rejected-write" && *w.owner.writes == 1 {
		return errors.New("rejected public custody CAS")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.owner.mode == "lost-ack" && *w.owner.writes == 1 {
		return errors.New("lost public custody acknowledgement")
	}
	return nil
}

func exerciseDiscoveryCredentialPublicHandoff(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, projected string, admin ctrlclient.Client, realAPI bool) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	before, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline := before.Status.Configuration.DeepCopy()
	initialSourceVersion := ""
	if cm != nil {
		initialSourceVersion = cm.ResourceVersion
	}
	switch mode {
	case "missing-seal", "missing-pair", "pending-operation":
		copy := before.DeepCopy()
		if mode == "missing-pair" {
			copy.Status.Configuration.CredentialApplications = nil
		}
		if mode == "missing-seal" {
			cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
			var r discoveryOwnershipRetirement
			json.Unmarshal([]byte(cond.Message), &r)
			r.CredentialHistoryDigest = ""
			raw, _ := json.Marshal(r)
			cond.Message = string(raw)
		}
		if mode == "pending-operation" {
			op := copy.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
			op.Phase = "Prepared"
			copy.Status.Configuration.Operation = op
		}
		if err := admin.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	case "configmap-source-loss":
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	case "projection-loss":
		if err := os.Remove(projected); err != nil {
			t.Fatal(err)
		}
	case "controller-change":
		copy := before.DeepCopy()
		copy.Generation++
		copy.Spec.Connection.MaxConcurrentRequests = 8
		if err := admin.Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	case "unenrolled":
		c.Finalizers = []string{"example.com/keeper"}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	reads, mutations, writes := 0, 0, 0
	sourceChanged := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
			mutations++
			t.Error("handoff crossed into native execution", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		reads++
		if mode == "configmap-source-replan" && !sourceChanged {
			cm.Data["model"] += " "
			if err := admin.Update(ctx, cm); err != nil {
				t.Error(err)
			}
			sourceChanged = true
		}
		fmt.Fprint(w, `{"response":[]}`)
	})
	a.statusWriter = publicCredentialCustodyFaultClient{statusOnlyFeatureClient{Client: admin}, mode, &writes}
	defer func() { a.statusWriter = admin }()
	expected := strings.HasPrefix(mode, "revision-") || mode == "cleanup-inline" || mode == "configmap-cleanup" || mode == "same-name" || mode == "lost-ack" || mode == "rejected-write" || mode == "configmap-source-replan"
	transferred := false
	for i := 0; i < 18; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			if mode != "controller-change" || !errors.Is(err, errStaleController) {
				t.Fatal(err)
			}
			break
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		nc := new(cvk.NetworkController)
		if err := admin.Get(ctx, a.key, nc); err != nil {
			t.Fatal(err)
		}
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section == "inventory" && owner.ConfigUID == string(c.UID) {
				transferred = true
			}
		}
		if transferred {
			break
		}
		if !expected && i >= 7 {
			break
		}
	}
	nc := new(cvk.NetworkController)
	if err := admin.Get(ctx, a.key, nc); err != nil {
		t.Fatal(err)
	}
	if transferred != expected || mutations != 0 {
		t.Fatal("public handoff failed or made a native mutation", mode, transferred, mutations, c.Status.Conditions)
	}
	if !expected {
		if writes != 0 || !reflect.DeepEqual(nc.Status.Configuration.Owners, baseline.Owners) {
			t.Fatal("unqualified public handoff changed custody")
		}
		return
	}
	if reads < 4 || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, baseline.ActionApplications) || !reflect.DeepEqual(nc.Status.Configuration.CredentialApplications, baseline.CredentialApplications) {
		t.Fatal("metadata handoff changed history or skipped observation")
	}
	expectedWrites := 1
	if mode == "rejected-write" {
		expectedWrites = 2
	}
	if writes != expectedWrites {
		t.Fatal("custody publication replayed or fault missing", writes)
	}
	source, err := loadControllerSource(ctx, admin, c)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := discoveryCredentialCustodyRecord(nc, c, source)
	if err != nil {
		t.Fatal("public custody is not independently readable", err)
	}
	if record.UnsubmittedOperation != nil {
		copy := nc.DeepCopy()
		op := record.UnsubmittedOperation.DeepCopy()
		op.ConfigName = c.Name
		op.ConfigUID = string(c.UID)
		op.ConfigGeneration = c.Generation
		op.ControllerGeneration = nc.Generation
		copy.Status.Configuration.Operation = op
		if _, _, err := discoveryCredentialCustodyRecord(copy, c, source); err == nil {
			t.Fatal("cancelled operation identity was reused by a new owner")
		}
	}
	if mode == "configmap-source-replan" && (!sourceChanged || record.CredentialCustody.SourceVersion == initialSourceVersion || record.CredentialCustody.SourceVersion != cm.ResourceVersion) {
		t.Fatal("stale source was transferred rather than revalidated")
	}
	a.statusWriter = admin
	execution := "complete"
	if strings.HasPrefix(mode, "revision-") {
		execution = mode
	}
	if mode == "cleanup-inline" || mode == "configmap-cleanup" {
		execution = "cleanup-inline"
		if cm != nil {
			execution = "cleanup-configmap"
		}
	}
	exerciseDiscoveryCredentialCustodyExecution(t, a, c, execution, cm, admin, realAPI)
}
