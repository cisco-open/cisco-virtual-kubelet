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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

var (
	_ fmt.Formatter = iosxePasswordCredentials{}
	_ fmt.Formatter = (*iosxePasswordCredentials)(nil)
)

func TestIOSXEPasswordCredentialsMetadata(t *testing.T) {
	creds := NewIOSXEPasswordCredentials("admin", "p@ss:word")

	got, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	want := map[string]string{
		"username": "admin",
		"password": "p@ss:word",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
	if _, ok := got["authorization"]; ok {
		t.Fatal("metadata unexpectedly contains an authorization header")
	}
	if !creds.RequireTransportSecurity() {
		t.Fatal("IOS XE password credentials must require transport security")
	}
}

func TestIOSXEPasswordCredentialsFormattingRedactsSecrets(t *testing.T) {
	value := iosxePasswordCredentials{username: "sensitive-username", password: "sensitive-password"}
	const want = "iosxePasswordCredentials{REDACTED}"
	operands := []any{
		value,
		&value,
		NewIOSXEPasswordCredentials("sensitive-public-username", "sensitive-public-password"),
	}
	for _, operand := range operands {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
			if got := fmt.Sprintf(format, operand); got != want {
				t.Errorf("Sprintf(%q, %T)=%q, want %q", format, operand, got, want)
			}
		}
	}
}

func TestDefaultDialRejectsPasswordCredentialsWithoutTLS(t *testing.T) {
	conn, err := defaultDial(context.Background(), "unused:50052", DialConfig{
		RPCCredentials: NewIOSXEPasswordCredentials("admin", "secret"),
	})
	if conn != nil {
		_ = conn.Close()
		t.Fatal("defaultDial returned a connection for plaintext password credentials")
	}
	if err == nil || !strings.Contains(err.Error(), "per-RPC credentials require TLS") {
		t.Fatalf("defaultDial error = %v, want TLS-required error", err)
	}
}

func TestDefaultDialSendsIOSXEPasswordCredentialsOnUnaryAndStream(t *testing.T) {
	serverTLS, clientTLS := testTLSConfigs(t)
	lis := bufconn.Listen(1 << 20)

	var unaryCalls atomic.Int64
	var streamCalls atomic.Int64
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := validateIOSXEPasswordMetadata(ctx, "admin", "p@ss:word"); err != nil {
				return nil, err
			}
			unaryCalls.Add(1)
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := validateIOSXEPasswordMetadata(stream.Context(), "admin", "p@ss:word"); err != nil {
				return err
			}
			streamCalls.Add(1)
			return handler(srv, stream)
		}),
	)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthServer)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := defaultDial(context.Background(), "passthrough:///bufconn", DialConfig{
		TLSConfig:      clientTLS,
		RPCCredentials: NewIOSXEPasswordCredentials("admin", "p@ss:word"),
		Extra: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})
	if err != nil {
		t.Fatalf("defaultDial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := healthpb.NewHealthClient(conn)
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("unary health Check: %v", err)
	}
	watch, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("streaming health Watch: %v", err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatalf("streaming health Watch receive: %v", err)
	}
	if got := unaryCalls.Load(); got != 1 {
		t.Fatalf("authenticated unary calls = %d, want 1", got)
	}
	if got := streamCalls.Load(); got != 1 {
		t.Fatalf("authenticated streaming calls = %d, want 1", got)
	}
}

func validateIOSXEPasswordMetadata(ctx context.Context, username, password string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing incoming metadata")
	}
	if got := md.Get("username"); !reflect.DeepEqual(got, []string{username}) {
		return status.Errorf(codes.Unauthenticated, "username metadata = %#v", got)
	}
	if got := md.Get("password"); !reflect.DeepEqual(got, []string{password}) {
		return status.Errorf(codes.Unauthenticated, "password metadata = %#v", got)
	}
	if got := md.Get("authorization"); len(got) != 0 {
		return status.Errorf(codes.Unauthenticated, "unexpected authorization metadata = %#v", got)
	}
	return nil
}

func testTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate TLS key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create TLS certificate: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse TLS certificate: %v", err)
	}
	roots.AddCert(parsed)
	return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, &tls.Config{
			RootCAs:    roots,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		}
}
