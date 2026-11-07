// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/encoding/protojson"
)

func signingKey(t *testing.T) (crypto.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signedBundle(t *testing.T, raw []byte, key crypto.PrivateKey) []byte {
	t.Helper()
	signer, err := signature.LoadDefaultSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.SignMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	algorithm, err := signature.GetDefaultAlgorithmDetails(key.(crypto.Signer).Public())
	if err != nil {
		t.Fatal(err)
	}
	digest := raw
	if hashType := algorithm.GetHashType(); hashType != 0 {
		hash := hashType.New()
		_, _ = hash.Write(raw)
		digest = hash.Sum(nil)
	}
	bundle := &protobundle.Bundle{MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json", VerificationMaterial: &protobundle.VerificationMaterial{Content: &protobundle.VerificationMaterial_PublicKey{PublicKey: &protocommon.PublicKeyIdentifier{Hint: "untrusted-key-hint"}}}, Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{MessageDigest: &protocommon.HashOutput{Algorithm: algorithm.GetProtoHashType(), Digest: digest}, Signature: sig}}}
	data, err := protojson.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSigstoreDefaultKeyAlgorithms(t *testing.T) {
	raw := exampleDescriptor(t)
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		t.Run(curve.Params().Name, func(t *testing.T) {
			key, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			testKeyAlgorithm(t, raw, key)
		})
	}
	t.Run("RSA2048", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		testKeyAlgorithm(t, raw, key)
	})
	t.Run("Ed25519", func(t *testing.T) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		testKeyAlgorithm(t, raw, key)
	})
}

func testKeyAlgorithm(t *testing.T, raw []byte, key crypto.Signer) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	publicKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	verifier, err := NewKeyVerifier([]string{publicKey})
	if err != nil {
		t.Fatal(err)
	}
	bundle := signedBundle(t, raw, key)
	if _, err := verifier.Verify(raw, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(append(append([]byte{}, raw...), ' '), bundle); err == nil {
		t.Fatal("accepted altered payload")
	}
}

func TestPinnedKeySignatureAndRotation(t *testing.T) {
	key, pub := signingKey(t)
	otherKey, otherPub := signingKey(t)
	raw := exampleDescriptor(t)
	bundle := signedBundle(t, raw, key)
	verifier, err := NewKeyVerifier([]string{otherPub, pub})
	if err != nil {
		t.Fatal(err)
	}
	if keyID, err := verifier.Verify(raw, bundle); err != nil || !ValidDigest(keyID) {
		t.Fatalf("authentic signature: %s %v", keyID, err)
	}
	if _, err := verifier.Verify(append(raw, ' '), bundle); err == nil {
		t.Fatal("accepted changed descriptor")
	}
	wrongVerifier, _ := NewKeyVerifier([]string{otherPub})
	if _, err := wrongVerifier.Verify(raw, bundle); err == nil {
		t.Fatal("accepted untrusted key")
	}
	if _, err := verifier.Verify(raw, signedBundle(t, []byte("other payload"), otherKey)); err == nil {
		t.Fatal("accepted signature over different payload")
	}
	var parsed protobundle.Bundle
	if err := protojson.Unmarshal(bundle, &parsed); err != nil {
		t.Fatal(err)
	}
	parsed.GetMessageSignature().Signature[0] ^= 0xff
	tampered, _ := protojson.Marshal(&parsed)
	if _, err := verifier.Verify(raw, tampered); err == nil {
		t.Fatal("accepted tampered signature")
	}
	for _, bad := range [][]byte{[]byte(`{"mediaType":"x","mediaType":"y"}`), []byte(`{}`), append(bundle, []byte(` {}`)...), []byte(strings.Repeat(" ", MaxSignatureBytes+1))} {
		if _, err := verifier.Verify(raw, bad); err == nil {
			t.Fatal("accepted malformed bundle")
		}
	}
}

func newTestResolver(t *testing.T, handler http.HandlerFunc, pub string) (*Resolver, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	resolver, err := NewResolver(Config{Origin: server.URL, ProducerPublicKeys: []string{pub}, Platform: Platform{OS: "linux", Architecture: "amd64"}, TLSConfig: &tls.Config{RootCAs: roots}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver, server
}

func TestResolverAuthenticatesDescriptorFromTLSOrigin(t *testing.T) {
	key, pub := signingKey(t)
	raw := exampleDescriptor(t)
	bundle := signedBundle(t, raw, key)
	digest := Digest(raw)
	descriptor, _ := DecodeDescriptor(raw, digest, 0)
	var requests atomic.Int64
	resolver, server := newTestResolver(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/v1/descriptors/sha256/" + strings.TrimPrefix(digest, "sha256:") + ".json":
			_, _ = w.Write(raw)
		case "/v1/descriptors/sha256/" + strings.TrimPrefix(digest, "sha256:") + ".sigstore.json":
			_, _ = w.Write(bundle)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}, pub)
	imageURL := server.URL + "/v1/packages/sha256/" + strings.TrimPrefix(descriptor.Package.Digest, "sha256:") + "/package.tar"
	result, err := resolver.Resolve(context.Background(), digest, imageURL)
	if err != nil {
		t.Fatal(err)
	}
	if result.DescriptorDigest != digest || result.SignatureBundleDigest != Digest(bundle) || !bytes.Equal(result.DescriptorBytes, raw) || requests.Load() != 2 {
		t.Fatal("incomplete verification receipt")
	}
	for _, image := range []string{imageURL + "?token=secret", strings.Replace(imageURL, "https://", "http://", 1), "https://other.invalid/" + descriptor.Package.Digest, imageURL + "/../package.tar"} {
		if _, err := resolver.Resolve(context.Background(), digest, image); err == nil {
			t.Fatal("accepted unbound image URL")
		}
	}
}

func TestResolverRefusesRedirectsAndUnboundedResponses(t *testing.T) {
	_, pub := signingKey(t)
	raw := exampleDescriptor(t)
	digest := Digest(raw)
	var externalHits atomic.Int64
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalHits.Add(1) }))
	defer external.Close()
	for name, handler := range map[string]http.HandlerFunc{
		"redirect": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, external.URL, http.StatusFound) },
		"large length": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "9999999")
			w.WriteHeader(200)
		},
		"large chunked": func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(strings.Repeat("x", MaxDescriptorBytes+1)))
		},
		"encoding": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(raw)
		},
		"not found": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "secret-upstream-error", http.StatusNotFound)
		},
		"tampered descriptor": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(append(raw, ' ')) },
	} {
		t.Run(name, func(t *testing.T) {
			resolver, _ := newTestResolver(t, handler, pub)
			_, err := resolver.Resolve(context.Background(), digest, "https://irrelevant.invalid/package.tar")
			if err == nil {
				t.Fatal("accepted unsafe response")
			}
			if strings.Contains(err.Error(), "secret-upstream-error") {
				t.Fatal("upstream body leaked")
			}
		})
	}
	if externalHits.Load() != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestResolverRejectsUntrustedTLSAndWrongPlatform(t *testing.T) {
	_, pub := signingKey(t)
	raw := exampleDescriptor(t)
	digest := Digest(raw)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer server.Close()
	resolver, err := NewResolver(Config{Origin: server.URL, ProducerPublicKeys: []string{pub}, Platform: Platform{OS: "linux", Architecture: "amd64"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.CloseIdleConnections()
	if _, err := resolver.Resolve(context.Background(), digest, server.URL); err == nil {
		t.Fatal("accepted untrusted TLS")
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	wrong, err := NewResolver(Config{Origin: server.URL, ProducerPublicKeys: []string{pub}, Platform: Platform{OS: "linux", Architecture: "arm64"}, TLSConfig: &tls.Config{RootCAs: roots}})
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.CloseIdleConnections()
	if _, err := wrong.Resolve(context.Background(), digest, server.URL); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("wrong architecture: %v", err)
	}
}

func TestResolverConfigurationBoundaries(t *testing.T) {
	_, pub := signingKey(t)
	base := Config{Origin: "https://store.example.com", ProducerPublicKeys: []string{pub}, Platform: Platform{OS: "linux", Architecture: "amd64"}}
	for _, origin := range []string{"http://store.example.com", "https://user:secret@store.example.com", "https://store.example.com/path", "https://store.example.com?token=x", "https://store.example.com#x", "https://store.example.com/%2e%2e", "https://store.example.com?", "https://STORE.example.com", "https://store.example.com.", "https://store.example.com/#", "https://store.example.com:", "https://store.example.com:0", "https://store.example.com:65536", "https://" + strings.Repeat("a", 2048)} {
		config := base
		config.Origin = origin
		if _, err := NewResolver(config); err == nil {
			t.Fatalf("accepted origin %s", origin)
		}
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.TLSConfig = &tls.Config{InsecureSkipVerify: true} }, func(c *Config) { c.TLSConfig = &tls.Config{ServerName: "other.invalid"} }, func(c *Config) { c.MaxPackageBytes = -1 }, func(c *Config) { c.Timeout = 3 * time.Minute }, func(c *Config) { c.ProducerPublicKeys = nil }, func(c *Config) { c.ProducerPublicKeys = []string{pub, pub} }, func(c *Config) { c.Platform = Platform{} }} {
		config := base
		mutate(&config)
		if _, err := NewResolver(config); err == nil {
			t.Fatal("accepted unsafe config")
		}
	}
}
