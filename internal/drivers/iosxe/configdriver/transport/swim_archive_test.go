// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package transport

import (
	"bytes"
	"strings"
	"testing"
)

func TestRetiredArchiveSCPIsSingleExactFile(t *testing.T) {
	path := "flash:gNOI_iosxe_17.18.02.bin"
	valid := "C0644 3 gNOI_iosxe_17.18.02.bin\nabc\x00"
	for name, input := range map[string]string{"valid": valid, "changed size": strings.Replace(valid, " 3 ", " 4 ", 1), "changed name": strings.Replace(valid, "17.18.02", "17.18.03", 1), "truncated": "C0644 3 gNOI_iosxe_17.18.02.bin\nab", "remote error": valid[:len(valid)-1] + "\x01", "additional file": valid + valid, "directory": strings.Replace(valid, "C0644", "D0755", 1), "large header": strings.Repeat("x", 4097)} {
		var ack, out bytes.Buffer
		err := receiveRetiredArchive(&ack, strings.NewReader(input), path, 3, &out)
		if name == "valid" {
			if err != nil || out.String() != "abc" {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	for _, p := range []string{"flash:packages.conf", "flash:*.bin", "flash:gNOI_iosxe_17.18.02.bin;reload", "flash:../image.bin", "flash:gNOI_iosxe_17.18.02.bin\nreload", "bootflash:gNOI_iosxe_17.18.02.bin"} {
		if retiredArchivePath.MatchString(p) {
			t.Fatalf("unsafe path %q", p)
		}
	}
}
