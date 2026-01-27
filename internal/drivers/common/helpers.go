package common

import (
	v1 "k8s.io/api/core/v1"
)

func FindPod(pods []v1.Pod, namespace, name string) *v1.Pod {
	for _, pod := range pods {

		if pod.Name == name && pod.Namespace == namespace {
			return &pod
		}
	}
	return nil
}
