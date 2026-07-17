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
	"reflect"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

type legacyScopeWriter struct{}

func (legacyScopeWriter) Family() string                                          { return "legacy" }
func (legacyScopeWriter) YANGPaths() []string                                     { return []string{"/native/vlan"} }
func (legacyScopeWriter) Fetch(context.Context, transport.Interface) (any, error) { return nil, nil }
func (legacyScopeWriter) Diff(any, any) ([]transport.Op, error)                   { return nil, nil }
func (legacyScopeWriter) Apply(context.Context, transport.Interface, []transport.Op) error {
	return nil
}

type dmeScopeWriter struct{ legacyScopeWriter }

func (dmeScopeWriter) OperationScope() OperationScope {
	return OperationScope{ReadPaths: []string{"/nxos/vlan/brief"}, WritePrefixes: []string{"sys/bd"}}
}

type contextualWriter struct{ legacyScopeWriter }

func (contextualWriter) DiffWithContext(ctx DiffContext, _, _ any) ([]transport.Op, error) {
	return []transport.Op{{Path: ctx.Platform + "/" + ctx.ModelVersion}}, nil
}

func TestScopeOfLegacyWriter(t *testing.T) {
	got := ScopeOf(legacyScopeWriter{})
	want := OperationScope{ReadPaths: []string{"/native/vlan"}, WritePrefixes: []string{"/native/vlan"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScopeOf() = %#v, want %#v", got, want)
	}
}

func TestScopeOfScopedWriterReturnsCopy(t *testing.T) {
	got := ScopeOf(dmeScopeWriter{})
	if !reflect.DeepEqual(got.WritePrefixes, []string{"sys/bd"}) {
		t.Fatalf("WritePrefixes = %v", got.WritePrefixes)
	}
	got.WritePrefixes[0] = "changed"
	again := ScopeOf(dmeScopeWriter{})
	if again.WritePrefixes[0] != "sys/bd" {
		t.Fatalf("ScopeOf leaked caller mutation: %v", again.WritePrefixes)
	}
}

func TestDiffUsesOptionalContextualCompiler(t *testing.T) {
	ops, err := Diff(DiffContext{Platform: "nxos", ModelVersion: "0.3.0"}, contextualWriter{}, nil, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || ops[0].Path != "nxos/0.3.0" {
		t.Fatalf("ops=%#v", ops)
	}
	legacy, err := Diff(DiffContext{Platform: "nxos"}, legacyScopeWriter{}, nil, nil)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("legacy Diff ops=%#v err=%v", legacy, err)
	}
}
