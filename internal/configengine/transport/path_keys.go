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

package transport

import (
	"strings"
	"sync"
)

var (
	pathKeyRegistryMu        sync.RWMutex
	pathKeyRegistry          = map[string]string{}
	pathKeyRegistryComposite = map[string][]string{}
)

// RegisterPathKey registers the YANG list-key field name(s) for the last
// segment of a path. Transport implementations that parse string paths can
// consult PathKeyForSegment before falling back to heuristics.
func RegisterPathKey(segment string, keyFields ...string) {
	if len(keyFields) == 0 || segment == "" {
		return
	}
	pathKeyRegistryMu.Lock()
	defer pathKeyRegistryMu.Unlock()
	pathKeyRegistry[segment] = keyFields[0]
	if len(keyFields) > 1 {
		pathKeyRegistryComposite[segment] = append([]string(nil), keyFields...)
	}
}

// PathKeyForSegment returns the first registered key field for the path
// segment, or an empty string when no schema metadata is registered.
func PathKeyForSegment(segment string) string {
	pathKeyRegistryMu.RLock()
	defer pathKeyRegistryMu.RUnlock()
	return pathKeyRegistry[segment]
}

// LastPathSegment extracts the last "/"-separated segment of a YANG xpath,
// with any module prefix stripped. Schema loaders use it to compute the
// registry key from a full family path.
func LastPathSegment(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	if i := strings.Index(last, ":"); i > 0 {
		last = last[i+1:]
	}
	return last
}
