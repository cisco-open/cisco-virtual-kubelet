// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package gnoi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCertificateInventoryValidity(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"PEM", pki.leafPEM, true},
		{"DER", pki.leaf.Raw, true},
		{"malformed", []byte("invalid certificate"), false},
		{"missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.Cert.getResp = certificateInventory("cvk-identity", tc.data)
			got, err := srv.client(t).GetCertificates(context.Background())
			if err != nil || len(got) != 1 {
				t.Fatalf("inventory: %v, %v", got, err)
			}
			if !tc.valid {
				if got[0].NotAfter != nil || got[0].NotBefore != nil || got[0].FingerprintSHA256 != "" {
					t.Fatal("invented validity for an unparseable certificate")
				}
				return
			}
			if got[0].NotAfter == nil || !got[0].NotAfter.Equal(pki.leaf.NotAfter) ||
				got[0].NotBefore == nil || !got[0].NotBefore.Equal(pki.leaf.NotBefore) {
				t.Fatal("certificate validity differs from the installed leaf")
			}
			if got[0].FingerprintSHA256 != fmt.Sprintf("%x", sha256.Sum256(pki.leaf.Raw)) {
				t.Fatal("fingerprint is not the DER SHA-256 digest")
			}
		})
	}
	unsupported := CertificateInfo{Type: certpb.CertificateType_CT_UNKNOWN.String(), Certificate: pki.leaf.Raw}
	unsupported.parseValidity()
	if unsupported.NotAfter != nil {
		t.Fatal("interpreted an unsupported certificate type as X.509")
	}
}

func TestCertificateInventoryMetricsRefreshAndEmptyInventory(t *testing.T) {
	RegisterMetrics(prometheus.NewRegistry())
	observed := time.Now()
	soon := observed.Add(24 * time.Hour)
	later := observed.Add(365 * 24 * time.Hour)
	recordCertificateInventory([]CertificateInfo{{NotAfter: &later}, {}, {NotAfter: &soon}}, observed)
	if testutil.ToFloat64(certificateEarliestExpiry) != float64(soon.Unix()) ||
		testutil.ToFloat64(certificateInventoryUnparsed) != 1 ||
		testutil.ToFloat64(certificateInventoryObserved) != float64(observed.Unix()) {
		t.Fatal("inventory metrics did not represent earliest expiry, invalid count, and freshness")
	}
	emptyObserved := time.Now()
	recordCertificateInventory(nil, emptyObserved)
	if testutil.ToFloat64(certificateEarliestExpiry) != 0 || testutil.ToFloat64(certificateInventoryUnparsed) != 0 ||
		testutil.ToFloat64(certificateInventoryObserved) != float64(emptyObserved.Unix()) {
		t.Fatal("empty inventory retained stale certificate values")
	}
	recordCertificateInventory([]CertificateInfo{{NotAfter: &soon}}, observed)
	if testutil.ToFloat64(certificateEarliestExpiry) != 0 {
		t.Fatal("older observation overwrote the latest inventory")
	}
}

func TestConcurrentCertificateInventoriesKeepNewestObservation(t *testing.T) {
	RegisterMetrics(prometheus.NewRegistry())
	latest := time.Now()
	expiry := latest.Add(24 * time.Hour)
	var callers sync.WaitGroup
	for i := 0; i < 100; i++ {
		callers.Add(1)
		go func(offset int) {
			defer callers.Done()
			certs := []CertificateInfo{{NotAfter: &expiry}}
			if offset != 0 {
				certs = []CertificateInfo{{}}
			}
			recordCertificateInventory(certs, latest.Add(-time.Duration(offset)*time.Second))
		}(i)
	}
	callers.Wait()
	if testutil.ToFloat64(certificateEarliestExpiry) != float64(expiry.Unix()) ||
		testutil.ToFloat64(certificateInventoryObserved) != float64(latest.Unix()) ||
		testutil.ToFloat64(certificateInventoryUnparsed) != 0 {
		t.Fatal("concurrent inventories mixed observations")
	}
}
