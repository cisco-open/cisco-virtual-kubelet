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

package telemetry

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// SubscribeClientFactory creates the dedicated gRPC client used by the
// telemetry subscriber. Implementations must return a connection owned by the
// caller; telemetry never borrows the configdriver gNMI transport connection.
type SubscribeClientFactory interface {
	NewClient(ctx context.Context) (*SubscribeClient, error)
}

// SubscribeClient wraps the gNMI client and the per-call authentication hook.
type SubscribeClient struct {
	Conn        *grpc.ClientConn
	Client      gpb.GNMIClient
	AuthContext func(context.Context) context.Context
}

// DefaultSubscribeClientFactory builds a fresh *grpc.ClientConn using the same
// TLS and HTTP Basic auth shape as the configdriver gNMI transport.
type DefaultSubscribeClientFactory struct {
	Config      transport.GNMIConfig
	DialOptions []grpc.DialOption
}

func NewDefaultSubscribeClientFactory(cfg transport.GNMIConfig) *DefaultSubscribeClientFactory {
	return &DefaultSubscribeClientFactory{Config: cfg}
}

func NewDefaultSubscribeClientFactoryForDevice(spec *ciskov1.DeviceSpec, password string) (*DefaultSubscribeClientFactory, error) {
	cfg, err := GNMIConfigFromDeviceSpec(spec, password)
	if err != nil {
		return nil, err
	}
	return NewDefaultSubscribeClientFactory(cfg), nil
}

func GNMIConfigFromDeviceSpec(spec *ciskov1.DeviceSpec, password string) (transport.GNMIConfig, error) {
	if spec == nil {
		return transport.GNMIConfig{}, errors.New("telemetry factory: nil DeviceSpec")
	}
	if spec.Address == "" {
		return transport.GNMIConfig{}, errors.New("telemetry factory: CiscoDevice.spec.address empty")
	}
	tlsEnabled := spec.TLS != nil && spec.TLS.Enabled
	// Telemetry-path TLS override. CISCO_VK_TELEMETRY_INSECURE forces the
	// telemetry gNMI client to dial the insecure listener regardless of the
	// CiscoDevice.spec.tls setting that drives the config-driver's RESTCONF
	// transport. CISCO_VK_TELEMETRY_PORT pins an explicit port. Both knobs
	// exist so operators can route telemetry through gnxi-server on 50052
	// while leaving RESTCONF/NETCONF on TLS for the config-driver.
	if v := os.Getenv("CISCO_VK_TELEMETRY_INSECURE"); v == "1" || strings.EqualFold(v, "true") {
		tlsEnabled = false
	}
	port := spec.Port
	if v := os.Getenv("CISCO_VK_TELEMETRY_PORT"); v != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
			port = p
		}
	}
	if port == 0 || port == 80 || port == 443 {
		if tlsEnabled {
			port = 9339
		} else {
			port = 50052
		}
	}
	cfg := transport.GNMIConfig{
		Address:  spec.Address,
		Port:     port,
		Username: spec.Username,
		Password: password,
	}
	if tlsEnabled {
		cfg.TLSConfig = tlsConfigFromDeviceSpec(spec)
	}
	return cfg, nil
}

func (f *DefaultSubscribeClientFactory) NewClient(_ context.Context) (*SubscribeClient, error) {
	cfg := f.Config
	if cfg.Address == "" {
		return nil, errors.New("telemetry factory: address empty")
	}
	target := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
	var creds credentials.TransportCredentials
	if cfg.TLSConfig != nil {
		creds = credentials.NewTLS(cfg.TLSConfig)
	} else {
		creds = insecure.NewCredentials()
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	opts = append(opts, f.DialOptions...)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry gNMI dial %s: %w", target, err)
	}
	return &SubscribeClient{
		Conn:        conn,
		Client:      gpb.NewGNMIClient(conn),
		AuthContext: authContextFunc(cfg.Username, cfg.Password),
	}, nil
}

func authContextFunc(username, password string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		if username == "" {
			return ctx
		}
		creds := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		return metadata.AppendToOutgoingContext(ctx, "authorization", "Basic "+creds)
	}
}

func tlsConfigFromDeviceSpec(spec *ciskov1.DeviceSpec) *tls.Config {
	if spec.TLS == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: spec.TLS.InsecureSkipVerify,
	}
}
