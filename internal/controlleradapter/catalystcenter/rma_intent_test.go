// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func rmaModel(action string) string {
	return strings.Replace(wiredInventoryModel(), `"state":"PROVISION"`, `"state":"PROVISION","rma":`+action, 1)
}

func TestRMAInventoryModel(t *testing.T) {
	for _, action := range []string{rmaMark, rmaReplace} {
		raw := rmaModel(`{"action":"` + action + `","replacement_serial_number":"FOC87654321"}`)
		inventory, states, actions, err := parseRMAInventory([]byte(raw))
		if err != nil || len(inventory.devices) != 1 || states["edge"] != "PROVISION" || actions["edge"].action != action || actions["edge"].site != "Global/Lab/Building" || actions["edge"].replacementSerial != "FOC87654321" || inventory.devices[0].reference.Serial != "FOC12345678" {
			t.Fatal("RMA changed device identity/provisioning", inventory, states, actions, err)
		}
		// Public routing remains closed until ownership and recovery are integrated.
		if _, _, err := parseWiredInventoryIntent([]byte(raw)); err == nil {
			t.Fatal("ordinary provisioning silently discarded RMA")
		}
		if _, _, _, err := parseTemplateInventory([]byte(raw), []byte(raw), true); err == nil {
			t.Fatal("template path silently discarded RMA")
		}
	}
	mark := rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`)
	if _, _, _, err := parseRMAInventory([]byte(mark)); err != nil {
		t.Fatal(err)
	}
	// Marking can resolve a missing declared serial later; REPLACE requires both.
	if _, _, _, err := parseRMAInventory([]byte(strings.Replace(mark, `,"serial_number":"FOC12345678"`, "", 1))); err != nil {
		t.Fatal(err)
	}
}

func TestRMAInventoryRejectsPartialOrAmbiguousIntent(t *testing.T) {
	valid := rmaModel(`{"action":"REPLACE","replacement_serial_number":"FOC87654321"}`)
	bad := []string{
		wiredInventoryModel(),
		rmaModel(`null`), rmaModel(`{}`), rmaModel(`{"action":"NONE"}`),
		rmaModel(`{"action":"REPLACE"}`), rmaModel(`{"action":"REPLACE","replacement_serial_number":null}`),
		rmaModel(`{"action":"REPLACE","replacement_serial_number":"foc12345678"}`),
		rmaModel(`{"action":"REPLACE","replacement_serial_number":"FOC87654321,FOC00000000"}`),
		rmaModel(`{"action":"MARK_FOR_REPLACEMENT","unknown":true}`),
		strings.Replace(valid, `"PROVISION"`, `"REPROVISION"`, 1),
		strings.Replace(valid, `"PROVISION"`, `"ASSIGN"`, 1),
		strings.Replace(valid, `,"serial_number":"FOC12345678"`, "", 1),
		strings.Replace(valid, `"name":"edge"`, `"name":"edge","dayn_templates":{}`, 1),
		strings.Replace(valid, `"name":"edge"`, `"name":"edge","fabric_roles":["EDGE_NODE"]`, 1),
	}
	for i, raw := range bad {
		if _, _, _, err := parseRMAInventory([]byte(raw)); err == nil {
			t.Errorf("accepted invalid model %d", i)
		}
	}
	for _, mode := range []string{"duplicate-source", "replacement-is-device", "duplicate-replacement", "two-distinct"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(valid), &root); err != nil {
				t.Fatal(err)
			}
			inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
			rows := inventory["devices"].([]any)
			other := map[string]any{"name": "other", "serial_number": "FOC22222222", "site": "Global/Lab/Building", "state": "PROVISION"}
			switch mode {
			case "duplicate-source":
				other["serial_number"] = "foc12345678"
			case "replacement-is-device":
				other["serial_number"] = "foc87654321"
			case "duplicate-replacement":
				other["rma"] = map[string]any{"action": rmaReplace, "replacement_serial_number": "foc87654321"}
			case "two-distinct":
				other["rma"] = map[string]any{"action": rmaReplace, "replacement_serial_number": "FOC33333333"}
			}
			inventory["devices"] = append(rows, other)
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = parseRMAInventory(raw)
			if (err == nil) != (mode == "two-distinct") {
				t.Fatal("incorrect physical alias validation", err)
			}
		})
	}
}

func TestRMAWirePayloads(t *testing.T) {
	mark, err := deviceReplacementMarkPayload("faulty-id")
	if err != nil || string(mark) != `[{"faultyDeviceId":"faulty-id","replacementStatus":"MARKED-FOR-REPLACEMENT"}]` {
		t.Fatal(string(mark), err)
	}
	workflow, err := deviceReplacementWorkflowPayload("FOC12345678", "FOC87654321")
	if err != nil || string(workflow) != `{"faultyDeviceSerialNumber":"FOC12345678","replacementDeviceSerialNumber":"FOC87654321"}` {
		t.Fatal(string(workflow), err)
	}
	for _, id := range []string{"", "bad/id", "bad\n"} {
		if _, err := deviceReplacementMarkPayload(id); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	for _, pair := range [][2]string{{"", "new"}, {"old", ""}, {"FOC12345678", "foc12345678"}, {"old", "new,second"}, {"old", "new\n"}} {
		if _, err := deviceReplacementWorkflowPayload(pair[0], pair[1]); err == nil {
			t.Fatal("invalid identity pair accepted")
		}
	}
}
