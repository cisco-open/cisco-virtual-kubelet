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
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestForDefaultsToRESTCONF(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "10.0.0.1",
		Port:    443,
		TLS:     &ciskov1.TLSConfig{Enabled: true},
	}
	cli, err := For(spec, "pw", FactoryOptions{})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if cli.Capabilities().Kind != KindRESTCONF {
		t.Errorf("got kind %v, want restconf", cli.Capabilities().Kind)
	}
}

func TestForNETCONFReserved(t *testing.T) {
	spec := &ciskov1.DeviceSpec{Address: "10.0.0.1", Transport: "netconf"}
	_, err := For(spec, "pw", FactoryOptions{})
	if err == nil || !strings.Contains(err.Error(), "NETCONF") {
		t.Fatalf("got %v, want NETCONF-reserved error", err)
	}
}

func TestForGNMIReserved(t *testing.T) {
	spec := &ciskov1.DeviceSpec{Address: "10.0.0.1", Transport: "gnmi"}
	_, err := For(spec, "pw", FactoryOptions{})
	if err == nil || !strings.Contains(err.Error(), "gNMI") {
		t.Fatalf("got %v, want gNMI-reserved error", err)
	}
}

func TestForUnknownTransport(t *testing.T) {
	spec := &ciskov1.DeviceSpec{Address: "10.0.0.1", Transport: "telnet"}
	_, err := For(spec, "pw", FactoryOptions{})
	if err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("got %v, want unknown-transport error", err)
	}
}

func TestForEmptyAddressRejected(t *testing.T) {
	spec := &ciskov1.DeviceSpec{}
	_, err := For(spec, "", FactoryOptions{})
	if err == nil {
		t.Error("expected error for empty address")
	}
}
