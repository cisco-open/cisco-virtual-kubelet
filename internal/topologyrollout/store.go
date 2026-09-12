// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package topologyrollout

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const LedgerDataKey = "ledger.json"

// Store performs uncached reads and resourceVersion-protected updates of the
// one ConfigMap ledger. ExpectedUID must come from the independently persisted
// administrator policy; a missing or recreated object fails closed.
type Store struct {
	Client      client.Client
	APIReader   client.Reader
	Key         types.NamespacedName
	ExpectedUID types.UID
}

func (s Store) Read(ctx context.Context) (*corev1.ConfigMap, *Ledger, error) {
	if s.Client == nil || s.ExpectedUID == "" || s.Key.Name == "" || s.Key.Namespace == "" {
		return nil, nil, fmt.Errorf("%w: store is incomplete", ErrLedgerIdentity)
	}
	reader := s.APIReader
	if reader == nil {
		reader = s.Client
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, s.Key, &cm); err != nil {
		return nil, nil, fmt.Errorf("read rollout ledger: %w", err)
	}
	if cm.UID != s.ExpectedUID {
		return nil, nil, fmt.Errorf("%w: object UID=%q expected=%q", ErrLedgerIdentity, cm.UID, s.ExpectedUID)
	}
	raw := []byte(cm.Data[LedgerDataKey])
	ledger, err := Decode(raw, string(s.ExpectedUID))
	if err != nil {
		return nil, nil, err
	}
	observeLedger(ledger, len(raw))
	return &cm, ledger, nil
}

// Mutate retries only Kubernetes resourceVersion conflicts. The callback is
// rerun against each fresh ledger and therefore must fully revalidate policy,
// health and control revisions rather than capturing a prior authorization.
func (s Store) Mutate(ctx context.Context, mutate func(*Ledger) error) error {
	if mutate == nil {
		return fmt.Errorf("rollout ledger mutation callback is required")
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		cm, ledger, err := s.Read(ctx)
		if err != nil {
			return err
		}
		if err := mutate(ledger); err != nil {
			return err
		}
		if err := validatePersistedLedger(ledger); err != nil {
			return err
		}
		if ledger.UID != string(s.ExpectedUID) {
			return fmt.Errorf("%w: mutation changed ledger UID", ErrLedgerIdentity)
		}
		// Reserve enforces the current administrator size ceiling before it
		// adds a record. Safety transitions (bind, revoke, settle) must remain
		// writable if an administrator later tightens that ceiling below the
		// already-persisted ledger size; otherwise a size-policy change could
		// prevent cancellation. The absolute format ceiling still applies.
		encoded, err := Encode(ledger, DefaultMaxSerializedBytes)
		if err != nil {
			return err
		}
		if cm.Data[LedgerDataKey] == string(encoded) {
			return nil
		}
		before := cm.DeepCopy()
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[LedgerDataKey] = string(encoded)
		if err := s.Client.Patch(ctx, cm, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			if apierrors.IsConflict(err) {
				return err
			}
			return fmt.Errorf("update rollout ledger: %w", err)
		}
		observeLedger(ledger, len(encoded))
		return nil
	})
}
