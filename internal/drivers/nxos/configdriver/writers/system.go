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

type systemWriter struct{}

func init() { register(systemWriter{}) }

func (systemWriter) Family() string { return nxosschema.FamilySystem }

func (systemWriter) YANGPaths() []string { return []string{nxosschema.PathSystemHostname} }

func (systemWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, nxosschema.PathSystemHostname)
	if err != nil {
		return nil, err
	}
	return decodeMap(raw, "system")
}

func (systemWriter) Diff(desired, observed any) ([]transport.Op, error) {
	want, err := coerceMap(desired, "system.desired")
	if err != nil || want == nil {
		return nil, err
	}
	got, err := coerceMap(observed, "system.observed")
	if err != nil {
		return nil, err
	}
	if got == nil {
		got = map[string]any{}
	}
	if err := rejectUnsupportedKeys(want, "system", "hostname", "mtu"); err != nil {
		return nil, err
	}

	attrs := map[string]string{}
	if hostname, ok := want["hostname"]; ok {
		name := strings.TrimSpace(stringLeaf(hostname))
		if name == "" {
			return nil, fmt.Errorf("system.hostname must not be empty")
		}
		if !scalarEqual(name, got["hostname"]) {
			attrs["name"] = name
		}
	}

	var children []map[string]any
	if mtuRaw, ok := want["mtu"]; ok {
		mtu, valid := intLeaf(mtuRaw)
		if !valid || mtu < 576 || mtu > 9216 {
			return nil, fmt.Errorf("system.mtu must be an integer between 576 and 9216")
		}
		if !scalarEqual(mtu, got["mtu"]) {
			children = append(children, dmeObject("ethpmEntity", nil,
				dmeObject("ethpmInst", map[string]string{
					"systemJumboMtu": fmt.Sprintf("%d", mtu),
				}),
			))
		}
	}

	if len(attrs) == 0 && len(children) == 0 {
		return nil, nil
	}
	op, err := dmeMergeOp(nxosschema.DNSystem, dmeObject("topSystem", attrs, children...))
	if err != nil {
		return nil, err
	}
	return []transport.Op{op}, nil
}

func (systemWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}
