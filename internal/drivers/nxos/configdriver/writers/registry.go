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
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

var registry = map[string]enginewriters.SectionWriter{}

func register(w enginewriters.SectionWriter) {
	if w == nil {
		panic("nxos writers: nil writer")
	}
	family := w.Family()
	if family == "" {
		panic("nxos writers: empty family")
	}
	if _, exists := registry[family]; exists {
		panic(fmt.Sprintf("nxos writers: duplicate family %q", family))
	}
	registry[family] = w
}

func Get(family string) enginewriters.SectionWriter {
	return registry[family]
}

func GetForRelease(family, release string) enginewriters.SectionWriter {
	w := Get(family)
	if w == nil {
		return nil
	}
	if err := nxosschema.ValidateDeviceVersion(release); err != nil {
		return versionErrorWriter{family: family, err: err}
	}
	return w
}

type versionErrorWriter struct {
	family string
	err    error
}

func (w versionErrorWriter) Family() string      { return w.family }
func (w versionErrorWriter) YANGPaths() []string { return nil }
func (w versionErrorWriter) Fetch(context.Context, transport.Interface) (any, error) {
	return nil, w.err
}
func (w versionErrorWriter) Diff(any, any) ([]transport.Op, error) { return nil, w.err }
func (w versionErrorWriter) Apply(context.Context, transport.Interface, []transport.Op) error {
	return w.err
}

func Families() []string {
	out := make([]string, 0, len(registry))
	for family := range registry {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}
