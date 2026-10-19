// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"
)

var wirelessAPDispatchCase = definitionRecoveryCase{"ap_profiles", `{"catalyst_center":{"wireless":{"ap_profiles":[{"name":"CVK_LAB","client_limit":20}]}}}`, wirelessAPPath, "ap-profile:CVK_LAB", "20", "40"}

func TestWirelessAPNoDispatchReplan(t *testing.T) {
	for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
		t.Run(mode, func(t *testing.T) { exerciseWirelessNoDispatchReplan(t, wirelessAPDispatchCase, mode, nil) })
	}
}

func TestWirelessAPDispatchSnapshots(t *testing.T) {
	for _, mode := range []string{"stable", "create-appeared", "replaced", "missing", "native-edit", "desired-edit", "version-edit", "unrelated", "power-replaced", "power-edited", "power-missing", "power-read-error", "failed-read", "lost-ack", "planned-power-missing", "planned-power-appeared", "retained-power-replaced", "retained-calendar-power-replaced"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			desired, err := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPDispatchCase.model, `"client_limit":20`, `"client_limit":40,"power_profile":"POWER"`, 1)))
			if err != nil {
				t.Fatal(err)
			}
			native := cloneWirelessAPFields(desired[0].metadata)
			native["id"] = "native-id"
			native["clientLimit"] = 20
			native["description"] = "preserve"
			if strings.HasPrefix(mode, "retained-") {
				delete(desired[0].metadata, "apPowerProfileName")
				if mode == "retained-calendar-power-replaced" {
					delete(native, "apPowerProfileName")
					calendar, err := parseWirelessAPCalendar([]any{map[string]any{"power_profile": "POWER", "scheduler_type": "DAILY", "scheduler_start_time": "22:00", "scheduler_end_time": "23:00"}})
					if err != nil {
						t.Fatal(err)
					}
					native["calendarPowerProfiles"] = calendar
				}
			}
			rows := []map[string]any{native}
			if mode == "create-appeared" {
				rows = []map[string]any{}
			}
			powers := []wirelessPowerProfile{{ID: "power-id", Name: "POWER", Rules: []wirelessPowerRule{{InterfaceType: "ETHERNET", InterfaceID: "GIGABITETHERNET0", ParameterType: "SPEED", ParameterValue: "1000MBPS"}}}}
			savedPower := powers[0]
			plannedOnly := strings.HasPrefix(mode, "planned-power-")
			if plannedOnly {
				powers = []wirelessPowerProfile{}
			}
			writes := 0
			failRead, failPower := false, false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessAPPath && r.Method == "GET":
					if failRead {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case r.URL.Path == wirelessPowerPath && r.Method == "GET":
					if failPower {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": powers})
				case r.Method == "POST" || r.Method == "PUT":
					writes++
					if mode == "lost-ack" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
					}
				default:
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessAPExecutor{client: c, desired: desired}
			var write *configurationWrite
			if plannedOnly {
				write, err = e.plan(ctx, map[string]bool{"POWER": true})
			} else {
				write, err = e.next(ctx)
			}
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			switch mode {
			case "create-appeared":
				rows = []map[string]any{native}
			case "replaced":
				native["id"] = "replacement"
			case "missing":
				rows = []map[string]any{}
			case "native-edit":
				native["description"] = "concurrent preserved edit"
			case "desired-edit":
				e.desired[0].metadata["clientLimit"] = int64(60)
			case "version-edit":
				e.desired[0].credentials = append(e.desired[0].credentials, configurationCredentialIntent{path: "/ap_profiles/0/management_password", field: "managementSetting.managementPassword", version: 1})
			case "unrelated":
				other := cloneWirelessAPFields(native)
				other["id"] = "unrelated"
				other["apProfileName"] = "OTHER"
				rows = append(rows, other)
			case "power-replaced", "retained-power-replaced", "retained-calendar-power-replaced":
				powers[0].ID = "replacement"
			case "power-edited":
				powers[0].Rules[0].ParameterValue = "100MBPS"
			case "power-missing":
				powers = []wirelessPowerProfile{}
			case "planned-power-appeared":
				powers = []wirelessPowerProfile{savedPower}
			case "power-read-error":
				failPower = true
			case "failed-read":
				failRead = true
			}
			_, err = write.submit(ctx)
			sent := mode == "stable" || mode == "unrelated" || mode == "lost-ack"
			if sent {
				if writes != 1 || (err == nil) == (mode == "lost-ack") {
					t.Fatal("native dispatch outcome", writes, err)
				}
			} else if err == nil || writes != 0 || write.noDispatch(err) == nil {
				t.Fatal("stale plan not certified before native dispatch", writes, err)
			}
			if _, err = write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("one-use plan replayed", err)
			}
			if writes > 1 {
				t.Fatal("mutation repeated")
			}
		})
	}
}

func TestWirelessAPMetadataRecoverySelection(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		raw := wirelessAPDispatchCase.model
		if mixed {
			raw = strings.Replace(raw, `"wireless":{`, `"wireless":{"interfaces":[{"name":"OTHER","vlan_id":3990}],`, 1)
		}
		e, err := (&client{}).compileWirelessConfiguration([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		selectTask := wirelessDefinitionKnownTaskRecovery(e)
		if selectTask == nil {
			t.Fatal("AP missing recovery selector")
		}
		now := metav1.NewTime(time.Now().Add(-time.Second))
		original := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "wireless", Resource: "ap-profile:CVK_LAB", Action: "Update", Phase: "Unknown", TaskID: "task-id", TargetID: "native-id", SubmittedAt: &now}
		for _, mode := range []string{"update", "create", "taskless", "credential", "unselected", "group-prefix", "physical", "replaced-section"} {
			op := original.DeepCopy()
			want := mode == "update" || mode == "create"
			switch mode {
			case "create":
				op.Action = "Create"
				op.TargetID = ""
			case "taskless":
				op.TaskID = ""
			case "credential":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Path: "management_password", Version: 1}}
			case "unselected":
				op.Resource = "ap-profile:OTHER"
			case "group-prefix":
				op.Resource = "ap_profiles/" + op.Resource
			case "physical":
				op.TargetSerial = "SERIAL"
			case "replaced-section":
				op.Section = "inventory"
			}
			if selectTask(op) != want {
				t.Fatal("recovery scope broadened", mixed, mode)
			}
		}
	}
}

func TestWirelessAPMetadataRecoveryPublic(t *testing.T) {
	for _, mode := range []string{"known", "taskless", "foreign-task", "changed-source", "replaced-target", "missing-target", "pending-task", "failed-task", "child-failed", "child-pending", "child-foreign", "tree-unavailable", "unknown-create", "create-taskless", "create-changed-source", "create-child-failed", "create-child-pending", "create-tree-unavailable"} {
		t.Run(mode, func(t *testing.T) { exerciseWirelessDefinitionRecoveryPublic(t, wirelessAPDispatchCase, mode, nil) })
	}
}
