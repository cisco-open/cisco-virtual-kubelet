// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func pnpIntentFixture() string {
	return `{"catalyst_center":{"inventory":{"devices":[{"name":"edge","state":"PNP","serial_number":"FOC12345678","site":"Global/Lab/Building","pid":"C9300-48P","image_name":"image.bin","image_skip":false,"onboarding_template":{"name":"bootstrap","variables":[{"name":"password","value":"fixture-secret"}]}}]}}}`
}

func TestPnPIntentAndPayloads(t *testing.T) {
	for _, stack := range []bool{false, true} {
		raw := pnpIntentFixture()
		if stack {
			raw = strings.Replace(raw, `"serial_number":"FOC12345678"`, `"serial_number":"FOC12345678,FOC87654321","stack":{"top_of_stack_serial_number":"FOC87654321","cabling_scheme":"1A"}`, 1)
		}
		wants, err := parsePnPInventory([]byte(raw))
		if err != nil || len(wants) != 1 {
			t.Fatal(err)
		}
		if strings.Contains(fmt.Sprintf("%v %#v", wants, wants), "fixture-secret") {
			t.Fatal("template value leaked")
		}
		body, err := pnpRegistrationPayload(wants[0])
		if err != nil {
			t.Fatal(err)
		}
		var registration map[string]map[string]any
		json.Unmarshal(body, &registration)
		info := registration["deviceInfo"]
		if info["serialNumber"] != "FOC12345678" || info["hostname"] != "edge" || info["pid"] != "C9300-48P" || (stack && info["stack"] != true) || (!stack && info["stack"] != nil) {
			t.Fatal("wrong registration mapping")
		}
		refs := pnpClaimReferences{"pnp-id", "site-id", "image-id", "template-id"}
		body, err = pnpClaimPayload(wants[0], refs)
		if err != nil {
			t.Fatal(err)
		}
		var claim map[string]any
		json.Unmarshal(body, &claim)
		typ := "Default"
		if stack {
			typ = "StackSwitch"
		}
		if claim["type"] != typ || claim["deviceId"] != "pnp-id" || claim["siteId"] != "site-id" || claim["hostname"] != "edge" {
			t.Fatal("wrong claim identity")
		}
		if claim["imageInfo"].(map[string]any)["skip"] != false || claim["imageInfo"].(map[string]any)["imageId"] != "image-id" {
			t.Fatal("wrong image mapping")
		}
		config := claim["configInfo"].(map[string]any)
		parameter := config["configParameters"].([]any)[0].(map[string]any)
		if config["configId"] != "template-id" || parameter["key"] != "password" || parameter["value"] != "fixture-secret" {
			t.Fatal("wrong template mapping")
		}
		if stack && (claim["topOfStackSerialNumber"] != "FOC87654321" || claim["cablingScheme"] != "1A") {
			t.Fatal("lost stack mapping")
		}
		refs.templateID = ""
		if _, err := pnpClaimPayload(wants[0], refs); err == nil {
			t.Fatal("unresolved template accepted")
		}
	}
}

func TestPnPIntentValidation(t *testing.T) {
	valid := pnpIntentFixture()
	if _, err := parsePnPInventory([]byte(strings.Replace(valid, `"bootstrap"`, `"Lab#bootstrap"`, 1))); err != nil {
		t.Fatal("qualified upstream template reference rejected", err)
	}
	for _, change := range [][2]string{
		{`"bootstrap"`, `"#bootstrap"`}, {`"bootstrap"`, `"Lab#"`},
		{`"bootstrap"`, `"Lab#Other#bootstrap"`}, {`"bootstrap"`, `"Lab# bootstrap"`},
		{`"PNP"`, `"PROVISION"`}, {`"FOC12345678"`, `"FOC12345678,foc12345678"`},
		{`"FOC12345678"`, `"FOC12345678,"`}, {`"FOC12345678"`, `"FOC12345678, FOC87654321"`},
		{`"pid":"C9300-48P"`, `"pid":""`}, {`"site":"Global/Lab/Building"`, `"site":"Global"`},
		{`"pid":"C9300-48P"`, `"pid":"C9300-48P","type":"Unknown"`},
		{`"pid":"C9300-48P"`, `"pid":"C9300-48P","stack":{"cabling_scheme":"1A"}`},
		{`"image_skip":false`, `"image_skip":"false"`},
		{`"image_skip":false`, `"image_skip":false,"rma":{"action":"REPLACE"}`},
		{`"name":"password","value":"fixture-secret"`, `"name":"password","value":"fixture-secret","unexpected":true`},
	} {
		if _, err := parsePnPInventory([]byte(strings.Replace(valid, change[0], change[1], 1))); err == nil {
			t.Errorf("accepted invalid %s", change[0])
		}
	}
	stack := strings.Replace(valid, `"serial_number":"FOC12345678"`, `"serial_number":"FOC12345678,FOC87654321","stack":{"top_of_stack_serial_number":"UNDECLARED","cabling_scheme":"1A"}`, 1)
	if _, err := parsePnPInventory([]byte(stack)); err == nil {
		t.Fatal("unknown top of stack accepted")
	}
	if _, err := parseInventoryIntent([]byte(valid)); err == nil {
		t.Fatal("ordinary inventory silently accepted PnP")
	}
}

func TestPnPClaimDefaultsAndStackEmptyReferences(t *testing.T) {
	for _, typ := range []string{"Default", "StackSwitch", "AccessPoint"} {
		want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
		if typ == "StackSwitch" {
			want.Serial += ",FOC87654321"
		}
		if typ == "AccessPoint" {
			want.Type = typ
			want.RFProfile = "rf-profile"
		}
		body, err := pnpClaimPayload(want, pnpClaimReferences{deviceID: "device", siteID: "site"})
		if err != nil {
			t.Fatal(err)
		}
		var claim map[string]any
		json.Unmarshal(body, &claim)
		image := claim["imageInfo"].(map[string]any)
		config := claim["configInfo"].(map[string]any)
		if claim["type"] != typ || image["skip"] != true || len(config["configParameters"].([]any)) != 0 {
			t.Fatal("wrong pinned defaults")
		}
		if typ == "StackSwitch" && (image["imageId"] != "" || config["configId"] != "") {
			t.Fatal("missing stack empty reference mapping")
		}
		if typ != "StackSwitch" && (image["imageId"] != nil || config["configId"] != nil) {
			t.Fatal("invented reference")
		}
		if typ == "AccessPoint" && claim["rfProfile"] != "rf-profile" {
			t.Fatal("missing RF profile")
		}
		if _, err := pnpClaimPayload(want, pnpClaimReferences{deviceID: "device", siteID: "site", imageID: "unrequested"}); err == nil {
			t.Fatal("injected image accepted")
		}
	}
}
