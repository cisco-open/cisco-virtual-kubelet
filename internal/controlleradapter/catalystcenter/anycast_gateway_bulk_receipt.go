// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// One decoder is shared by retained-task selection and original-footprint
// verification. Legacy acknowledged cohorts remain observable, never writable.
type anycastBulkReceipt struct {
	version, start, total, count int
	mask                         []byte
	binding, cohort              string
}

func decodeAnycastBulkReceipt(resource string) (anycastBulkReceipt, error) {
	fail := func() (anycastBulkReceipt, error) { return anycastBulkReceipt{}, errConfigurationTransition }
	r := anycastBulkReceipt{}
	var fields []string
	switch {
	case strings.HasPrefix(resource, anycastBulkChunkPrefix):
		r.version = 2
		fields = strings.Split(strings.TrimPrefix(resource, anycastBulkChunkPrefix), ":")
		if len(fields) != 6 {
			return fail()
		}
		for i, target := range []*int{&r.start, &r.total, &r.count} {
			n, err := strconv.Atoi(fields[i])
			if err != nil || strconv.Itoa(n) != fields[i] {
				return fail()
			}
			*target = n
		}
		if r.total <= anycastBulkRequestLimit || r.total > 1000 || r.start < 0 || r.start >= r.total || r.start%anycastBulkRequestLimit != 0 || r.count != min(anycastBulkRequestLimit, r.total-r.start) {
			return fail()
		}
		r.cohort = fields[5]
		fields = fields[2:5]
	case strings.HasPrefix(resource, anycastBulkPrefix):
		r.version = 1
		fields = strings.Split(strings.TrimPrefix(resource, anycastBulkPrefix), ":")
		if len(fields) != 3 {
			return fail()
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil || strconv.Itoa(n) != fields[0] {
			return fail()
		}
		r.count = n
		r.total = n
	default:
		return fail()
	}
	if r.count < 1 || r.count > 1000 {
		return fail()
	}
	r.binding = fields[2]
	if !validTemplateComparisonDigest(r.binding) || r.version == 2 && !validTemplateComparisonDigest(r.cohort) {
		return fail()
	}

	mask, err := hex.DecodeString(fields[1])
	if err != nil || len(mask) != (r.count+7)/8 || hex.EncodeToString(mask) != fields[1] || r.count%8 != 0 && mask[len(mask)-1]>>uint(r.count%8) != 0 {
		return fail()
	}
	selected := false
	for _, b := range mask {
		selected = selected || b != 0
	}
	if !selected {
		return fail()
	}
	r.mask = mask
	return r, nil
}
func (r anycastBulkReceipt) legacy() string {
	return fmt.Sprintf("%s%d:%s:%s", anycastBulkPrefix, r.count, hex.EncodeToString(r.mask), r.binding)
}
