// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");

package controller

import (
	"errors"
	"testing"
)

func TestIsRetryableRolloutPlanningError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "topology handoff guard", err: errors.New("target cat9k: bound Node still carries the topology initialization guard"), want: true},
		{name: "unready device is terminal planning failure", err: errors.New("target cat9k: device identity and topology are not ready"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableRolloutPlanningError(tt.err); got != tt.want {
				t.Fatalf("isRetryableRolloutPlanningError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}
