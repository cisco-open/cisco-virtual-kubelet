// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"testing"
	"time"
)

func TestGrantFreshnessIsIndependentOfRenewal(t *testing.T) {
	_, r, _, now := claimFixture(t)
	at := r.Status.Grant.GrantedAt.Time
	if err := ValidateGrantFreshness(r, now); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGrantFreshness(r, at.Add(ConfigurationGrantMaxAge)); err == nil {
		t.Fatal("expired preparation accepted")
	}
	if err := ValidateGrantFreshness(r, at.Add(-time.Second)); err == nil {
		t.Fatal("future grant accepted")
	}
	if err := ValidateGrantFreshness(nil, now); err == nil {
		t.Fatal("missing evidence accepted")
	}
}
