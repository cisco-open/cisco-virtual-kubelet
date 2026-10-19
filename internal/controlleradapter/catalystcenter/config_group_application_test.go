// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Invoked from the stateful native lifecycle test after its successful POST.
// Reservation objects and actor authorization are fixtures, not runtime qualification.
type groupApplicationTestRecorder func(*adapter, context.Context, *configapi.NetworkControllerConfig, string, string, []*ops.ControllerConfigurationReservation, func(context.Context, []*ops.ControllerConfigurationReservation) error, time.Time) error

func testGroupApplicationPersistence(t *testing.T, native *client, raw string, verified *cvk.NetworkControllerConfigurationOperation, now time.Time, record groupApplicationTestRecorder, checkOwned func(*cvk.NetworkControllerConfigurationStatus, string, string) (bool, error), ownership ...cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	modes := []string{"inline", "configmap", "lost-response", "denied", "late-source", "late-controller", "missing-name", "wrong-operation", "missing-owner", "missing-member", "revoked-member"}
	if verified.Action == "Update" {
		modes = append(modes, "missing-ownership")
	}
	for _, mode := range modes {
		t.Run("application-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = native
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if mode == "configmap" || mode == "late-source" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "model", UID: "model-uid"}, Data: map[string]string{"model.yaml": raw}}
				if err := a.statusWriter.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			prepared := verified.DeepCopy()
			targets := prepared.PhysicalGroup.Targets
			prepared.PhysicalGroup = nil
			prepared.Phase = "Prepared"
			prepared.TaskID = ""
			prepared.SubmittedAt = nil
			prepared.ConfigName = config.Name
			prepared.ConfigUID = string(config.UID)
			prepared.ConfigGeneration = config.Generation
			prepared.ControllerGeneration = nc.Generation
			prepared.SourceUID = string(source.uid)
			prepared.SourceVersion = source.version
			op, err := contract.WithGroupIdentity(prepared, targets)
			if err != nil {
				t.Fatal(err)
			}
			op.Phase = "Verifying"
			op.TaskID = verified.TaskID
			op.SubmittedAt = verified.SubmittedAt.DeepCopy()
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
			if mode != "missing-ownership" {
				for _, entry := range ownership {
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, *entry.DeepCopy())
				}
			}
			if mode == "missing-owner" {
				nc.Status.Configuration.Owners = nil
			}
			retainedCount := len(nc.Status.Configuration.ActionApplications)
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			members := []*ops.ControllerConfigurationReservation{}
			for _, target := range targets {
				key, _ := json.Marshal([]string{contract.GroupReservationProtocol, string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity})
				digest := sha256.Sum256(key)
				name := fmt.Sprintf("config-group-%x", digest[:16])
				r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, UID: types.UID(name + "-uid"), Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{
					Protocol: contract.GroupReservationProtocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: config.Name, UID: string(config.UID)}, Device: target.Device, Node: target.Node, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}, ControllerUsername: "system:serviceaccount:lab:connector", ControllerGeneration: nc.Generation, ConfigGeneration: config.Generation, OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, PhysicalIdentity: target.PhysicalIdentity, InventoryID: target.InventoryID,
				}}
				holder, err := contract.Holder(r.Namespace, r)
				if err != nil {
					t.Fatal(err)
				}
				r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", Holder: holder, GrantedAt: metav1.NewTime(op.SubmittedAt.Add(-2 * time.Second))}
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(op.SubmittedAt.Add(-time.Second))}
				if mode == "revoked-member" {
					r.Status.Grant.Phase = "Revoked"
				}
				if err := a.statusWriter.Create(ctx, r); err != nil {
					t.Fatal(err)
				}
				members = append(members, r)
			}
			if mode == "missing-member" {
				members = nil
			}
			authorizations := 0
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				authorizations++
				switch mode {
				case "denied":
					return errors.New("denied")
				case "late-source":
					var cm core.ConfigMap
					if err := a.statusReader.Get(ctx, source.key, &cm); err != nil {
						return err
					}
					cm.Data["model.yaml"] = "changed"
					return a.statusWriter.Update(ctx, &cm)
				case "late-controller":
					var latest cvk.NetworkController
					if err := a.statusReader.Get(ctx, a.key, &latest); err != nil {
						return err
					}
					latest.Spec.Paused = true
					return a.statusWriter.Update(ctx, &latest)
				}
				return nil
			}
			originalWriter := a.statusWriter
			if mode == "lost-response" {
				a.statusWriter = lostJournalAckClient{originalWriter}
			}
			operationID, name := op.ID, "Shared"
			if mode == "wrong-operation" {
				operationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			}
			if mode == "missing-name" {
				name = "Absent"
			}
			err = record(a, ctx, config, operationID, name, members, authorize, now)
			success := mode == "inline" || mode == "configmap"
			persisted := success || mode == "lost-response"
			if (err == nil) != success {
				t.Fatal(mode, err)
			}
			var stored cvk.NetworkController
			if err := a.statusReader.Get(ctx, a.key, &stored); err != nil {
				t.Fatal(err)
			}
			if (stored.Status.Configuration.Operation.Phase == "Verified") != persisted {
				t.Fatal("incorrect operation transition")
			}
			if persisted {
				if len(stored.Status.Configuration.ActionApplications) != retainedCount+1 {
					t.Fatal("prior ownership receipt was replaced")
				}
				if owned, err := checkOwned(stored.Status.Configuration, config.Name, string(config.UID)); !owned || err != nil {
					t.Fatal(owned, err)
				}
				if _, err := contract.GroupCompletionsForReceipt(&stored, members, now); err != nil {
					t.Fatal(err)
				}
				// Restart observes the persisted receipt. It cannot repeat this transition.
				restarted := *a
				restarted.statusWriter = originalWriter
				if err := record(&restarted, ctx, config, op.ID, "Shared", members, authorize, now); err == nil {
					t.Fatal("repeated receipt transition accepted")
				}
			} else if len(stored.Status.Configuration.ActionApplications) != retainedCount {
				t.Fatal("failed recovery retained ownership")
			}
			if mode == "wrong-operation" || mode == "missing-name" {
				if authorizations != 0 {
					t.Fatal("invalid operation reached authorization")
				}
			}
		})
	}
}
