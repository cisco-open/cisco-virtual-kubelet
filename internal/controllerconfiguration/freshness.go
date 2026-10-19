// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"time"
)

// ConfigurationGrantMaxAge bounds the interval from grant publication to a new
// controller submission. Native preparation immediately precedes publication
// in a separately bounded scan context. Lease renewal does not refresh it.
// It never bounds an already submitted task or permits releasing quarantine.
const ConfigurationGrantMaxAge = 2 * time.Minute

func ValidateGrantFreshness(r *ops.ControllerConfigurationReservation, now time.Time) error {
	if r == nil || now.IsZero() || r.Status.Grant == nil || r.Status.Grant.GrantedAt.IsZero() || r.Status.Grant.GrantedAt.After(now) || !now.Before(r.Status.Grant.GrantedAt.Add(ConfigurationGrantMaxAge)) {
		return errors.New("configuration grant requires fresh native workload preparation")
	}
	return nil
}
