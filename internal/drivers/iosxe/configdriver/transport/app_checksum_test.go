// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package transport

import (
	"context"
	"strings"
	"testing"
)

func TestApplicationArchiveNativeChecksumEvidence(t *testing.T) {
	path := "flash:cvk-" + strings.Repeat("1", 16) + "-" + strings.Repeat("2", 64) + ".tar"
	digest := strings.Repeat("a", 128)
	listing := "Directory of flash:/\n254042 -rw- 7656448 Nov 2 2026 20:00:00 +00:00 " + strings.TrimPrefix(path, "flash:") + "\n11353194496 bytes total (5398515712 bytes free)"
	checksum := "...Done!\nverify /sha512 (" + path + ") = " + digest + "\n"
	for _, tc := range []struct {
		name, listing, checksum string
		size                    int64
		ok                      bool
	}{
		{"application archive", listing, checksum, 7656448, true},
		{"uppercase hash", listing, strings.ReplaceAll(checksum, digest, strings.ToUpper(digest)), 7656448, true},
		{"corrupt same-size archive", listing, strings.ReplaceAll(checksum, digest, strings.Repeat("b", 128)), 7656448, false},
		{"truncated archive", listing, checksum, 7656449, false},
		{"different filename", strings.ReplaceAll(listing, strings.Repeat("2", 64), strings.Repeat("3", 64)), checksum, 7656448, false},
		{"different checksum target", listing, strings.ReplaceAll(checksum, path, path+".other"), 7656448, false},
		{"embedded platform signature", listing, "Embedded hash verification successful", 7656448, false},
		{"invalid syntax", listing, "% Invalid input detected", 7656448, false},
		{"missing file", "%Error opening file", checksum, 7656448, false},
		{"ambiguous file metadata", listing + "\n" + listing, checksum, 7656448, false},
		{"ambiguous hash response", listing, checksum + checksum, 7656448, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAppPackageChecksumOutput(path, tc.size, digest, tc.listing, tc.checksum)
			if (err == nil) != tc.ok {
				t.Fatalf("checksum acceptance=%v", err)
			}
		})
	}
}

func TestApplicationChecksumRejectsUnownedPathsAndMissingTrustBeforeDial(t *testing.T) {
	for _, path := range []string{"flash:packages.conf", "flash:cvk-../other.tar", "flash:cvk-x.tar\nreload"} {
		if err := VerifyAppPackageFile(context.Background(), AppPackageChecksumConfig{}, path, 7, strings.Repeat("a", 128)); err == nil {
			t.Fatal("accepted unsafe path")
		}
	}
	path := "flash:cvk-" + strings.Repeat("1", 16) + "-" + strings.Repeat("2", 64) + ".tar"
	if err := VerifyAppPackageFile(context.Background(), AppPackageChecksumConfig{Address: "192.0.2.1"}, path, 7, strings.Repeat("a", 128)); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("missing host pin did not fail before dial: %v", err)
	}
}
