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

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

type vlanWriter struct{}

func init() { register(vlanWriter{}) }

func (vlanWriter) Family() string { return nxosschema.FamilyVLAN }

func (vlanWriter) YANGPaths() []string { return []string{nxosschema.PathVLANBrief} }

func (vlanWriter) OperationScope() enginewriters.OperationScope {
	return enginewriters.OperationScope{
		ReadPaths:     []string{nxosschema.PathVLANBrief},
		WritePrefixes: []string{nxosschema.DNBridgeDomain},
	}
}

func (vlanWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, nxosschema.PathVLANBrief)
	if err != nil {
		return nil, err
	}
	return decodeMap(raw, "vlan")
}

func (vlanWriter) Diff(desired, observed any) ([]transport.Op, error) {
	wantList, err := coerceList(desired, "vlans", "vlan.desired")
	if err != nil {
		return nil, err
	}
	gotList, err := coerceList(observed, "vlans", "vlan.observed")
	if err != nil {
		return nil, err
	}
	got := map[int]map[string]any{}
	for _, item := range gotList {
		if id, ok := intLeaf(item["id"]); ok {
			got[id] = item
		}
	}
	desiredByID := map[string]map[string]any{}
	for _, item := range wantList {
		if err := rejectUnsupportedKeys(item, "vlan.vlans[]", "id", "name"); err != nil {
			return nil, err
		}
		id, ok := intLeaf(item["id"])
		if !ok || id < 1 || id > 4094 {
			return nil, fmt.Errorf("vlan id %v is invalid", item["id"])
		}
		desiredByID[fmt.Sprintf("%04d", id)] = item
	}
	var ops []transport.Op
	for _, key := range sortedKeys(desiredByID) {
		item := desiredByID[key]
		id, _ := intLeaf(item["id"])
		gotItem := got[id]
		changed := gotItem == nil
		attrs := map[string]string{
			"fabEncap": fmt.Sprintf("vlan-%d", id),
		}
		if nameRaw, ok := item["name"]; ok {
			name := stringLeaf(nameRaw)
			if name == "" {
				return nil, fmt.Errorf("vlan %d name must not be empty", id)
			}
			if gotItem == nil || !scalarEqual(name, gotItem["name"]) {
				attrs["name"] = name
				changed = true
			}
		}
		if changed {
			op, err := dmeMergeOp(nxosschema.DNBridgeDomain, dmeObject("bdEntity", nil,
				dmeObject("l2BD", attrs),
			))
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func (vlanWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	wantList, err := coerceList(desired, "vlans", "vlan.prune.desired")
	if err != nil {
		return nil, err
	}
	gotList, err := coerceList(observed, "vlans", "vlan.prune.observed")
	if err != nil {
		return nil, err
	}
	desiredIDs := map[int]struct{}{}
	for _, item := range wantList {
		id, ok := intLeaf(item["id"])
		if !ok || id < 1 || id > 4094 {
			return nil, fmt.Errorf("vlan prune desired id %v is invalid", item["id"])
		}
		desiredIDs[id] = struct{}{}
	}
	observedIDs := map[int]struct{}{}
	for _, item := range gotList {
		id, ok := intLeaf(item["id"])
		if !ok {
			continue
		}
		observedIDs[id] = struct{}{}
	}
	var ops []transport.Op
	for _, id := range sortedIntKeys(observedIDs) {
		if _, wanted := desiredIDs[id]; wanted {
			continue
		}
		if id == 1 {
			return nil, fmt.Errorf("vlan 1 cannot be pruned through NXOSConfig")
		}
		ops = append(ops, dmeDeleteOp(vlanDMEPath(id)))
	}
	return ops, nil
}

func (vlanWriter) KeysOf(v any) []string {
	list, err := coerceList(v, "vlans", "vlan.keys")
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, item := range list {
		if id, ok := intLeaf(item["id"]); ok {
			keys = append(keys, fmt.Sprintf("%d", id))
		}
	}
	return keys
}

func (vlanWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func vlanDMEPath(id int) string {
	return fmt.Sprintf("%s/bd-[vlan-%d]", nxosschema.DNBridgeDomain, id)
}

func sortedIntKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
