// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
)

// DownloadPackage uses the same origin/TLS/redirect policy as metadata. The
// caller owns the returned temporary file and must close and remove it.
func (r *Resolver) DownloadPackage(ctx context.Context, a *VerifiedArtifact) (*os.File, error) {
	if r == nil || a == nil || a.Descriptor == nil || !ValidDigest(a.Descriptor.Package.Digest) || a.Descriptor.Package.SizeBytes < 1 || a.Descriptor.Package.SizeBytes > r.maxPackageBytes {
		return nil, fmt.Errorf("invalid verified package")
	}

	expectedURL := r.origin + "/v1/packages/sha256/" + a.Descriptor.Package.Digest[7:] + "/package.tar"
	if a.PackageURL != expectedURL {
		return nil, fmt.Errorf("package origin mismatch")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", expectedURL, nil)
	if err != nil {
		return nil, err
	}
	// Bulk transfers use the operation context deadline rather than the short
	// metadata timeout. Transport still bounds headers, TLS and connection setup.
	client := *r.client
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("package download failed")
	}
	defer resp.Body.Close()
	p := a.Descriptor.Package
	if resp.StatusCode != 200 || (resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity") || (resp.ContentLength >= 0 && resp.ContentLength != p.SizeBytes) {
		return nil, fmt.Errorf("invalid package response")
	}
	f, err := os.CreateTemp("", "cvk-artifact-*")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, p.SizeBytes+1))
	if err != nil || n != p.SizeBytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != p.Digest {
		return nil, fmt.Errorf("downloaded package size or SHA256 mismatch")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	ok = true
	return f, nil
}
