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

package iosxe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// UnmarshalFunc defines a function signature for unmarshalling data
type UnmarshalFunc func([]byte, any) error

// XEDriver implements the device driver for Cisco IOS-XE AppHosting
type XEDriver struct {
	config       *config.DeviceConfig
	client       common.NetworkClient
	marshaller   func(any) ([]byte, error)
	unmarshaller UnmarshalFunc
}

// NewAppHostingDriver creates a new IOS-XE AppHosting driver instance
func NewAppHostingDriver(ctx context.Context, config *config.DeviceConfig) (*XEDriver, error) {
	u := &url.URL{
		Host: fmt.Sprintf("%s:%d", config.Address, config.Port),
	}

	if config.TLSConfig.Enabled {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}

	if config.TLSConfig != nil {
		tlsConfig.InsecureSkipVerify = config.TLSConfig.InsecureSkipVerify

		if config.TLSConfig.CertFile != "" && config.TLSConfig.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(config.TLSConfig.CertFile, config.TLSConfig.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client certificate: %v", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		if config.TLSConfig.CAFile != "" {
			caCert, err := os.ReadFile(config.TLSConfig.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %v", err)
			}
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}
	}

	BaseUrl := u.String()
	Timeout := 30 * time.Second
	Client, err := common.NewNetworkClient(
		BaseUrl,
		&common.ClientAuth{
			Method:   "BasicAuth",
			Username: config.Username,
			Password: config.Password,
		},
		tlsConfig,
		Timeout,
	)

	d := &XEDriver{
		config: config,
		client: Client,
	}

	protocol := "restconf"
	if protocol == "restconf" {
		d.marshaller = d.getRestconfMarshaller()
		d.unmarshaller = d.getRestconfUnmarshaller()
	}

	err = d.CheckConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate device connection: %v", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"url":      BaseUrl,
		"platform": "IOS-XE",
	}).Info("Connected to IOSXE device")

	return d, nil
}

// gethostMetaUnmarshaller returns an unmarshaller for host-meta XML responses
func (d *XEDriver) gethostMetaUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.Strict = false
		return decoder.Decode(v)
	}
}

// getRestconfMarshaller returns a marshaller for RESTCONF JSON payloads using ygot
func (d *XEDriver) getRestconfMarshaller() func(any) ([]byte, error) {
	return func(v any) ([]byte, error) {
		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return nil, fmt.Errorf("value is not a ygot.GoStruct")
		}
		jsonStr, err := ygot.EmitJSON(gs, &ygot.EmitJSONConfig{
			Format: ygot.RFC7951,
			RFC7951Config: &ygot.RFC7951JSONConfig{
				AppendModuleName: true,
			},
			SkipValidation: true,
		})
		return []byte(jsonStr), err
	}
}

// getRestconfUnmarshaller returns an unmarshaller for RESTCONF JSON responses using ygot
func (d *XEDriver) getRestconfUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse JSON wrapper: %w", err)
		}

		var innerData []byte
		if len(wrapper) == 1 {
			for _, val := range wrapper {
				innerData = val
			}
		} else {
			innerData = data
		}

		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return fmt.Errorf("target is not a ygot.GoStruct")
		}

		return Unmarshal(innerData, gs)
	}
}

// CheckConnection validates connectivity to the device
func (d *XEDriver) CheckConnection(ctx context.Context) error {
	res := &common.HostMeta{}

	err := d.client.Get(ctx, "/.well-known/host-meta", res, d.gethostMetaUnmarshaller())
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	log.G(ctx).Debugf("Restconf Root: %s\n", res.Links[0].Href)
	return nil
}

// GetDeviceResources returns the available resources on the device
func (d *XEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}

// debugLogJson logs a ygot struct as formatted JSON for debugging
func (d *XEDriver) debugLogJson(ctx context.Context, obj ygot.GoStruct) error {
	jsonStr, err := ygot.EmitJSON(obj, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		Indent: "  ",
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize ygot object: %w", err)
	}

	log.G(ctx).Debug(jsonStr)
	return nil
}
