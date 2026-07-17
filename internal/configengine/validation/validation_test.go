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

package validation

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

func TestStructuralValidatorAcceptsYANGAndDME(t *testing.T) {
	tests := []struct {
		name string
		ctx  Context
		op   transport.Op
	}{
		{
			name: "yang",
			ctx:  Context{Family: "vlan", AllowedWritePrefixes: []string{"/Cisco-IOS-XE-native:native/vlan"}},
			op:   transport.Op{Verb: transport.VerbMerge, Path: "/Cisco-IOS-XE-native:native/vlan", Body: []byte(`{"Cisco-IOS-XE-native:vlan":{}}`)},
		},
		{
			name: "dme",
			ctx:  Context{Platform: "nxos", Family: "vlan", AllowedWritePrefixes: []string{"sys/bd"}},
			op:   transport.Op{Verb: transport.VerbMerge, Path: "sys/bd", Body: []byte(`{"bdEntity":{"children":[]}}`)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := NewStructuralValidator().ValidateOperation(tc.ctx, tc.op); err != nil {
				t.Fatalf("ValidateOperation: %v", err)
			}
		})
	}
}

func TestStructuralValidatorRejectsDMEOutsideScope(t *testing.T) {
	err := NewStructuralValidator().ValidateOperation(Context{
		Platform: "nxos", Family: "vlan", AllowedWritePrefixes: []string{"sys/bd"},
	}, transport.Op{
		Verb: transport.VerbDelete, Path: "sys/intf/phys-[eth1/1]",
	})
	if err == nil {
		t.Fatal("ValidateOperation returned nil for out-of-scope DME mutation")
	}
}

func TestStructuralValidatorRejectsDMEAncestorOutsideDeclaredScope(t *testing.T) {
	err := NewStructuralValidator().ValidateOperation(Context{
		Platform: "nxos", Family: "vlan", AllowedWritePrefixes: []string{"sys/bd"},
	}, transport.Op{
		Verb: transport.VerbMerge, Path: "sys", Body: []byte(`{"topSystem":{}}`),
	})
	if err == nil {
		t.Fatal("ValidateOperation returned nil for undeclared DME ancestor mutation")
	}
}
