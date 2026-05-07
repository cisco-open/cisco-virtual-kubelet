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

import "testing"

// Watch-item #6 from the architectural review: parsers that consume
// device-supplied bytes (NETCONF <hello>, <rpc-reply>, gNMI path
// strings) are the obvious place a malformed peer can crash us. A
// crash on the receive path takes the cisco-vk pod down and stalls
// the per-device reconciler. The contract these fuzz targets enforce
// is the minimal one: never panic, always return either a non-nil
// result or a non-nil error. Semantic correctness is left to the
// table tests in netconf_test.go and gnmi_test.go.

func FuzzParseHello(f *testing.F) {
	f.Add([]byte(`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <capabilities>
    <capability>urn:ietf:params:netconf:base:1.1</capability>
  </capabilities>
  <session-id>4</session-id>
</hello>`))
	f.Add([]byte(""))
	f.Add([]byte("<hello/>"))
	f.Add([]byte("<hello><capabilities/></hello>"))
	f.Add([]byte("<hello><session-id>not-a-number</session-id></hello>"))
	f.Add([]byte("<hello><capabilities><capability></capability></capabilities></hello>"))
	f.Add([]byte("<<<<<>>>>>"))

	f.Fuzz(func(t *testing.T, data []byte) {
		caps, sid, err := parseHello(data)
		if err == nil && caps == nil {
			t.Fatalf("parseHello returned nil capabilities and nil error for %q", data)
		}
		_ = sid
	})
}

func FuzzParseRPCReply(f *testing.F) {
	f.Add([]byte(`<rpc-reply message-id="1" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`))
	f.Add([]byte(`<rpc-reply message-id="2"><data><foo/></data></rpc-reply>`))
	f.Add([]byte(`<rpc-reply message-id="3"><rpc-error><error-tag>operation-failed</error-tag></rpc-error></rpc-reply>`))
	f.Add([]byte(""))
	f.Add([]byte("<rpc-reply/>"))
	f.Add([]byte("<rpc-reply><ok></ok><rpc-error><error-tag/></rpc-error></rpc-reply>"))
	f.Add([]byte("<not-rpc-reply/>"))
	f.Add([]byte("<rpc-reply><data><a><b><c><d/></c></b></a></data></rpc-reply>"))

	f.Fuzz(func(t *testing.T, data []byte) {
		reply, err := parseRPCReply(data)
		if err == nil && reply == nil {
			t.Fatalf("parseRPCReply returned nil reply and nil error for %q", data)
		}
	})
}

func FuzzParseGNMIPath(f *testing.F) {
	f.Add("/")
	f.Add("")
	f.Add("/native/hostname")
	f.Add("/native/interface/Loopback=0")
	f.Add("/openconfig-interfaces:interfaces/interface=GigabitEthernet0/0")
	f.Add("//double//slash//")
	f.Add("/foo=1=2=3")
	f.Add(":only-prefix")
	f.Add("=name-only")

	f.Fuzz(func(t *testing.T, p string) {
		path, err := parseGNMIPath(p)
		if err == nil && path == nil {
			t.Fatalf("parseGNMIPath returned nil path and nil error for %q", p)
		}
	})
}
