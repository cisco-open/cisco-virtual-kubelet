// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSynchronousConfigurationPreflightProof(t *testing.T) {
	for _, mode := range []string{"completed", "refused", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			write := &configurationWrite{resource: "floor-image-sync:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), action: "Update", identity: configurationResourceIdentity{target: "floor"}, submitCompleted: func(context.Context) error {
				calls++
				if mode == "lost-response" {
					return errInvalidResponse
				}
				return nil
			}}
			guardConfigurationPreflight(write, func(context.Context) error {
				if mode == "refused" {
					return errConfigurationStale
				}
				return nil
			})
			if write.submit != nil || !noDispatchWriteSupported("sites", write) {
				t.Fatal("synchronous sender or refusal scope changed")
			}
			err := write.submitCompleted(context.Background())
			proof := write.noDispatch(err)
			if mode == "refused" {
				if !errors.Is(err, errConfigurationStale) || proof == nil || calls != 0 {
					t.Fatal("preflight refusal lost its no-dispatch proof")
				}
			} else if proof != nil || calls != 1 || (err != nil) != (mode == "lost-response") {
				t.Fatal("native outcome was mistaken for refusal")
			}
			if write.noDispatch(err) != nil {
				t.Fatal("proof was claimed twice")
			}
			if !errors.Is(write.submitCompleted(context.Background()), errConfigurationTransition) || calls > 1 {
				t.Fatal("sender was replayed")
			}
		})
	}
}
