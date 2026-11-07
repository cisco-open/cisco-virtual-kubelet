// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package appartifact

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestVerifiedPackageDownloadChecksBytesAndRedirects(t *testing.T) {
	_, pub := signingKey(t)
	for _, mode := range []string{"valid", "tampered", "oversized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			r, server := newTestResolver(t, func(w http.ResponseWriter, req *http.Request) {
				switch mode {
				case "valid":
					w.Write([]byte("package"))
				case "tampered":
					w.Write([]byte("packagX"))
				case "oversized":
					w.Write([]byte("package!"))
				case "redirect":
					http.Redirect(w, req, "https://untrusted.example/package", 302)
				}
			}, pub)
			a := &VerifiedArtifact{PackageURL: server.URL + "/v1/packages/sha256/" + Digest([]byte("package"))[7:] + "/package.tar", Descriptor: &Descriptor{Package: Package{Digest: Digest([]byte("package")), SizeBytes: 7}}}
			file, err := r.DownloadPackage(context.Background(), a)
			if mode != "valid" {
				if err == nil {
					file.Close()
					os.Remove(file.Name())
					t.Fatal("accepted invalid response")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			defer os.Remove(file.Name())
			bytes, _ := io.ReadAll(file)
			if string(bytes) != "package" {
				t.Fatal("wrong staged bytes")
			}
		})
	}
}
