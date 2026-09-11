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

package softwareupgrade_test

import (
	"errors"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
)

func TestRetryableResolveErrorPublicContract(t *testing.T) {
	cause := errors.New("temporary resolver failure")
	marked := softwareupgrade.MarkRetryableResolveError(cause)
	if !softwareupgrade.IsRetryableResolveError(marked) {
		t.Fatalf("marked error = %v, want retryable classification", marked)
	}
	if !errors.Is(marked, cause) {
		t.Fatalf("marked error = %v, want original cause", marked)
	}
	if got := softwareupgrade.MarkRetryableResolveError(marked); got != marked {
		t.Fatal("marking a retryable error was not idempotent")
	}
	if got := softwareupgrade.MarkRetryableResolveError(nil); got != nil {
		t.Fatalf("marking nil returned %v", got)
	}
}
