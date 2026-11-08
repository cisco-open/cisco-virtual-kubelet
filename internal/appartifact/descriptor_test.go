// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func exampleDescriptor(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../docs/contracts/examples/app-hosting-artifact-v1alpha1.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDescriptorExactIdentity(t *testing.T) {
	raw := exampleDescriptor(t)
	descriptor, err := DecodeDescriptor(raw, Digest(raw), 0)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Source.Platform.Architecture != "amd64" || descriptor.Package.SizeBytes != 104857600 {
		t.Fatal("wrong descriptor")
	}
	if _, err := DecodeDescriptor(append(append([]byte{}, raw...), '\n'), Digest(raw), 0); err == nil {
		t.Fatal("accepted changed exact bytes")
	}
	if _, err := DecodeDescriptor(raw, "sha256:"+strings.Repeat("f", 64), 0); err == nil {
		t.Fatal("accepted wrong digest")
	}
	if _, err := DecodeDescriptor(raw, Digest(raw), 1024); err == nil {
		t.Fatal("accepted oversized package")
	}
}

func TestDescriptorRejectsAmbiguousAndMalformedJSON(t *testing.T) {
	raw := string(exampleDescriptor(t))
	cases := map[string]string{
		"duplicate root":    strings.Replace(raw, `"kind":`, `"kind":"AppHostingArtifact","kind":`, 1),
		"escaped duplicate": strings.Replace(raw, `"kind":`, `"ki\u006ed":"AppHostingArtifact","kind":`, 1),
		"nested duplicate":  strings.Replace(raw, `"architecture":`, `"architecture":"arm64","architecture":`, 1),
		"unknown":           strings.Replace(raw, `"kind":`, `"approved":true,"kind":`, 1),
		"case alias":        strings.Replace(raw, `"kind":`, `"Kind":`, 1),
		"missing":           strings.Replace(raw, `"kind": "AppHostingArtifact",`, "", 1),
		"null optional":     strings.Replace(raw, `"releaseLabel": "1.8.3"`, `"releaseLabel":null`, 1),
		"empty optional":    strings.Replace(raw, `"releaseLabel": "1.8.3"`, `"releaseLabel":""`, 1),
		"trailing":          raw + ` {}`,
		"utf8":              strings.Replace(raw, "edge-agent", "edge-\xff", 1),
		"nested":            strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20),
		"oversize":          strings.Repeat(" ", MaxDescriptorBytes) + raw,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeDescriptor([]byte(input), Digest([]byte(input)), 0); err == nil {
				t.Fatal("accepted malformed descriptor")
			}
		})
	}
}

func TestDescriptorSemantics(t *testing.T) {
	raw := exampleDescriptor(t)
	cases := map[string]func(*Descriptor){
		"version":              func(d *Descriptor) { d.APIVersion = "apphosting.cisco.com/v99" },
		"digest algorithm":     func(d *Descriptor) { d.Package.Digest = "sha512:" + strings.Repeat("a", 64) },
		"index mismatch":       func(d *Descriptor) { d.Source.RequestedDigest = d.Source.ManifestDigest },
		"direct mismatch":      func(d *Descriptor) { d.Source.IndexDigest = "" },
		"missing architecture": func(d *Descriptor) { d.Source.Platform.Architecture = "" },
		"os":                   func(d *Descriptor) { d.Source.Platform.OS = "windows" },
		"variant":              func(d *Descriptor) { d.Source.Platform.Variant = "../arm" },
		"format":               func(d *Descriptor) { d.Package.Format = "oci-layout" },
		"media type":           func(d *Descriptor) { d.Package.MediaType = "application/gzip" },
		"negative size":        func(d *Descriptor) { d.Package.SizeBytes = -1 },
		"timestamp":            func(d *Descriptor) { d.CreatedAt = "yesterday" },
		"registry credentials": func(d *Descriptor) { d.Source.Registry = "user:secret@registry.example.com" },
		"registry query":       func(d *Descriptor) { d.Source.Registry = "registry.example.com?secret=x" },
		"repository traversal": func(d *Descriptor) { d.Source.Repository = "vendor/../agent" },
		"producer size":        func(d *Descriptor) { d.Producer.Name = strings.Repeat("a", 257) },
		"sbom digest":          func(d *Descriptor) { d.SBOM.Digest = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var d Descriptor
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			change(&d)
			data, _ := json.Marshal(d)
			if _, err := DecodeDescriptor(data, Digest(data), 0); err == nil {
				t.Fatal("accepted invalid descriptor")
			}
		})
	}
	var d Descriptor
	_ = json.Unmarshal(raw, &d)
	d.Source.IndexDigest = ""
	d.Source.RequestedDigest = d.Source.ManifestDigest
	d.SBOM = nil
	data, _ := json.Marshal(d)
	if _, err := DecodeDescriptor(data, Digest(data), 0); err != nil {
		t.Fatalf("valid direct manifest: %v", err)
	}
}

func TestBindingsRequireExactContainerSet(t *testing.T) {
	digest := Digest([]byte("descriptor"))
	raw := `{"agent":{"descriptorDigest":"` + digest + `"}}`
	if _, err := ParseBindings(raw, []string{"agent"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		raw   string
		names []string
	}{
		{raw, []string{"other"}}, {raw, []string{"agent", "second"}}, {raw, []string{"agent", "agent"}},
		{`{"agent":{"descriptorDigest":"` + digest + `","releaseID":"123"}}`, []string{"agent"}},
		{`{"agent":{"descriptorDigest":"` + digest + `"},"agent":{"descriptorDigest":"` + digest + `"}}`, []string{"agent"}},
		{`{"agent":{"DescriptorDigest":"` + digest + `"}}`, []string{"agent"}},
		{`{"agent":null}`, []string{"agent"}}, {`null`, []string{"agent"}},
	}
	for _, test := range cases {
		if _, err := ParseBindings(test.raw, test.names); err == nil {
			t.Fatalf("accepted invalid bindings %q", test.raw)
		}
	}
}
