// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// Package appartifact implements the optional Verified artifact identity boundary.
// It does not authorize deployments or claim that an application is vulnerability-free.
package appartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	APIVersion                   = "apphosting.cisco.com/v1alpha1"
	BindingAnnotation            = "cisco.io/apphost-artifacts"
	MaxDescriptorBytes           = 256 << 10
	MaxSignatureBytes            = 1 << 20
	MaxBindingBytes              = 16 << 10
	DefaultMaxPackageBytes int64 = 4 << 30
)

var (
	digestPattern       = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	containerPattern    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	architecturePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	variantPattern      = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,32}$`)
	repositoryPattern   = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
)

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

type Source struct {
	Registry        string   `json:"registry"`
	Repository      string   `json:"repository"`
	ReleaseLabel    string   `json:"releaseLabel,omitempty"`
	RequestedDigest string   `json:"requestedDigest"`
	IndexDigest     string   `json:"indexDigest,omitempty"`
	ManifestDigest  string   `json:"manifestDigest"`
	ConfigDigest    string   `json:"configDigest"`
	Platform        Platform `json:"platform"`
}

type Package struct {
	Format    string `json:"format"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"sizeBytes"`
}

type Producer struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	BuildDigest  string `json:"buildDigest"`
	RecipeDigest string `json:"recipeDigest"`
}

type Evidence struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
}

type Descriptor struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Source     Source    `json:"source"`
	Package    Package   `json:"package"`
	Producer   Producer  `json:"producer"`
	CreatedAt  string    `json:"createdAt"`
	Provenance Evidence  `json:"provenance"`
	SBOM       *Evidence `json:"sbom,omitempty"`
}

// Digest identifies the exact bytes, including whitespace and any final newline.
func Digest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

// DecodeDescriptor validates the bounded wire contract and its exact expected hash.
// This does not verify signatures, upstream registry content, or package bytes.
func DecodeDescriptor(raw []byte, expectedDigest string, maxPackageBytes int64) (*Descriptor, error) {
	if !ValidDigest(expectedDigest) {
		return nil, fmt.Errorf("invalid expected descriptor digest")
	}
	if len(raw) == 0 || len(raw) > MaxDescriptorBytes {
		return nil, fmt.Errorf("descriptor exceeds size limit or is empty")
	}
	if Digest(raw) != expectedDigest {
		return nil, fmt.Errorf("descriptor digest mismatch")
	}
	var descriptor Descriptor
	if err := decodeStrict(raw, &descriptor); err != nil {
		return nil, fmt.Errorf("invalid descriptor: %w", err)
	}
	if maxPackageBytes == 0 {
		maxPackageBytes = DefaultMaxPackageBytes
	}
	if maxPackageBytes < 1 {
		return nil, fmt.Errorf("package size limit must be positive")
	}
	if err := descriptor.validate(maxPackageBytes); err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func (d *Descriptor) validate(limit int64) error {
	if d.APIVersion != APIVersion || d.Kind != "AppHostingArtifact" {
		return fmt.Errorf("unsupported artifact contract")
	}
	for name, digest := range map[string]string{"requested": d.Source.RequestedDigest, "manifest": d.Source.ManifestDigest, "config": d.Source.ConfigDigest, "package": d.Package.Digest, "producer build": d.Producer.BuildDigest, "recipe": d.Producer.RecipeDigest, "provenance": d.Provenance.Digest} {
		if !ValidDigest(digest) {
			return fmt.Errorf("invalid %s digest", name)
		}
	}
	if d.Source.IndexDigest == "" {
		if d.Source.RequestedDigest != d.Source.ManifestDigest {
			return fmt.Errorf("direct source digest does not match manifest")
		}
	} else if !ValidDigest(d.Source.IndexDigest) || d.Source.RequestedDigest != d.Source.IndexDigest || d.Source.IndexDigest == d.Source.ManifestDigest {
		return fmt.Errorf("invalid source index relationship")
	}
	if err := d.Source.Platform.Validate(); err != nil {
		return err
	}
	registry, err := url.Parse("https://" + d.Source.Registry)
	if err != nil || len(d.Source.Registry) > 253 || registry.Host == "" || registry.Host != d.Source.Registry || registry.User != nil || registry.Path != "" || registry.RawQuery != "" || registry.Fragment != "" || strings.ToLower(d.Source.Registry) != d.Source.Registry {
		return fmt.Errorf("invalid canonical source registry")
	}
	if !repositoryPattern.MatchString(d.Source.Repository) || len(d.Source.Repository) > 255 {
		return fmt.Errorf("invalid canonical source repository")
	}
	if d.Package.Format != "docker-save-v1" && d.Package.Format != "iox-package-v1" {
		return fmt.Errorf("unsupported package format")
	}
	if d.Package.MediaType != "application/x-tar" {
		return fmt.Errorf("unsupported package media type")
	}
	if d.Package.SizeBytes < 1 || d.Package.SizeBytes > limit || d.Package.SizeBytes > 9007199254740991 {
		return fmt.Errorf("package exceeds size limit or has invalid size")
	}
	if _, err := time.Parse(time.RFC3339Nano, d.CreatedAt); err != nil {
		return fmt.Errorf("invalid artifact creation timestamp")
	}
	for _, value := range []string{d.Source.ReleaseLabel, d.Producer.Name, d.Producer.Version, d.Provenance.MediaType} {
		if len([]rune(value)) > 256 {
			return fmt.Errorf("artifact text field exceeds limit")
		}
	}
	if d.SBOM != nil && (!ValidDigest(d.SBOM.Digest) || len([]rune(d.SBOM.MediaType)) > 256) {
		return fmt.Errorf("invalid SBOM reference")
	}
	return nil
}

func (p Platform) Validate() error {
	if p.OS != "linux" || !architecturePattern.MatchString(p.Architecture) || (p.Variant != "" && !variantPattern.MatchString(p.Variant)) {
		return fmt.Errorf("invalid or unsupported explicit platform")
	}
	return nil
}

// Binding is the Verified-mode annotation. Governed release/transition fields
// are intentionally unsupported rather than silently treated as authorization.
type Binding struct {
	DescriptorDigest string `json:"descriptorDigest"`
}

func ParseBindings(raw string, containerNames []string) (map[string]Binding, error) {
	if len(raw) == 0 || len(raw) > MaxBindingBytes {
		return nil, fmt.Errorf("artifact bindings are missing or exceed size limit")
	}
	if len(containerNames) == 0 || len(containerNames) > 64 {
		return nil, fmt.Errorf("unsupported container count")
	}
	var bindings map[string]Binding
	if err := decodeStrict([]byte(raw), &bindings); err != nil {
		return nil, fmt.Errorf("invalid Verified artifact bindings (Governed fields are unsupported): %w", err)
	}
	if len(bindings) != len(containerNames) {
		return nil, fmt.Errorf("one artifact binding is required for each regular container")
	}
	seen := map[string]bool{}
	for _, name := range containerNames {
		if len(name) > 63 || !containerPattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate container name")
		}
		seen[name] = true
		binding, ok := bindings[name]
		if !ok || !ValidDigest(binding.DescriptorDigest) {
			return nil, fmt.Errorf("missing or invalid artifact binding for container %q", name)
		}
	}
	return bindings, nil
}
