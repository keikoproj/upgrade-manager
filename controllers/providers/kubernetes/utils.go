/*
Copyright 2021 Intuit Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Placeholder Kubernetes helper functions

type KubernetesClientSet struct {
	Kubernetes kubernetes.Interface
}

func GetKubernetesClient() (kubernetes.Interface, error) {
	var config *rest.Config
	config, err := GetKubernetesConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func GetKubernetesConfig() (*rest.Config, error) {
	var config *rest.Config
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = GetKubernetesLocalConfig()
		if err != nil {
			return nil, err
		}
		return config, nil
	}
	return config, nil
}

func GetKubernetesLocalConfig() (*rest.Config, error) {
	var kubePath string
	if os.Getenv("KUBECONFIG") != "" {
		kubePath = os.Getenv("KUBECONFIG")
	} else {
		usr, err := user.Current()
		if err != nil {
			return nil, err
		}
		kubePath = usr.HomeDir + "/.kube/config"
	}

	if kubePath == "" {
		err := fmt.Errorf("failed to get kubeconfig path")
		return nil, err
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubePath)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func SelectNodeByInstanceID(instanceID string, nodes *corev1.NodeList) corev1.Node {
	for _, node := range nodes.Items {
		tokens := strings.Split(node.Spec.ProviderID, "/")
		nodeID := tokens[len(tokens)-1]
		if strings.EqualFold(instanceID, nodeID) {
			return node
		}
	}
	return corev1.Node{}
}
