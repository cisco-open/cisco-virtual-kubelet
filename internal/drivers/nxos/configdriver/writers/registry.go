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
	"fmt"
	"sort"

	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
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

func GetForRelease(family, _ string) enginewriters.SectionWriter {
	return Get(family)
}

func Families() []string {
	out := make([]string, 0, len(registry))
	for family := range registry {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}
