// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var catalystNumericRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[0-9]+(?:\.[0-9]+)*)?$`)

func supportsAnycastSecondaryPools(version string) bool {
	if len(version) > 128 || !catalystNumericRelease.MatchString(version) {
		return false
	}
	parts := strings.Split(strings.SplitN(version, "-", 2)[0], ".")
	numbers := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		numbers[i] = n
	}
	return numbers[0] > 3 || numbers[0] == 3 && (numbers[1] > 2 || numbers[1] == 2 && numbers[2] >= 3)
}

// This is the pinned provider's documented minimum, not live qualification or
// proof of authorization. Return exact release evidence for future journal
// dependency binding and recheck it at submission rather than caching forever.
func (c *client) anycastCapabilityEvidence(ctx context.Context, want anycastGatewayIntent) (string, error) {
	if !want.needsSecondaryPools {
		return "", nil
	}
	version, err := c.installedRelease(ctx)
	if err != nil {
		return "", err
	}
	if !supportsAnycastSecondaryPools(version) {
		return "", errors.New("secondary anycast IP pools require Catalyst Center 3.2.3 or later with a recognized release format")
	}
	return version, nil
}
