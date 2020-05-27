package controllers

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/pkg/log"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func (r *RollingUpgradeReconciler) createK8sV1Event(objMeta *upgrademgrv1alpha1.RollingUpgrade, reason EventReason, level string, msgFields map[string]string) *v1.Event {
	// Marshal as JSON
	// I think it is very tough to trigger this error since json.Marshal function can return two types of errors
	// UnsupportedTypeError or UnsupportedValueError. Since our type is very rigid, these errors won't be triggered.
	b, _ := json.Marshal(msgFields)
	msgPayload := string(b)
	t := metav1.Time{Time: time.Now()}
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%v-%v.%v", objMeta.Name, time.Now().Unix(), rand.Int()),
		},
		Source: v1.EventSource{
			// TODO(vigith): get it from GVK?
			Component: "upgrade-manager",
		},
		InvolvedObject: v1.ObjectReference{
			Kind:            "RollingUpgrade",
			Name:            objMeta.Name,
			Namespace:       objMeta.Namespace,
			ResourceVersion: objMeta.ResourceVersion,
			APIVersion:      upgrademgrv1alpha1.GroupVersion.Version,
			UID:             objMeta.UID,
		},
		Reason:         string(reason),
		Message:        msgPayload,
		Type:           level,
		Count:          1,
		FirstTimestamp: t,
		LastTimestamp:  t,
	}

	log.Debugf("Publishing event: %v", event)
	_event, err := r.generatedClient.CoreV1().Events(objMeta.Namespace).Create(event)
	if err != nil {
		log.Errorf("Create Events Failed %v, %v", event, err)
	}

	return _event
}
