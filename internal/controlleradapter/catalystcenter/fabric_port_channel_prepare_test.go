// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func portChannelJournalFixture(t *testing.T, mode string, post ...func(http.ResponseWriter, *http.Request)) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, []configurationDispatchFixtureMember, *bool) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	c, _, revised := portChannelDependencyFixture(t, mode, post...)
	a.client = c
	config.Spec.ManagedSections = []string{"fabric"}
	row := strings.Replace(portChannelTestRow, `"id":7`, `"id":7,"protocol":"LACP"`, 1)
	config.Spec.Source.Inline.Raw = portChannelTestModel("[" + row + "]")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	members := []configurationDispatchFixtureMember{}
	for i, name := range []string{"edge", "peer"} {
		serial := []string{"foc12345678", "foc87654321"}[i]
		uid, nodeUID := types.UID(name+"-uid"), types.UID(name+"-node-uid")
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: uid}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: fmt.Sprintf("198.51.100.%d", 101+i), PhysicalIdentity: serial}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(uid), NodeName: name, NodeUID: string(nodeUID), PhysicalIdentity: serial}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: nodeUID}}
		node.Status.NodeInfo.MachineID = serial
		for _, obj := range []ctrlclient.Object{device, node} {
			if err := a.statusWriter.Create(ctx, obj); err != nil {
				t.Fatal(err)
			}
		}
		members = append(members, configurationDispatchFixtureMember{device, node})
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, members, revised
}

func TestFabricPortChannelPrepare(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "undeclared", "lost-ack", "pruned", "preflight-source", "preflight-owner", "preflight-node", "preflight-group", "preflight-phase", "preflight-port"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if mode == "preflight-port" {
				nativeMode = "changed-port"
			}
			a, config, source, members, revised := portChannelJournalFixture(t, nativeMode)
			ctx := context.Background()
			id := int64(7)
			if mode == "undeclared" {
				id = 8
			}
			if mode == "lost-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			op, err := a.prepareFabricPortChannel(ctx, config, source, "edge", id)
			successful := mode == "valid" || mode == "existing" || strings.HasPrefix(mode, "preflight-")
			if (err == nil) != successful {
				t.Fatal("preparation outcome", err)
			}
			state := journalState(t, a)
			if mode == "existing" || mode == "undeclared" {
				if op != nil || state != nil && state.Operation != nil {
					t.Fatal("unowned compliance or undeclared action journaled")
				}
				return
			}
			if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.TaskID != "" || state.Operation.SubmittedAt != nil {
				t.Fatal("missing retained preparation")
			}
			if mode != "pruned" && (contract.ValidateGroupJournalIdentity(state.Operation) != nil || len(state.Operation.PhysicalGroup.Targets) != 2) {
				t.Fatal("incomplete physical footprint")
			}
			var rows ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &rows); err != nil || len(rows.Items) != 0 {
				t.Fatal("preparation created member reservations", err)
			}
			if !successful {
				return
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			checked := nc.Status.Configuration.Operation.DeepCopy()
			switch mode {
			case "preflight-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "preflight-owner":
				nc.Status.Configuration.Owners = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "preflight-node":
				if err := a.statusWriter.Delete(ctx, members[1].node); err != nil {
					t.Fatal(err)
				}
			case "preflight-group":
				checked.PhysicalGroup.Targets[0].Node.UID = "replacement"
			case "preflight-phase":
				checked.Phase = "Unknown"
			case "preflight-port":
				*revised = true
			}
			err = a.revalidateFabricPortChannelOperation(ctx, config, source, "edge", 7, checked, "Prepared")
			if (err == nil) != (mode == "valid") {
				t.Fatal("preflight outcome", err)
			}
			if mode == "valid" {
				nc.Status.Configuration.Operation.Phase = "Submitting"
				at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				nc.Status.Configuration.Operation.SubmittedAt = &at
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := a.revalidateFabricPortChannelOperation(ctx, config, source, "edge", 7, nc.Status.Configuration.Operation, "Submitting"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.prepareFabricPortChannel(ctx, config, source, "edge", 7); err == nil {
				t.Fatal("restarted prepare replaced retained operation")
			}
		})
	}
}
