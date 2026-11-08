// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	"os"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func configureVerifiedDriver(ctx context.Context, driver drivers.CiscoKubernetesDeviceDriver, spec *ciskov1.DeviceSpec, kube kubernetes.Interface, identity workerRuntimeIdentity) error {
	xe, ok := driver.(*iosxe.XEDriver)
	if !ok {
		return fmt.Errorf("Verified device delivery currently requires IOS-XE")
	}
	if err := spec.AppArtifacts.Validate(); err != nil {
		return err
	}

	if spec.TLS == nil || !spec.TLS.Enabled || spec.TLS.InsecureSkipVerify {
		return fmt.Errorf("Verified requires authenticated RESTCONF TLS")
	}
	var files func(context.Context) (iosxe.VerifiedFiles, error)
	var delivery iosxe.VerifiedPackageDelivery
	if spec.AppArtifacts.Delivery == "Copy" {
		delivery = xe.CopyVerifiedDelivery(spec.AppArtifacts.SSHHostKeySHA256)
	} else {
		resolved, err := resolveGNOIConfig(spec, spec.Password, envEnabled(gNOIInsecureEnv), gNOIProvisioningMountPath, false)
		if err != nil {
			return err
		}
		if resolved.dialConfig.TLSConfig == nil || resolved.dialConfig.TLSConfig.InsecureSkipVerify {
			return fmt.Errorf("Verified requires authenticated gNOI TLS")
		}
		provider, _, closeProvider, err := setupGNOI(ctx, configReconcilerOptions{Spec: spec, Password: spec.Password})
		if err != nil {
			return err
		}
		if provider == nil {
			return fmt.Errorf("Verified requires gNOI File.Put and File.Get")
		}
		go func() {
			<-ctx.Done()
			if closeProvider != nil {
				closeProvider()
			}
		}()
		files = func(ctx context.Context) (iosxe.VerifiedFiles, error) { return provider.GNOIClient(ctx) }
	}
	config := spec.AppArtifacts
	resolver, err := appartifact.NewResolver(appartifact.Config{Origin: config.StoreURL, ProducerPublicKeys: config.ProducerPublicKeys, Platform: appartifact.Platform{OS: config.Platform.OS, Architecture: config.Platform.Architecture, Variant: config.Platform.Variant}, MaxPackageBytes: config.EffectiveMaxPackageBytes()})
	if err != nil {
		return err
	}
	journal, err := verifiedInstallationJournal(ctx, kube, identity, spec.Address)
	if err != nil {
		return err
	}
	xe.SetVerifiedRuntime(&iosxe.VerifiedRuntime{Resolver: resolver, Journal: journal, Files: files, Delivery: delivery})
	go func() { <-ctx.Done(); resolver.CloseIdleConnections() }()
	return nil
}

func verifiedInstallationJournal(ctx context.Context, kube kubernetes.Interface, identity workerRuntimeIdentity, address string) (appartifact.Journal, error) {
	namespace := identity.DeviceNamespace
	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
	}
	if namespace == "" {
		namespace = "default"
	}
	name := iosxe.VerifiedJournalName(address)
	if identity.ManagedTopology {
		if identity.DeviceNamespace == "" || identity.DeviceName == "" || identity.DeviceUID == "" {
			return appartifact.Journal{}, fmt.Errorf("managed Verified journal requires complete device identity")
		}
		name = appartifact.ManagedJournalName(identity.DeviceUID)
	}
	journal := appartifact.Journal{Maps: kube.CoreV1().ConfigMaps(namespace), Name: name}
	cm, err := journal.Maps.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return journal, fmt.Errorf("Verified journal %s/%s is unavailable: %w", namespace, name, err)
	}
	if identity.ManagedTopology {
		if err = appartifact.ValidateManagedJournal(cm, namespace, identity.DeviceName, identity.DeviceUID); err != nil {
			return journal, err
		}
		if cm.UID == "" {
			return journal, fmt.Errorf("managed Verified journal lacks Kubernetes UID")
		}
		journal.UID = cm.UID
	}
	if _, _, err = journal.Load(ctx, "startup"); err != nil {
		return journal, err
	}
	return journal, nil
}
