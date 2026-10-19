// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"
)

func TestBootstrapMetadataFenceFingerprint(t *testing.T) {
	policy, _, _ := bootstrapProtectionFixture(t)
	digest, err := bootstrapProtectionDigest(policy, bootstrapTestManager, "bootstrap-leases")
	if err != nil {
		t.Fatal(err)
	}
	if digest != bootstrapProtectionSpecDigest {
		t.Fatal("rendered fence policy differs from compiled contract", digest)
	}
}

func TestLegacyBootstrapCannotAdoptMetadataFence(t *testing.T) {
	c, nc, _ := bootstrapFenceFixture(t)
	identity, err := ReadBootstrapIdentity(context.Background(), c, nc)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := bootstrapFenceCandidate("leases", nc.Namespace, identity, bootstrapFenceFixtureWorker(t, c, nc), bootstrapFenceTestInstallation(), bootstrapFenceFixtureProtection(t, c))
	if err != nil {
		t.Fatal(err)
	}
	candidate.UID = "retained"
	candidate.ResourceVersion = "1"
	if !sameBootstrapFence(candidate, candidate.DeepCopy()) {
		t.Fatal("legacy candidate lost")
	}
	candidate.Labels[metadataFenceLabel] = MetadataFenceProtocol
	if sameBootstrapFence(candidate, candidate.DeepCopy()) {
		t.Fatal("legacy comparator adopted metadata custody")
	}
}

func TestHistoricalBootstrapFenceRemainsReadable(t *testing.T) {
	c, nc, _ := bootstrapFenceFixture(t)
	identity, err := ReadBootstrapIdentity(context.Background(), c, nc)
	if err != nil {
		t.Fatal(err)
	}
	protection := bootstrapFenceFixtureProtection(t, c)
	protection.PolicyDigest = bootstrapLegacyProtectionSpecDigest
	candidate, err := bootstrapFenceCandidate("leases", nc.Namespace, identity, bootstrapFenceFixtureWorker(t, c, nc), bootstrapFenceTestInstallation(), protection)
	if err != nil {
		t.Fatal(err)
	}
	candidate.UID = "historical"
	if err := c.Create(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	retained, err := ReadBootstrapFence(context.Background(), c, "leases", identity.PhysicalIdentity)
	if err != nil || retained == nil || retained.UID != "historical" {
		t.Fatal("historical retained evidence lost", err)
	}
}
