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
		lines := []string{"interface " + full}
		changed := gotItem == nil
		if descRaw, ok := item["description"]; ok {
			desc := strings.TrimSpace(stringLeaf(descRaw))
			if desc == "" {
				lines = append(lines, "no description")
			} else {
				lines = append(lines, "description "+desc)
			}
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
				lines = append(lines, "shutdown")
			} else {
				lines = append(lines, "no shutdown")
			}
			if gotItem == nil || !scalarEqual(shutdown, gotItem["shutdown"]) {
				changed = true
			}
		}
		if changed {
			ops = append(ops, cliOp(lines...))
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
	typ = strings.TrimSpace(stringLeaf(item["type"]))
	if typ == "" {
		typ = "Ethernet"
	}
	if !strings.EqualFold(typ, "Ethernet") {
		return "", "", "", fmt.Errorf("interface_ethernet: unsupported type %q", typ)
	}
	name = strings.TrimSpace(stringLeaf(item["name"]))
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
