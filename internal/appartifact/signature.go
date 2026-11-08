// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/encoding/protojson"
)

// KeyVerifier accepts modern Sigstore v0.3 message-signature bundles verified
// against locally configured public keys. It makes no transparency-log,
// timestamp, keyless identity, or deployment-authorization claim.
type KeyVerifier struct{ keys []pinnedKey }
type pinnedKey struct {
	verifier  signature.Verifier
	digest    string
	algorithm signature.AlgorithmDetails
}

func NewKeyVerifier(publicKeys []string) (*KeyVerifier, error) {
	if len(publicKeys) < 1 || len(publicKeys) > 8 {
		return nil, fmt.Errorf("configure between one and eight producer public keys")
	}
	result := &KeyVerifier{}
	seen := map[string]bool{}
	for _, keyPEM := range publicKeys {
		if len(keyPEM) > 16<<10 || !strings.HasPrefix(strings.TrimSpace(keyPEM), "-----BEGIN PUBLIC KEY-----") {
			return nil, fmt.Errorf("producer public key exceeds size limit")
		}
		block, rest := pem.Decode([]byte(keyPEM))
		if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("producer key must be one PKIX PUBLIC KEY PEM block")
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("invalid producer public key")
		}
		switch typed := key.(type) {
		case *rsa.PublicKey:
			if typed.N.BitLen() != 2048 && typed.N.BitLen() != 3072 && typed.N.BitLen() != 4096 {
				return nil, fmt.Errorf("unsupported RSA producer key size")
			}
		case *ecdsa.PublicKey:
			if typed.Curve != elliptic.P256() && typed.Curve != elliptic.P384() && typed.Curve != elliptic.P521() {
				return nil, fmt.Errorf("unsupported producer key curve")
			}
		case ed25519.PublicKey:
		default:
			return nil, fmt.Errorf("unsupported producer public key type")
		}
		verifier, err := signature.LoadDefaultVerifier(key)
		if err != nil {
			return nil, fmt.Errorf("unsupported producer signature algorithm: %w", err)
		}
		algorithm, err := signature.GetDefaultAlgorithmDetails(key)
		if err != nil {
			return nil, fmt.Errorf("unsupported producer signature algorithm")
		}
		keyDigest := Digest(block.Bytes)
		if seen[keyDigest] {
			return nil, fmt.Errorf("duplicate producer public key")
		}
		seen[keyDigest] = true
		result.keys = append(result.keys, pinnedKey{verifier: verifier, digest: keyDigest, algorithm: algorithm})
	}
	return result, nil
}

// Verify authenticates the exact descriptor bytes and returns the configured
// key's SPKI SHA-256. Bundle hints never select or introduce a trusted key.
func (v *KeyVerifier) Verify(payload, rawBundle []byte) (string, error) {
	if v == nil || len(v.keys) == 0 {
		return "", fmt.Errorf("producer verifier is not configured")
	}
	if len(payload) == 0 || len(payload) > MaxDescriptorBytes || len(rawBundle) == 0 || len(rawBundle) > MaxSignatureBytes {
		return "", fmt.Errorf("signature input exceeds size limit or is empty")
	}
	if _, err := jsonValue(rawBundle); err != nil {
		return "", fmt.Errorf("invalid signature bundle: %w", err)
	}
	var bundle protobundle.Bundle
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: 16}).Unmarshal(rawBundle, &bundle); err != nil {
		return "", fmt.Errorf("invalid Sigstore bundle")
	}
	if bundle.MediaType != "application/vnd.dev.sigstore.bundle.v0.3+json" {
		return "", fmt.Errorf("only Sigstore v0.3 bundles are supported")
	}
	material := bundle.GetVerificationMaterial()
	if material == nil || material.GetPublicKey() == nil {
		return "", fmt.Errorf("only configured-key Sigstore bundles are supported; keyless is unsupported")
	}
	message := bundle.GetMessageSignature()
	if message == nil || message.MessageDigest == nil || len(message.Signature) == 0 {
		return "", fmt.Errorf("message-signature bundle required; DSSE is unsupported")
	}
	for _, key := range v.keys {
		// The pinned key's maintained algorithm definition determines the
		// message digest. Untrusted bundle metadata cannot select a weaker hash.
		if message.MessageDigest.Algorithm != key.algorithm.GetProtoHashType() || !bytes.Equal(message.MessageDigest.Digest, signatureMessageDigest(payload, key.algorithm.GetHashType())) {
			continue
		}
		if err := key.verifier.VerifySignature(bytes.NewReader(message.Signature), bytes.NewReader(payload)); err == nil {
			return key.digest, nil
		}
	}
	return "", fmt.Errorf("descriptor signature does not match a configured producer key")
}

// Pure Ed25519 signs the bytes directly; the Sigstore bundle represents these
// with HASH_ALGORITHM_UNSPECIFIED. Other algorithms carry their declared hash.
func signatureMessageDigest(payload []byte, algorithm crypto.Hash) []byte {
	if algorithm == 0 {
		return payload
	}
	hash := algorithm.New()
	_, _ = hash.Write(payload)
	return hash.Sum(nil)
}
