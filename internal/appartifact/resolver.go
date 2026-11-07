// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Origin is an administrator-configured HTTPS origin, with no path/query.
	Origin             string
	ProducerPublicKeys []string
	Platform           Platform
	MaxPackageBytes    int64
	Timeout            time.Duration
	// TLSConfig supports private CA roots. InsecureSkipVerify and an unrelated
	// ServerName are rejected. Configuration is cloned at construction time.
	TLSConfig *tls.Config
}

type Resolver struct {
	origin          string
	platform        Platform
	maxPackageBytes int64
	client          *http.Client
	verifier        *KeyVerifier
}

type VerifiedArtifact struct {
	Descriptor            *Descriptor
	DescriptorBytes       []byte
	DescriptorDigest      string
	SignatureBundleDigest string
	ProducerKeyDigest     string
	PackageURL            string
}

func NewResolver(config Config) (*Resolver, error) {
	origin, err := url.Parse(config.Origin)
	if len(config.Origin) > 2048 || err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.Opaque != "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawFragment != "" || origin.RawPath != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, fmt.Errorf("artifact origin must be an HTTPS origin without userinfo, path, query or fragment")
	}
	if config.Origin != "https://"+origin.Host && config.Origin != "https://"+origin.Host+"/" {
		return nil, fmt.Errorf("artifact origin contains a forbidden delimiter")
	}
	if origin.Host != strings.ToLower(origin.Host) || strings.HasSuffix(origin.Hostname(), ".") {
		return nil, fmt.Errorf("artifact origin must use a canonical host")
	}
	if strings.HasSuffix(origin.Host, ":") {
		return nil, fmt.Errorf("artifact origin has an empty port")
	}
	if port := origin.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, fmt.Errorf("artifact origin port must be between 1 and 65535")
		}
	}
	if err := config.Platform.Validate(); err != nil {
		return nil, err
	}
	if config.MaxPackageBytes == 0 {
		config.MaxPackageBytes = DefaultMaxPackageBytes
	}
	if config.MaxPackageBytes < 1 || config.MaxPackageBytes > 64<<30 {
		return nil, fmt.Errorf("package limit must be between one byte and 64 GiB")
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < 0 || config.Timeout > 2*time.Minute {
		return nil, fmt.Errorf("artifact timeout must be positive and at most two minutes")
	}
	verifier, err := NewKeyVerifier(config.ProducerPublicKeys)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
		if tlsConfig.RootCAs != nil {
			tlsConfig.RootCAs = tlsConfig.RootCAs.Clone()
		}
	}
	if tlsConfig.InsecureSkipVerify || (tlsConfig.ServerName != "" && tlsConfig.ServerName != origin.Hostname()) {
		return nil, fmt.Errorf("artifact TLS must verify the configured origin")
	}
	if tlsConfig.MinVersion < tls.VersionTLS12 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DialContext:         (&net.Dialer{Timeout: config.Timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: config.Timeout, ResponseHeaderTimeout: config.Timeout,
		MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 4, MaxIdleConnsPerHost: 2,
		IdleConnTimeout: 30 * time.Second, DisableCompression: true,
	}
	client := &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("artifact redirects are forbidden") }}
	return &Resolver{origin: strings.TrimSuffix(config.Origin, "/"), platform: config.Platform, maxPackageBytes: config.MaxPackageBytes, client: client, verifier: verifier}, nil
}

func (r *Resolver) CloseIdleConnections() {
	if r != nil && r.client != nil {
		r.client.CloseIdleConnections()
	}
}

// Resolve verifies the descriptor and signature, then matches the exact image
// URL and target platform. It never downloads or verifies the device package;
// drivers must still verify those bytes on device before install/activation.
func (r *Resolver) Resolve(ctx context.Context, descriptorDigest, imageURL string) (*VerifiedArtifact, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("artifact resolver is not configured")
	}
	if !ValidDigest(descriptorDigest) {
		return nil, fmt.Errorf("invalid descriptor digest")
	}
	hex := strings.TrimPrefix(descriptorDigest, "sha256:")
	base := r.origin + "/v1/descriptors/sha256/" + hex
	raw, err := r.fetch(ctx, base+".json", MaxDescriptorBytes)
	if err != nil {
		return nil, fmt.Errorf("descriptor retrieval: %w", err)
	}
	descriptor, err := DecodeDescriptor(raw, descriptorDigest, r.maxPackageBytes)
	if err != nil {
		return nil, err
	}
	if descriptor.Source.Platform != r.platform {
		return nil, fmt.Errorf("artifact platform does not match configured target")
	}
	packageURL := r.origin + "/v1/packages/sha256/" + strings.TrimPrefix(descriptor.Package.Digest, "sha256:") + "/package.tar"
	if imageURL != packageURL {
		return nil, fmt.Errorf("container image must match the exact configured artifact package URL")
	}
	bundle, err := r.fetch(ctx, base+".sigstore.json", MaxSignatureBytes)
	if err != nil {
		return nil, fmt.Errorf("signature retrieval: %w", err)
	}
	keyDigest, err := r.verifier.Verify(raw, bundle)
	if err != nil {
		return nil, err
	}
	return &VerifiedArtifact{Descriptor: descriptor, DescriptorBytes: raw, DescriptorDigest: descriptorDigest, SignatureBundleDigest: Digest(bundle), ProducerKeyDigest: keyDigest, PackageURL: packageURL}, nil
}

func (r *Resolver) fetch(ctx context.Context, target string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid artifact request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("HTTPS artifact request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact server returned HTTP %d", response.StatusCode)
	}
	if response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return nil, fmt.Errorf("encoded artifact responses are unsupported")
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("artifact response exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("incomplete artifact response")
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("artifact response exceeds size limit")
	}
	return raw, nil
}
