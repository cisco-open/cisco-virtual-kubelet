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
	"sync"
)

// registry is a process-global lookup of family → writer. Writers
// self-register from their file's init() so adding a family is a one-file
// change with no central registration site to keep in sync.
//
// The map is guarded by mu because init() order across files in the same
// package is unspecified — the mutex cost is a once-per-boot.
var (
	mu       sync.RWMutex
	registry = map[string]SectionWriter{}
)

// Register adds w to the process-global registry. It panics on duplicate
// registration because that is a programming error (two files claiming the
// same family name); letting it pass would silently shadow one of them.
func Register(w SectionWriter) {
	if w == nil {
		panic("writers.Register: nil SectionWriter")
	}
	name := w.Family()
	if name == "" {
		panic("writers.Register: SectionWriter.Family() returned empty string")
	}

	mu.Lock()
	defer mu.Unlock()

	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("writers.Register: duplicate family %q", name))
	}
	registry[name] = w
}

// Get returns the writer registered for family or nil when none is
// registered. A nil return is not an error — it indicates a family the
// driver has not yet been taught; the caller reports it as Unsupported
// in the family status.
func Get(family string) SectionWriter {
	mu.RLock()
	defer mu.RUnlock()
	return registry[family]
}

// Families returns a sorted snapshot of registered family names. The
// slice is owned by the caller.
func Families() []string {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Len reports the number of registered writers; intended for tests and
// startup logging.
func Len() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(registry)
}
