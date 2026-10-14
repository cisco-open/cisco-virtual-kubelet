// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestAdaptiveQoSAttachmentVariable(t *testing.T) {
	property := "//vpn/10/GigabitEthernet1/interface/qos-adaptive/downstream/bandwidth-down"
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/dataservice/template/device/config/input":
			fmt.Fprintf(w, `{"header":{"columns":[{"property":%q,"title":"Default Downstream (Kbps)","editable":true}]},"data":[{"csv-deviceId":"router1",%q:"1000"}]}`, property, property)
		case "/dataservice/template/device/object/template1":
			fmt.Fprint(w, `{"generalTemplates":[{"templateId":"vpn1","templateType":"cisco_vpn","subTemplates":[{"templateId":"ethernet1","templateType":"cisco_vpn_interface","subTemplates":[]}]}]}`)
		case "/dataservice/template/feature/object/ethernet1":
			fmt.Fprint(w, `{"templateDefinition":{"if-name":{"vipValue":"GigabitEthernet1"},"qos-adaptive":{"downstream":{"bandwidth-down":{"vipVariableName":"cvkDownstream"}}}}}`)
		default:
			t.Errorf("unexpected adaptive QoS path %s", request.URL.Path)
		}
	})
	row, properties, err := c.attachmentInput(context.Background(), "template1", "router1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(properties["cvkDownstream"]) != 1 || properties["cvkDownstream"][0] != property || len(properties["Kbps"]) != 0 {
		t.Fatal("authoritative adaptive QoS variable mapping missing")
	}
	values, err := attachmentValues(row, properties, map[string]string{"cvkDownstream": "1000"})
	if err != nil || values["cvkDownstream"] != "1000" {
		t.Fatal("adaptive QoS readback failed", err)
	}
}
