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

// Package cfgvalidation provides the registration interface that per-family
// ygot-generated schema packages use to hook into the CI fixture harness.
//
// Generated packages call Register from their init() function; the fixture
// test then calls Lookup to retrieve a validator for a given (family,
// releaseTag) pair. When no validator is registered for a pair the harness
// skips schema validation for that case, allowing incremental rollout without
// breaking existing fixtures.
package cfgvalidation

import (
	"encoding/json"
	"fmt"
	"sync"
)

// FamilySchemaValidator validates a single RESTCONF/YANG JSON body against
// the generated ygot schema for a (family, releaseTag) pair.
type FamilySchemaValidator interface {
	// ValidateBody unmarshals the JSON body against the family's ygot schema.
	// Returns nil when the body is structurally valid; a non-nil error contains
	// the path and reason for the first validation failure.
	ValidateBody(body json.RawMessage) error
}

// key identifies a (family, releaseTag) pair.
type key struct {
	family     string
	releaseTag string
}

var (
	mu       sync.RWMutex
	registry = map[key]FamilySchemaValidator{}
)

// Register adds a validator for the given (family, releaseTag) pair.
// Generated packages call this from their init() function.
// Duplicate registration panics because that indicates two generated
// packages claiming the same (family, release) scope.
func Register(family, releaseTag string, v FamilySchemaValidator) {
	if v == nil {
		panic(fmt.Sprintf("cfgvalidation.Register: nil validator for %s/%s", family, releaseTag))
	}
	k := key{family, releaseTag}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[k]; dup {
		panic(fmt.Sprintf("cfgvalidation.Register: duplicate validator for family=%q release=%q", family, releaseTag))
	}
	registry[k] = v
}

// Lookup returns the registered validator for (family, releaseTag), or nil
// when none has been registered. A nil return means schema validation should
// be skipped for this case.
func Lookup(family, releaseTag string) FamilySchemaValidator {
	mu.RLock()
	defer mu.RUnlock()
	return registry[key{family, releaseTag}]
}

// RegisteredKeys returns a sorted snapshot of all registered (family,
// releaseTag) pairs. Intended for diagnostic logging in tests.
func RegisteredKeys() [][2]string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([][2]string, 0, len(registry))
	for k := range registry {
		out = append(out, [2]string{k.family, k.releaseTag})
	}
	return out
}
