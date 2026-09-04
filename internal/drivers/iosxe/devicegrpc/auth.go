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

package devicegrpc

import (
	"context"

	"google.golang.org/grpc/credentials"
)

// NewIOSXEPasswordCredentials returns per-RPC credentials in the metadata
// shape expected by IOS XE's secure gNXI server when secure-password-auth is
// enabled. IOS XE expects separate lowercase username and password fields;
// this is deliberately not an HTTP Authorization header.
//
// The returned credentials require transport security so a future caller
// cannot accidentally disclose the password over a plaintext gRPC channel.
func NewIOSXEPasswordCredentials(username, password string) credentials.PerRPCCredentials {
	return iosxePasswordCredentials{
		username: username,
		password: password,
	}
}

type iosxePasswordCredentials struct {
	username string
	password string
}

func (c iosxePasswordCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"username": c.username,
		"password": c.password,
	}, nil
}

func (iosxePasswordCredentials) RequireTransportSecurity() bool { return true }
