// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
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

type sdaAliasInputMutationClient struct {
	ctrlclient.Client
	change func()
}

func (c *sdaAliasInputMutationClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	err := c.Client.Get(ctx, key, obj, opts...)
	if _, ok := obj.(*configapi.NetworkControllerConfig); err == nil && ok && c.change != nil {
		change := c.change
		c.change = nil
		change()
	}
	return err
}

func sdaTransitAliasModel(devices []Device) (map[string]any, []map[string]any) {
	rows := []map[string]any{}
	names := []string{"CONTROL0", "CONTROL1"}
	for i, d := range devices {
		rows = append(rows, map[string]any{"name": names[i], "fqdn_name": d.Hostname, "device_ip": d.ManagementIP, "serial_number": d.Serial, "site": "Global/Lab/Building", "state": "PROVISION"})
	}
	return map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": rows}, "fabric": map[string]any{"transits": []any{map[string]any{"name": "Transit", "type": "SDA_LISP_PUB_SUB_TRANSIT", "control_plane_devices": names, "multicast_over_sda_transit": true}}}}}, rows
}

func TestSDATransitInventoryAlias(t *testing.T) {
	for _, mode := range []string{"alias", "no-anchors", "direct-name", "role", "wrong-site", "wrong-ip", "wrong-serial", "wrong-role", "ambiguous-name", "physical-only", "duplicate-physical", "assign-state", "reprovision-state", "missing-row", "duplicate-name", "unknown-field", "extra-collection", "malformed-later", "frozen-map", "late-hostname"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, _, f, api := newSDATransitFixture(t)
			f.devices[0].Hostname, f.devices[1].Hostname = "edge0.lab.example", "edge1.lab.example"
			root, rows := sdaTransitAliasModel(f.devices)
			inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
			parseFailure := false
			switch mode {
			case "no-anchors":
				for _, row := range rows {
					delete(row, "serial_number")
					delete(row, "device_ip")
				}
			case "direct-name":
				f.devices[0].Hostname = "CONTROL0"
				f.devices[1].Hostname = "CONTROL1"
				for _, row := range rows {
					delete(row, "fqdn_name")
				}
			case "role", "wrong-role":
				for i, row := range rows {
					row["device_role"], row["role_source"] = "ACCESS", "MANUAL"
					if mode == "role" {
						f.devices[i].Role, f.devices[i].RoleSource = "ACCESS", "MANUAL"
					}
				}
			case "wrong-site":
				rows[0]["site"] = "Global/Lab"
			case "wrong-ip":
				rows[0]["device_ip"] = "198.51.100.200"
			case "wrong-serial":
				rows[0]["serial_number"] = "FOC99999999"
			case "ambiguous-name":
				d := f.devices[0]
				d.ID, d.Hostname, d.Serial, d.ManagementIP = "other", "CONTROL0", "FOC99999999", "198.51.100.200"
				f.devices = append(f.devices, d)
			case "physical-only":
				delete(rows[0], "fqdn_name")
			case "duplicate-physical":
				for _, key := range []string{"fqdn_name", "serial_number", "device_ip"} {
					rows[1][key] = rows[0][key]
				}
			case "assign-state":
				rows[0]["state"] = "ASSIGN"
				parseFailure = true
			case "reprovision-state":
				rows[0]["state"] = "REPROVISION"
				parseFailure = true
			case "missing-row":
				inventory["devices"] = rows[:1]
				parseFailure = true
			case "duplicate-name":
				rows[1]["name"] = rows[0]["name"]
				parseFailure = true
			case "unknown-field":
				rows[0]["ignored"] = true
				parseFailure = true
			case "extra-collection":
				inventory["tags"] = []any{}
				parseFailure = true
			case "malformed-later":
				inventory["devices"] = append(rows, map[string]any{"name": "later", "site": "Global/Lab/Building", "state": "ASSIGN", "unsupported": "value"})
				parseFailure = true
			}
			raw, _ := json.Marshal(root)
			original := append([]byte(nil), raw...)
			wants, err := parseSDATransitIntents(raw)
			if parseFailure {
				if err == nil || f.posts != 0 {
					t.Fatal("invalid dependency model accepted", err)
				}
				return
			}
			if err != nil || len(wants) != 1 || len(wants[0].references) != 2 {
				t.Fatal("alias model did not compile", err)
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("dependency projection rewrote source bytes")
			}
			want := wants[0]
			frozen := freezeSDATransitIntent(want)
			write, targets, err := c.planManagedSDATransitCreate(ctx, api, "lab", want, func(context.Context, []Device) error { return nil })
			positive := mode == "alias" || mode == "no-anchors" || mode == "direct-name" || mode == "role" || mode == "frozen-map" || mode == "late-hostname"
			if !positive {
				if err == nil || write != nil || targets != nil || f.posts != 0 {
					t.Fatal("invalid alias/prerequisite admitted", err)
				}
				return
			}
			if err != nil || write == nil || len(targets) != 2 {
				t.Fatal("managed alias stage failed", err)
			}
			if mode == "frozen-map" {
				ref := want.references["CONTROL0"]
				ref.selector.FQDN = "changed"
				want.references["CONTROL0"] = ref
			}
			if mode == "late-hostname" {
				f.devices[0].Hostname = "renamed.lab.example"
			}
			task, err := write.submit(ctx)
			if mode == "late-hostname" {
				if err == nil || f.posts != 0 {
					t.Fatal("late native alias replacement submitted")
				}
				return
			}
			if err != nil || f.posts != 1 {
				t.Fatal("alias create failed", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: write.resource, TargetID: write.identity.target, Phase: "Verifying", TaskID: task.ID, SubmittedAt: &metav1.Time{Time: time.UnixMilli(f.stamp).Add(-time.Millisecond)}}
			if ok, err := c.observeSDATransitCreate(ctx, frozen, op, time.Now().Add(time.Second)); err != nil || !ok {
				t.Fatal("alias task/readback did not recover", err)
			}
		})
	}
}

func TestSDATransitAliasJournalAndPublicRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "forged-reference", "source-change", "input-freeze"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, f, physical := sdaTransitJournalFixture(t)
			f.devices[0].Hostname, f.devices[1].Hostname = "edge0.lab.example", "edge1.lab.example"
			root, _ := sdaTransitAliasModel(f.devices)
			raw, _ := json.Marshal(root)
			config.Spec.Source.Inline.Raw = raw
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			wants, err := parseSDATransitIntents(raw)
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			original := freezeSDATransitIntent(want)
			if mode == "input-freeze" {
				a.statusReader = &sdaAliasInputMutationClient{Client: a.statusWriter, change: func() {
					ref := want.references["CONTROL0"]
					ref.selector.FQDN = "caller-changed.lab.example"
					want.references["CONTROL0"] = ref
				}}
			}
			if mode == "forged-reference" {
				ref := want.references["CONTROL0"]
				ref.selector.Serial = "FOC99999999"
				want.references["CONTROL0"] = ref
			}
			op, err := a.prepareSDATransit(ctx, config, nil, want)
			if mode == "forged-reference" {
				if err == nil || op != nil || f.posts != 0 {
					t.Fatal("forged reference escaped declaration binding")
				}
				return
			}
			if err != nil || contract.ValidateGroupJournalIdentity(op) != nil {
				t.Fatal("alias preparation failed", err)
			}
			if mode == "input-freeze" {
				if want.references["CONTROL0"].selector.FQDN == original.references["CONTROL0"].selector.FQDN {
					t.Fatal("input mutation hook did not run")
				}
				snapshot, err := a.client.sdaTransitSnapshot(ctx, original)
				if err != nil || op.Resource != sdaTransitResource(snapshot) || f.posts != 0 {
					t.Fatal("caller mutation changed prepared native identity", err)
				}
				if err := a.revalidateSDATransitOperation(ctx, config, nil, original, op, "Prepared"); err != nil {
					t.Fatal(err)
				}
				if err := a.revalidateSDATransitOperation(ctx, config, nil, want, op, "Prepared"); err == nil {
					t.Fatal("forged caller reference inherited prepared identity")
				}
				return
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, physical)
			if mode == "source-change" {
				var changed map[string]any
				json.Unmarshal(raw, &changed)
				row := changed["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
				row["fqdn_name"] = "other.lab.example"
				config.Spec.Source.Inline.Raw, _ = json.Marshal(changed)
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			_, err = a.dispatchSDATransit(ctx, config, nil, want, permit)
			if mode == "source-change" {
				if err == nil || f.posts != 0 || journalState(t, a).Operation.Phase != "Prepared" {
					t.Fatal("alias source change submitted", err)
				}
				return
			}
			if err != nil || f.posts != 1 {
				t.Fatal(err)
			}
			if mode == "unknown" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			a.configurationAdmission = &configurationAdmission{protocol: contract.GroupReservationProtocol, leaseNamespace: "leases", authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
			for i := 0; i < 2; i++ {
				nc, _ := a.currentController(ctx)
				status := config.Status.DeepCopy()
				_, pending, _, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
				if !pending || journalState(t, a).Operation.Phase != "Verifying" || f.posts != 1 {
					t.Fatal("alias public recovery failed")
				}
			}
			if len(journalState(t, a).ActionApplications) != 0 {
				t.Fatal("alias mapping published completion")
			}
			var nodes corev1.NodeList
			if err := a.statusReader.List(ctx, &nodes); err != nil {
				t.Fatal(err)
			}
			for _, target := range journalState(t, a).Operation.PhysicalGroup.Targets {
				if target.Device.Name == "CONTROL0" || target.Device.Name == "CONTROL1" {
					t.Fatal("model alias replaced Kubernetes identity")
				}
			}
			if !reflect.DeepEqual(want.references, wants[0].references) {
				t.Fatal("recovery mutated source references")
			}
		})
	}
}
