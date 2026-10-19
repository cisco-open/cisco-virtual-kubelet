// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestIPAMAllocation(t *testing.T) {
	for _, mode := range []string{"empty", "converged", "resize", "reparent", "global-overlap", "reservation-overlap", "orphan", "ambiguous", "disjoint"} {
		t.Run(mode, func(t *testing.T) {
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			pools, reservations := fixturePools(), fixtureReservations()
			switch mode {
			case "empty":
				pools, reservations = nil, nil
			case "resize":
				bits := 23
				intent.pools[0].Space.PrefixLength = &bits
			case "reparent":
				intent.reservations[0].V4.GlobalPoolID = "different"
			case "global-overlap":
				p := fixturePools()[0]
				p.Name, p.ID = "other", "other-id"
				pools = append(pools, p)
			case "reservation-overlap":
				r := fixtureReservations()[0]
				r.Name, r.ID = "other", "other-id"
				reservations = append(reservations, r)
			case "orphan":
				reservations[0].V4.GlobalPoolID = "missing"
			case "ambiguous":
				r := fixtureReservations()[0]
				r.SiteID, r.ID = "other-site", "other-id"
				reservations = append(reservations, r)
			case "disjoint":
				r := fixtureReservations()[0]
				r.Name, r.ID = "other", "other-id"
				r.V4.Subnet = "192.0.2.0"
				reservations = append(reservations, r)
			}
			err = validateIPAMAllocation(intent, pools, reservations)
			valid := mode == "empty" || mode == "converged" || mode == "disjoint"
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
		})
	}
}
