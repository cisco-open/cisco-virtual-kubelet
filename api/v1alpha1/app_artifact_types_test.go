// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
)

func artifactTestPublicKey(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func validArtifactConfig(t *testing.T) *AppArtifactConfig {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &AppArtifactConfig{Mode: AppArtifactModeVerified, StoreURL: "https://artifacts.example.test", ProducerPublicKeys: []string{artifactTestPublicKey(t, public)}, Platform: &AppArtifactPlatform{OS: "linux", Architecture: "amd64"}}
}

func TestAppArtifactStandardDefaults(t *testing.T) {
	for _, config := range []*AppArtifactConfig{nil, {}, {Mode: AppArtifactModeStandard}} {
		if err := config.Validate(); err != nil {
			t.Fatal(err)
		}
		if config.EffectiveMode() != AppArtifactModeStandard {
			t.Fatalf("unexpected mode: %q", config.EffectiveMode())
		}
		if config.EffectiveMaxPackageBytes() != 4<<30 {
			t.Fatal("unexpected default package limit")
		}
	}
	data, err := json.Marshal(&DeviceSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "appArtifacts") {
		t.Fatalf("omitted mode added configuration: %s", data)
	}
}

func TestAppArtifactConfigValidation(t *testing.T) {
	base := validArtifactConfig(t)
	cases := []struct {
		name   string
		change func(*AppArtifactConfig)
		want   string
	}{
		{"valid", func(*AppArtifactConfig) {}, ""},
		{"copy without pin", func(c *AppArtifactConfig) { c.Delivery = "Copy" }, "host key"},
		{"copy with pin", func(c *AppArtifactConfig) {
			c.Delivery = "Copy"
			c.SSHHostKeySHA256 = "SHA256:6a1Ts89cusFB7YnEyKujejZUTb11osqRyI8wBJ1MslA"
		}, ""},
		{"pin without copy", func(c *AppArtifactConfig) { c.SSHHostKeySHA256 = "SHA256:6a1Ts89cusFB7YnEyKujejZUTb11osqRyI8wBJ1MslA" }, "requires Copy"},
		{"unknown delivery", func(c *AppArtifactConfig) { c.Delivery = "SCP" }, "delivery"},
		{"explicit limit", func(c *AppArtifactConfig) { c.MaxPackageBytes = 1024 }, ""},
		{"trailing slash", func(c *AppArtifactConfig) { c.StoreURL += "/" }, ""},
		{"ipv6", func(c *AppArtifactConfig) { c.StoreURL = "https://[2001:db8::1]:8443" }, ""},
		{"unknown mode", func(c *AppArtifactConfig) { c.Mode = "Governed" }, "mode"},
		{"implicit standard with trust", func(c *AppArtifactConfig) { c.Mode = "" }, "require mode Verified"},
		{"standard with trust", func(c *AppArtifactConfig) { c.Mode = AppArtifactModeStandard }, "require mode Verified"},
		{"http", func(c *AppArtifactConfig) { c.StoreURL = "http://artifacts.example.test" }, "HTTPS origin"},
		{"missing store", func(c *AppArtifactConfig) { c.StoreURL = "" }, "HTTPS origin"},
		{"userinfo", func(c *AppArtifactConfig) { c.StoreURL = "https://user:password@artifacts.example.test" }, "HTTPS origin"},
		{"query", func(c *AppArtifactConfig) { c.StoreURL += "?token=secret" }, "HTTPS origin"},
		{"empty query", func(c *AppArtifactConfig) { c.StoreURL += "?" }, "HTTPS origin"},
		{"fragment", func(c *AppArtifactConfig) { c.StoreURL += "#" }, "HTTPS origin"},
		{"base path", func(c *AppArtifactConfig) { c.StoreURL += "/objects" }, "HTTPS origin"},
		{"encoded path", func(c *AppArtifactConfig) { c.StoreURL += "/%2e%2e" }, "HTTPS origin"},
		{"invalid host", func(c *AppArtifactConfig) { c.StoreURL = "https://bad_host" }, "HTTPS origin"},
		{"uppercase host", func(c *AppArtifactConfig) { c.StoreURL = "https://Artifacts.example.test" }, "lowercase"},
		{"trailing host dot", func(c *AppArtifactConfig) { c.StoreURL = "https://artifacts.example.test." }, "HTTPS origin"},
		{"invalid port", func(c *AppArtifactConfig) { c.StoreURL += ":65536" }, "port"},
		{"empty port", func(c *AppArtifactConfig) { c.StoreURL += ":" }, "HTTPS origin"},
		{"missing platform", func(c *AppArtifactConfig) { c.Platform = nil }, "platform"},
		{"non linux", func(c *AppArtifactConfig) { c.Platform.OS = "windows" }, "platform"},
		{"missing architecture", func(c *AppArtifactConfig) { c.Platform.Architecture = "" }, "platform"},
		{"invalid variant", func(c *AppArtifactConfig) { c.Platform.Variant = "../v8" }, "platform"},
		{"negative limit", func(c *AppArtifactConfig) { c.MaxPackageBytes = -1 }, "maxPackageBytes"},
		{"oversize limit", func(c *AppArtifactConfig) { c.MaxPackageBytes = MaxAppArtifactPackageBytes + 1 }, "maxPackageBytes"},
		{"missing keys", func(c *AppArtifactConfig) { c.ProducerPublicKeys = nil }, "producerPublicKeys"},
		{"many keys", func(c *AppArtifactConfig) { c.ProducerPublicKeys = make([]string, 9) }, "producerPublicKeys"},
		{"duplicate keys", func(c *AppArtifactConfig) {
			c.ProducerPublicKeys = append(c.ProducerPublicKeys, c.ProducerPublicKeys[0])
		}, "duplicates"},
		{"private block", func(c *AppArtifactConfig) {
			c.ProducerPublicKeys[0] = strings.Replace(c.ProducerPublicKeys[0], "PUBLIC KEY", "PRIVATE KEY", -1)
		}, "PUBLIC KEY"},
		{"multiple PEM blocks", func(c *AppArtifactConfig) { c.ProducerPublicKeys[0] += c.ProducerPublicKeys[0] }, "exactly one"},
		{"trailing junk", func(c *AppArtifactConfig) { c.ProducerPublicKeys[0] += "junk" }, "exactly one"},
		{"leading junk", func(c *AppArtifactConfig) { c.ProducerPublicKeys[0] = "junk\n" + c.ProducerPublicKeys[0] }, "PUBLIC KEY"},
		{"oversize key", func(c *AppArtifactConfig) {
			c.ProducerPublicKeys[0] = strings.Repeat("x", MaxAppArtifactPublicKeyBytes+1)
		}, "bounded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base.DeepCopy()
			tc.change(c)
			err := c.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "token=secret") {
				t.Fatal("validation leaked URL credentials")
			}
		})
	}
}

func TestAppArtifactPublicKeyAlgorithms(t *testing.T) {
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		c := validArtifactConfig(t)
		c.ProducerPublicKeys = []string{artifactTestPublicKey(t, &key.PublicKey)}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, bits := range []int{1024, 2048} {
		key, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatal(err)
		}
		c := validArtifactConfig(t)
		c.ProducerPublicKeys = []string{artifactTestPublicKey(t, &key.PublicKey)}
		err = c.Validate()
		if (bits < 2048) != (err != nil) {
			t.Fatalf("RSA %d validation=%v", bits, err)
		}
	}
	// Public-key validation should reject unsupported sizes before the signature
	// library is constructed, including sizes larger than the supported defaults.
	for _, bits := range []int{2049, 8192} {
		key := &rsa.PublicKey{N: new(big.Int).SetBit(big.NewInt(1), bits-1, 1), E: 65537}
		c := validArtifactConfig(t)
		c.ProducerPublicKeys = []string{artifactTestPublicKey(t, key)}
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted unsupported RSA key size %d", bits)
		}
	}
}

func TestAppArtifactDeepCopy(t *testing.T) {
	original := &DeviceSpec{AppArtifacts: validArtifactConfig(t)}
	copy := original.DeepCopy()
	copy.AppArtifacts.ProducerPublicKeys[0] = "changed"
	copy.AppArtifacts.Platform.Architecture = "arm64"
	if original.AppArtifacts.ProducerPublicKeys[0] == "changed" || original.AppArtifacts.Platform.Architecture != "amd64" {
		t.Fatal("deep copy aliases artifact trust configuration")
	}
}
