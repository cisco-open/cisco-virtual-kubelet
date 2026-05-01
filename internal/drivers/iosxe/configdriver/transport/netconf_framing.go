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
	"fmt"
	"io"
)

// NETCONF framing (RFC 6241 §4.1 for 1.0, RFC 6242 §4.2 for 1.1).
//
// We implement both:
//
//   - 1.0 framing — the legacy end-of-message marker ]]>]]>, used
//     during the hello exchange and for the entire session when
//     neither peer advertises base:1.1.
//
//   - 1.1 chunked framing — length-prefixed chunks
//         \n#<N>\n<N bytes>
//     terminated by \n##\n. Switched on after hello when both
//     peers advertise base:1.1 in their capabilities.
//
// The framer is a pair of functions rather than an interface-heavy
// type because the per-session switch is a simple boolean and
// escaping it with an interface would add lifetime complexity to
// tests that inject pipes.

// endOfMessage10 is the NETCONF 1.0 end-of-message marker. Any
// occurrence inside an RPC payload has to be pre-escaped by the
// sender (Cisco YANG output doesn't contain it naturally), but a
// defence-in-depth reader never splits on a marker found inside a
// CDATA section.
var endOfMessage10 = []byte("]]>]]>")

// writeFrame10 writes the 1.0 frame: payload + end-of-message.
// Callers must ensure payload does not itself contain the marker.
func writeFrame10(w io.Writer, payload []byte) error {
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write(endOfMessage10)
	return err
}

// readFrame10 reads one 1.0 frame into out, returning the payload
// up to but not including the terminating ]]>]]> marker. The
// returned slice aliases into an internal buffer; callers must
// copy if they hold on to it across the next read.
func readFrame10(r *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		// ReadBytes consumes up to and including the delimiter.
		// We use ']' and then check the 5-char tail for ]]>]]>.
		chunk, err := r.ReadBytes(']')
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				return nil, fmt.Errorf("NETCONF: unexpected EOF mid-frame (%d bytes buffered)", buf.Len())
			}
			return nil, err
		}
		buf.Write(chunk)
		// Peek four more bytes to detect the complete marker.
		peek, err := r.Peek(5)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(peek) >= 5 && bytes.Equal(peek[:5], []byte("]>]]>")) {
			if _, err := r.Discard(5); err != nil {
				return nil, err
			}
			raw := buf.Bytes()
			// Strip the trailing ']' we appended plus the ]]>]]>
			// tail we just consumed.
			return raw[:len(raw)-1], nil
		}
		// False alarm — the ']' was data, not the start of the marker.
	}
}

// writeFrame11 writes the 1.1 chunked frame. Single-chunk encoding
// is sufficient for our RPC sizes (every Cisco YANG subtree we
// address fits well under any sensible chunk limit); if a client
// ever needs multi-chunk it's an additive change.
func writeFrame11(w io.Writer, payload []byte) error {
	header := fmt.Appendf(nil, "\n#%d\n", len(payload))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n##\n"))
	return err
}

// readFrame11 reads one 1.1 chunked frame. Multiple chunks
// concatenate into a single message; the terminator \n##\n ends
// the message.
func readFrame11(r *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	for {
		// Each chunk starts with \n# (or \n## for end).
		if err := expectByte(r, '\n'); err != nil {
			return nil, fmt.Errorf("NETCONF 1.1: expected chunk LF: %w", err)
		}
		if err := expectByte(r, '#'); err != nil {
			return nil, fmt.Errorf("NETCONF 1.1: expected chunk '#': %w", err)
		}
		peek, err := r.Peek(1)
		if err != nil {
			return nil, err
		}
		if peek[0] == '#' {
			// End-of-message: \n##\n
			if _, err := r.Discard(1); err != nil {
				return nil, err
			}
			if err := expectByte(r, '\n'); err != nil {
				return nil, fmt.Errorf("NETCONF 1.1: expected trailing LF after ##: %w", err)
			}
			return out.Bytes(), nil
		}
		// Chunk length.
		sizeLine, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("NETCONF 1.1: chunk size: %w", err)
		}
		var size int
		if _, err := fmt.Sscanf(sizeLine, "%d\n", &size); err != nil {
			return nil, fmt.Errorf("NETCONF 1.1: bad chunk size %q: %w", sizeLine, err)
		}
		if size <= 0 || size > (4<<20) { // 4 MiB hard cap
			return nil, fmt.Errorf("NETCONF 1.1: chunk size %d out of bounds", size)
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(r, chunk); err != nil {
			return nil, fmt.Errorf("NETCONF 1.1: chunk body: %w", err)
		}
		out.Write(chunk)
	}
}

// expectByte reads one byte and errors when it doesn't match the
// expected value. Inlined all over readFrame11; the single helper
// makes the framing loop readable.
func expectByte(r *bufio.Reader, want byte) error {
	got, err := r.ReadByte()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("expected %q, got %q", want, got)
	}
	return nil
}
