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

package transport

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// InvokeRPC invokes one YANG RPC below the RESTCONF /operations root. It is an
// optional concrete capability rather than part of the base configuration
// transport.Interface: configuration writers must not acquire a generic RPC
// escape hatch, while narrowly scoped platform capabilities can type-assert it.
func (r *restconfTransport) InvokeRPC(ctx context.Context, path string, payload []byte) ([]byte, error) {
	if !validOperationPath(path) {
		return nil, fmt.Errorf("RESTCONF RPC path %q must be a plain /operations resource", path)
	}
	ctx, span := startTransportSpan(ctx, KindRESTCONF, "rpc", spanPath(path))
	defer span.End()
	return r.doOps(ctx, http.MethodPost, path, payload)
}

func validOperationPath(path string) bool {
	if !strings.HasPrefix(path, "/operations/") || len(path) == len("/operations/") {
		return false
	}
	if strings.ContainsAny(path, "?#\\") || strings.Contains(path, "//") {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
