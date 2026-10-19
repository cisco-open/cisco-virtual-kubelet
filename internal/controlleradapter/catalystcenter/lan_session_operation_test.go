// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func lanSessionOperationTargets() []contract.GroupTargetIdentity {
	out := []contract.GroupTargetIdentity{}
	for _, p := range [][2]string{{"seed", "SEED-SERIAL"}, {"peer", "PEER-SERIAL"}, {"pnp", "FOC12345678"}} {
		out = append(out, contract.GroupTargetIdentity{InventoryID: p[0], PhysicalIdentity: p[1], Device: ops.ConfigurationReservationReference{Name: p[0], UID: p[0] + "-device"}, Node: ops.ConfigurationReservationReference{Name: p[0], UID: p[0] + "-node"}})
	}
	return out
}

func TestLANSessionStartOwnedOperationBinding(t *testing.T) {
	for _, mode := range []string{"prepared", "submitting", "no-group", "bad-resource", "wrong-site", "wrong-primary", "partial-group", "foreign-native-id", "wrong-serial", "foreign-owner", "late-owner", "late-source", "changed-native", "changed-version", "cancelled", "accepted-not-prepared", "task-already-present", "secret-snapshot-missing", "caller-metadata-not-declared", "configmap", "configmap-stale", "configmap-forged-bytes", "caller-spec-mutated"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			c, want, s := newLANStartFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"lan_automation"}
			metadata := want
			metadata.Password = ""
			raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"lan_automation": map[string]any{"devices": []any{metadata}}}})
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw
			var source *configSourceSnapshot
			var cm *corev1.ConfigMap
			if strings.HasPrefix(mode, "configmap") {
				cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "lan-model", Namespace: config.Namespace, UID: "lan-model-uid"}, Data: map[string]string{"model.yaml": string(raw)}}
				if err := a.statusWriter.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
				source = &configSourceSnapshot{raw: append([]byte{}, raw...), key: ctrlclient.ObjectKeyFromObject(cm), uid: cm.UID, version: cm.ResourceVersion}
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			before, err := c.lanSessionPreflight(ctx, want)
			if err != nil {
				t.Fatal(err)
			}
			resource, err := lanSessionStartResource(want, before)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "lan_automation", Resource: resource, Action: "Create", TargetID: "area", ParentID: "seed", Phase: "Prepared"}
			if source != nil {
				op.SourceUID = string(source.uid)
				op.SourceVersion = source.version
			}
			targets := lanSessionOperationTargets()
			switch mode {
			case "partial-group":
				targets = targets[:2]
			case "foreign-native-id":
				targets[2].InventoryID = "other-pnp"
			case "wrong-serial":
				targets[2].PhysicalIdentity = "other-serial"
			case "wrong-site":
				op.TargetID = "other-site"
			case "wrong-primary":
				op.ParentID = "other-seed"
			case "bad-resource":
				op.Resource = lanSessionStartPrefix + "bad"
			}
			op, err = contract.WithGroupIdentity(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			phase := "Prepared"
			if mode == "submitting" {
				phase = "Submitting"
				op.Phase = phase
				op.SubmittedAt = &metav1.Time{Time: time.Now().UTC()}
			}
			switch mode {
			case "no-group":
				op.PhysicalGroup = nil
			case "cancelled":
				op.CancellationRequestedAt = &metav1.Time{Time: time.Now().UTC()}
			case "accepted-not-prepared":
				op.Phase = "Accepted"
			case "task-already-present":
				op.TaskID = "task"
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "lan_automation", ConfigName: config.Name, ConfigUID: string(config.UID)}}, Operation: op}
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			retained := op.DeepCopy()
			switch mode {
			case "changed-native":
				s.changed = "auth-changed"
			case "changed-version":
				v := *want.PasswordVersion + 1
				want.PasswordVersion = &v
			case "caller-metadata-not-declared":
				want.Name = "other"
			}
			if mode == "late-owner" || mode == "late-source" {
				once := false
				s.onRead = func(*http.Request) {
					if once {
						return
					}
					once = true
					if mode == "late-owner" {
						fresh, e := a.currentController(ctx)
						if e != nil {
							t.Error(e)
							return
						}
						fresh.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if e = a.statusWriter.Status().Update(ctx, fresh); e != nil {
							t.Error(e)
						}
					} else {
						fresh := config.DeepCopy()
						fresh.Generation++
						if e := a.statusWriter.Update(ctx, fresh); e != nil {
							t.Error(e)
						}
					}
				}
			}
			switch mode {
			case "configmap-stale":
				cm.Data["model.yaml"] += " "
				if err := a.statusWriter.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
			case "configmap-forged-bytes":
				source.raw = append(source.raw, byte(' '))
			case "caller-spec-mutated":
				config.Spec.Source.Inline.Raw = append(config.Spec.Source.Inline.Raw, byte(' '))
			}
			if mode == "secret-snapshot-missing" {
				config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "lan_automation", Path: "/devices/0/isis_domain_password", Source: "isis"})
			}
			restarted := *a
			err = restarted.revalidateLANSessionStartOperation(ctx, config, source, want, op, phase)
			valid := mode == "prepared" || mode == "submitting" || mode == "configmap"
			if (err == nil) != valid {
				t.Fatal("wrong retained operation result", mode, err)
			}
			after := journalState(t, a).Operation
			if !reflect.DeepEqual(retained, after) || s.posts != 0 {
				t.Fatal("observation mutated journal/native state")
			}
		})
	}
}
