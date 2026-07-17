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

package writers

import (
	"errors"

	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
)

// ErrNotImplemented is returned by Phase-0 writer skeletons. It wraps the
// driver-level sentinel so callers can use errors.Is(err,
// configdriver.ErrNotImplemented) uniformly regardless of which package
// surfaced the failure.
var ErrNotImplemented = errors.Join(errUnimplementedWriter, configdriver.ErrNotImplemented)

// errUnimplementedWriter is the distinguishing leaf error so callers that
// care which layer was unimplemented can tell without string matching.
var errUnimplementedWriter = errors.New("iosxe writer: not implemented in Phase 0 scaffold")

type (
	SectionWriter  = enginewriters.SectionWriter
	PruneCapable   = enginewriters.PruneCapable
	KeyExtractable = enginewriters.KeyExtractable
)
