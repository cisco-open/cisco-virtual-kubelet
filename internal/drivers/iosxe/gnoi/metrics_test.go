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

package gnoi

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRPCOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: "ok"},
		{name: "unauthenticated", err: status.Error(codes.Unauthenticated, "redacted"), want: "unauthenticated"},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "redacted"), want: "permission_denied"},
		{name: "not provisioned", err: status.Error(codes.FailedPrecondition, "redacted"), want: "failed_precondition"},
		{name: "unimplemented", err: status.Error(codes.Unimplemented, "redacted"), want: "unimplemented"},
		{name: "deadline", err: status.Error(codes.DeadlineExceeded, "redacted"), want: "deadline_exceeded"},
		{name: "canceled", err: status.Error(codes.Canceled, "redacted"), want: "canceled"},
		{name: "unavailable", err: status.Error(codes.Unavailable, "redacted"), want: "unavailable"},
		{name: "other", err: errors.New("plain error"), want: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rpcOutcome(tt.err); got != tt.want {
				t.Fatalf("rpcOutcome()=%q, want %q", got, tt.want)
			}
		})
	}
}
