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
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type EventWriter struct {
	*KubernetesClientSet
	logr.Logger
}

func NewEventWriter(k *KubernetesClientSet, logger logr.Logger) *EventWriter {
	return &EventWriter{
		KubernetesClientSet: k,
		Logger:              logger,
	}
}

// EventReason defines the reason of an event
type EventReason string

// EventLevel defines the level of an event
type EventLevel string

const (
	// EventLevelNormal is the level of a normal event
	EventLevelNormal = "Normal"
	// EventLevelWarning is the level of a warning event
	EventLevelWarning = "Warning"
	// EventReasonRUStarted Rolling Upgrade Started
	EventReasonRUStarted EventReason = "RollingUpgradeStarted"
	// EventReasonRUInstanceStarted Rolling Upgrade for Instance has started
	EventReasonRUInstanceStarted EventReason = "RollingUpgradeInstanceStarted"
	// EventReasonRUInstanceFinished Rolling Upgrade for Instance has finished
	EventReasonRUInstanceFinished EventReason = "RollingUpgradeInstanceFinished"
	// EventReasonRUFinished Rolling Upgrade Finished
	EventReasonRUFinished EventReason = "RollingUpgradeFinished"
)

func (w *EventWriter) CreateEvent(rollingUpgrade *v1alpha1.RollingUpgrade, reason EventReason, level string, msgFields map[string]string) {
	b, _ := json.Marshal(msgFields)
	msgPayload := string(b)
	t := metav1.Time{Time: time.Now()}
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%v-%v.%v", rollingUpgrade.GetName(), time.Now().Unix(), rand.Int()),
		},
		Source: corev1.EventSource{
			Component: "upgrade-manager",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:            "RollingUpgrade",
			Name:            rollingUpgrade.GetName(),
			Namespace:       rollingUpgrade.GetNamespace(),
			ResourceVersion: rollingUpgrade.GetResourceVersion(),
			APIVersion:      v1alpha1.GroupVersion.Version,
			UID:             rollingUpgrade.GetUID(),
		},
		Reason:         string(reason),
		Message:        msgPayload,
		Type:           level,
		Count:          1,
		FirstTimestamp: t,
		LastTimestamp:  t,
	}

	w.V(1).Info("publishing event", "event", event)
	_, err := w.Kubernetes.CoreV1().Events(rollingUpgrade.GetNamespace()).Create(context.Background(), event, metav1.CreateOptions{})
	if err != nil {
		w.Error(err, "failed to publish event")
	}
}
