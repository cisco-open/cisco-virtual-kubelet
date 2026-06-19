// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package writers

import (
	"context"
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

type ethernetWriter struct{}

func init() { register(ethernetWriter{}) }

func (ethernetWriter) Family() string { return nxosschema.FamilyInterfaceEthernet }

func (ethernetWriter) YANGPaths() []string { return []string{nxosschema.PathInterfaceEthernet} }

func (ethernetWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, nxosschema.PathInterfaceEthernet)
	if err != nil {
		return nil, err
	}
	return decodeMap(raw, "interface_ethernet")
}

func (ethernetWriter) Diff(desired, observed any) ([]transport.Op, error) {
	wantList, err := coerceList(desired, "interfaces", "interface_ethernet.desired")
	if err != nil {
		return nil, err
	}
	gotList, err := coerceList(observed, "interfaces", "interface_ethernet.observed")
	if err != nil {
		return nil, err
	}
	got := map[string]map[string]any{}
	for _, item := range gotList {
		_, _, full, err := ethernetName(item)
		if err == nil {
			got[full] = item
		}
	}
	desiredByName := map[string]map[string]any{}
	for _, item := range wantList {
		if err := rejectUnsupportedKeys(item, "interface_ethernet.interfaces[]",
			"id", "type", "name", "description", "shutdown", "mtu"); err != nil {
			return nil, err
		}
		_, _, full, err := ethernetName(item)
		if err != nil {
			return nil, err
		}
		desiredByName[full] = item
	}
	var ops []transport.Op
	for _, full := range sortedKeys(desiredByName) {
		item := desiredByName[full]
		gotItem := got[full]
		_, name, _, _ := ethernetName(item)
		attrs := map[string]string{"id": "eth" + name}
		changed := gotItem == nil
		if descRaw, ok := item["description"]; ok {
			desc := strings.TrimSpace(stringLeaf(descRaw))
			attrs["descr"] = desc
			if gotItem == nil || !scalarEqual(desc, gotItem["description"]) {
				changed = true
			}
		}
		if shutdownRaw, ok := item["shutdown"]; ok {
			shutdown, valid := boolLeaf(shutdownRaw)
			if !valid {
				return nil, fmt.Errorf("%s shutdown must be boolean", full)
			}
			if shutdown {
				attrs["adminSt"] = "down"
			} else {
				attrs["adminSt"] = "up"
			}
			attrs["userCfgdFlags"] = "admin_state"
			if gotItem == nil || !scalarEqual(shutdown, gotItem["shutdown"]) {
				changed = true
			}
		}
		if mtuRaw, ok := item["mtu"]; ok {
			mtu, valid := intLeaf(mtuRaw)
			if !valid || mtu < 576 || mtu > 9216 {
				return nil, fmt.Errorf("%s mtu must be an integer between 576 and 9216", full)
			}
			attrs["mtu"] = fmt.Sprintf("%d", mtu)
			if gotItem == nil || !scalarEqual(mtu, gotItem["mtu"]) {
				changed = true
			}
		}
		if changed {
			op, err := dmeMergeOp(nxosschema.DNInterfaceEntity, dmeObject("interfaceEntity", nil,
				dmeObject("l1PhysIf", attrs),
			))
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func (ethernetWriter) KeysOf(v any) []string {
	list, err := coerceList(v, "interfaces", "interface_ethernet.keys")
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, item := range list {
		_, _, full, err := ethernetName(item)
		if err == nil {
			keys = append(keys, full)
		}
	}
	return keys
}

func (ethernetWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func ethernetName(item map[string]any) (typ, name, full string, err error) {
	if raw, ok := item["type"]; ok {
		typ = strings.TrimSpace(stringLeaf(raw))
	}
	if typ == "" {
		typ = "Ethernet"
	}
	if !strings.EqualFold(typ, "Ethernet") {
		return "", "", "", fmt.Errorf("interface_ethernet: unsupported type %q", typ)
	}
	if raw, ok := item["name"]; ok {
		name = strings.TrimSpace(stringLeaf(raw))
	}
	if name == "" {
		if raw, ok := item["id"]; ok {
			name = strings.TrimSpace(stringLeaf(raw))
		}
	}
	if name == "" {
		return "", "", "", fmt.Errorf("interface_ethernet: missing name")
	}
	if strings.HasPrefix(strings.ToLower(name), "ethernet") {
		full = "Ethernet" + strings.TrimSpace(name[len("Ethernet"):])
		name = strings.TrimSpace(name[len("Ethernet"):])
	} else {
		full = "Ethernet" + name
	}
	if name == "" {
		return "", "", "", fmt.Errorf("interface_ethernet: missing numeric name")
	}
	return "Ethernet", name, full, nil
}
