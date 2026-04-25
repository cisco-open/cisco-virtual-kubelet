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
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFrame10RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"hello", `<hello><capabilities><capability>urn:ietf:params:netconf:base:1.0</capability></capabilities></hello>`},
		{"rpc", `<rpc message-id="101"><get-config><source><running/></source></get-config></rpc>`},
		{"multi-line", "line1\nline2\nline3"},
		{"contains-closing-bracket", "] and ]] but not the marker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame10(&buf, []byte(tc.payload)); err != nil {
				t.Fatalf("write: %v", err)
			}
			r := bufio.NewReader(&buf)
			got, err := readFrame10(r)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(got) != tc.payload {
				t.Fatalf("round-trip lost data:\n  got:  %q\n  want: %q", got, tc.payload)
			}
		})
	}
}

func TestFrame10ReaderDrainsSingleStream(t *testing.T) {
	// Two frames back-to-back: reader must return each payload
	// individually without bleeding into the next.
	var buf bytes.Buffer
	_ = writeFrame10(&buf, []byte("first"))
	_ = writeFrame10(&buf, []byte("second"))

	r := bufio.NewReader(&buf)
	first, err := readFrame10(r)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if string(first) != "first" {
		t.Errorf("first=%q", first)
	}
	second, err := readFrame10(r)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(second) != "second" {
		t.Errorf("second=%q", second)
	}
}

func TestFrame11RoundTrip(t *testing.T) {
	cases := []string{
		"small payload",
		strings.Repeat("a", 10000), // mid-sized
	}
	for i, payload := range cases {
		var buf bytes.Buffer
		if err := writeFrame11(&buf, []byte(payload)); err != nil {
			t.Fatalf("write[%d]: %v", i, err)
		}
		r := bufio.NewReader(&buf)
		got, err := readFrame11(r)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if string(got) != payload {
			t.Fatalf("round-trip[%d] mismatch len got=%d want=%d",
				i, len(got), len(payload))
		}
	}
}

func TestFrame11RejectsHugeChunks(t *testing.T) {
	// Oversized chunk size would let a malicious server pin the
	// reader's memory. 4 MiB is the enforced cap; verify a
	// larger size is rejected before allocation.
	raw := []byte("\n#999999999\n")
	r := bufio.NewReader(bytes.NewReader(raw))
	if _, err := readFrame11(r); err == nil {
		t.Fatal("expected size-bounds error")
	}
}
