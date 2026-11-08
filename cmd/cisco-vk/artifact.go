// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/spf13/cobra"
)

func newArtifactCommand() *cobra.Command {
	command := &cobra.Command{Use: "artifact", Short: "Preflight optional Verified application artifacts"}
	command.AddCommand(newArtifactVerifyCommand())
	return command
}

func newArtifactVerifyCommand() *cobra.Command {
	var store, platform, descriptorDigest, packageURL, packageFile, caFile string
	var keyFiles []string
	var maxPackageBytes int64
	var timeout time.Duration
	command := &cobra.Command{
		Use: "verify", Short: "Verify signed artifact metadata and optionally a local package",
		Long: "Verify a descriptor digest, pinned producer signature, target platform and exact package URL.\nWith --package-file, also hash a local prepared archive. This does not verify bytes\non a Cisco device, authorize a deployment, or assess vulnerabilities.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			parts := strings.Split(platform, "/")
			if len(parts) < 2 || len(parts) > 3 {
				return fmt.Errorf("platform must be os/architecture[/variant]")
			}
			target := appartifact.Platform{OS: parts[0], Architecture: parts[1]}
			if len(parts) == 3 {
				target.Variant = parts[2]
				if target.Variant == "" {
					return fmt.Errorf("platform variant must not be empty")
				}
			}
			if len(keyFiles) < 1 || len(keyFiles) > 8 {
				return fmt.Errorf("provide between one and eight producer public keys")
			}
			keys := make([]string, 0, len(keyFiles))
			for _, path := range keyFiles {
				raw, err := readArtifactPublicKey(path)
				if err != nil {
					return err
				}
				keys = append(keys, string(raw))
			}
			tlsConfig, err := loadArtifactTLSConfig(caFile)
			if err != nil {
				return err
			}
			resolver, err := appartifact.NewResolver(appartifact.Config{
				Origin: store, ProducerPublicKeys: keys, Platform: target,
				MaxPackageBytes: maxPackageBytes, Timeout: timeout, TLSConfig: tlsConfig,
			})
			if err != nil {
				return err
			}
			defer resolver.CloseIdleConnections()
			artifact, err := resolver.Resolve(command.Context(), descriptorDigest, packageURL)
			if err != nil {
				return err
			}
			scope := "signed-descriptor"
			if packageFile != "" {
				if err := verifyLocalArtifactPackage(packageFile, artifact.Descriptor.Package); err != nil {
					return err
				}
				scope = "signed-descriptor-and-local-package"
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(struct {
				VerificationScope          string `json:"verificationScope"`
				DescriptorDigest           string `json:"descriptorDigest"`
				ManifestDigest             string `json:"manifestDigest"`
				PackageDigest              string `json:"packageDigest"`
				ProducerKeyDigest          string `json:"producerKeyDigest"`
				DeviceInstallationVerified bool   `json:"deviceInstallationVerified"`
			}{scope, artifact.DescriptorDigest, artifact.Descriptor.Source.ManifestDigest,
				artifact.Descriptor.Package.Digest, artifact.ProducerKeyDigest, false})
		},
	}
	flags := command.Flags()
	flags.StringVar(&store, "store", "", "Trusted HTTPS artifact store origin")
	flags.StringSliceVar(&keyFiles, "producer-key", nil, "Pinned producer public-key PEM file (repeat for key rotation)")
	flags.StringVar(&platform, "platform", "", "Explicit target platform, e.g. linux/amd64")
	flags.StringVar(&descriptorDigest, "descriptor", "", "Exact sha256 descriptor digest")
	flags.StringVar(&packageURL, "package-url", "", "Exact immutable package URL intended for the Pod image")
	flags.StringVar(&packageFile, "package-file", "", "Also verify the size and SHA256 of this local archive")
	flags.StringVar(&caFile, "ca-file", "", "PEM CA bundle for the store (replaces system TLS roots for this command)")
	flags.Int64Var(&maxPackageBytes, "max-package-bytes", appartifact.DefaultMaxPackageBytes, "Maximum declared package size")
	flags.DurationVar(&timeout, "timeout", 30*time.Second, "Timeout per metadata request (at most 2m)")
	for _, name := range []string{"store", "producer-key", "platform", "descriptor", "package-url"} {
		_ = command.MarkFlagRequired(name)
	}
	return command
}

func readArtifactPublicKey(path string) ([]byte, error) {
	file, err := openRegularArtifactFile(path)
	if err != nil {
		return nil, fmt.Errorf("open producer public key: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return nil, fmt.Errorf("producer public key must be a regular file of at most 16384 bytes")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(raw) > 16384 {
		return nil, fmt.Errorf("could not read bounded producer public key")
	}
	return raw, nil
}

func loadArtifactTLSConfig(path string) (*tls.Config, error) {
	if path == "" {
		return nil, nil
	}
	file, err := openRegularArtifactFile(path)
	if err != nil {
		return nil, fmt.Errorf("open artifact store CA bundle: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("artifact store CA bundle must be a regular file of at most 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("could not read bounded artifact store CA bundle")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("artifact store CA bundle contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
}

// This proves local file content only, not device content or immutability after
// the check. A device installer must independently verify its staged bytes.
func verifyLocalArtifactPackage(path string, expected appartifact.Package) error {
	if expected.SizeBytes < 1 || expected.SizeBytes > 64<<30 || !appartifact.ValidDigest(expected.Digest) {
		return fmt.Errorf("invalid expected package identity")
	}
	file, err := openRegularArtifactFile(path)
	if err != nil {
		return fmt.Errorf("open local package: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.SizeBytes {
		return fmt.Errorf("local package must be a regular file with the exact declared size")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, expected.SizeBytes+1))
	if err != nil || size != expected.SizeBytes {
		return fmt.Errorf("local package size changed or could not be read")
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected.Digest {
		return fmt.Errorf("local package digest mismatch")
	}
	return nil
}

// Check regularity before opening so ordinary FIFO/device inputs cannot block
// the CLI. Callers also fstat after opening to validate the descriptor actually
// read. Local input paths must remain under the operator's control.
func openRegularArtifactFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact input must be a regular file")
	}
	return os.Open(path)
}
