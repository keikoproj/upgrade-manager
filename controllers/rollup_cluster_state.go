/*
Copyright 2019 Intuit, Inc..

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

package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"sync"
)

const (
	// updateStarted indicates that the update process has been started for an instance
	updateInitialized = "new"
	// updateInProgress indicates that update has been triggered for an instance
	updateInProgress = "in-progress"
	// updateCompleted indicates that update is completed for an instance
	updateCompleted = "completed"
)

// ClusterState contains the methods to store the instance states during a cluster update
type ClusterState interface {
	markUpdateInitialized(instanceId string)
	markUpdateInProgress(instanceId string)
	markUpdateCompleted(instanceId string)
	instanceUpdateInitialized(instanceId string) bool
	instanceUpdateInProgress(instanceId string) bool
	instanceUpdateCompleted(instanceId string) bool
	deleteEntryOfAsg(asgName string) bool
	getNextAvailableInstanceIdInAz(asgName string, azName string) string
	initializeAsg(asgName string, instances []*autoscaling.Instance)
	addInstanceState(instanceData *InstanceData)
	updateInstanceState(instanceId, instanceState string)
	getInstanceState(instanceId string) string
}

type InstanceData struct {
	Id            string
	AmiId         string
	AsgName       string
	AzName        string
	InstanceState string
}

// ClusterStateImpl implements the ClusterState interface
type ClusterStateImpl struct {
	mu sync.RWMutex
}

// ClusterStateStore stores the state of the instances running in different Azs for multiple ASGs
var ClusterStateStore sync.Map

// newClusterState returns the object the struct implementing the ClusterState interface
func NewClusterState() ClusterState {
	return &ClusterStateImpl{}
}

// markUpdateInProgress updates the instance state to in-progresss
func (c *ClusterStateImpl) markUpdateInProgress(instanceId string) {
	c.updateInstanceState(instanceId, updateInProgress)
}

// markUpdateCompleted updates the instance state to completed
func (c *ClusterStateImpl) markUpdateCompleted(instanceId string) {
	c.updateInstanceState(instanceId, updateCompleted)
}

// instanceUpdateInProgress returns true if the instance update is in progress
func (c *ClusterStateImpl) instanceUpdateInProgress(instanceId string) bool {
	return c.getInstanceState(instanceId) == updateInProgress
}

// instanceUpdateCompleted returns true if the instance update is completed
func (c *ClusterStateImpl) instanceUpdateCompleted(instanceId string) bool {
	return c.getInstanceState(instanceId) == updateCompleted
}

// deleteEntryOfAsg deletes the entry for an ASG in the cluster state map
func (c *ClusterStateImpl) deleteEntryOfAsg(asgName string) bool {
	deleted := false
	ClusterStateStore.Range(func(key interface{}, value interface{}) bool {
		keyName, _ := key.(string)
		instanceData, _ := value.(*InstanceData)
		if instanceData.AsgName == asgName {
			ClusterStateStore.Delete(keyName)
			deleted = true
		}
		return true
	})
	return deleted
}

// markUpdateInitialized updates the instance state to in-progresss
func (c *ClusterStateImpl) markUpdateInitialized(instanceId string) {
	c.updateInstanceState(instanceId, updateInitialized)
}

// instanceUpdateInitialized returns true if the instance update is in progress
func (c *ClusterStateImpl) instanceUpdateInitialized(instanceId string) bool {
	return c.getInstanceState(instanceId) == updateInitialized
}

// initializeAsg adds an entry for all the instances in an ASG with updateInitialized state
func (c *ClusterStateImpl) initializeAsg(asgName string, instances []*autoscaling.Instance) {
	for _, instance := range instances {
		instanceData := &InstanceData{
			Id:            *instance.InstanceId,
			AzName:        *instance.AvailabilityZone,
			AsgName:       asgName,
			InstanceState: updateInitialized,
		}
		c.addInstanceState(instanceData)
	}
}

// getNextAvailableInstanceId returns the id of the next instance available for update in an ASG
// adding a mutex to avoid the race conditions and same instance returned for 2 go-routines
func (c *ClusterStateImpl) getNextAvailableInstanceIdInAz(asgName string, azName string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	instanceId := ""
	ClusterStateStore.Range(func(key interface{}, value interface{}) bool {
		state, _ := value.(*InstanceData)
		if state.AsgName == asgName &&
			(azName == "" || state.AzName == azName) &&
			state.InstanceState == updateInitialized {
			c.markUpdateInProgress(state.Id)
			instanceId = state.Id
			return false
		}
		return true
	})
	return instanceId
}

// updateInstanceState updates the state of the instance in cluster store
func (c *ClusterStateImpl) addInstanceState(instanceData *InstanceData) {
	ClusterStateStore.Store(instanceData.Id, instanceData)
}

// updateInstanceState updates the state of the instance in cluster store
func (c *ClusterStateImpl) updateInstanceState(instanceId, instanceState string) {
	val, ok := ClusterStateStore.Load(instanceId)
	if ok {
		instanceData, _ := val.(*InstanceData)
		instanceData.InstanceState = instanceState
		ClusterStateStore.Store(instanceId, instanceData)
	}
}

// getInstanceState returns the state of the instance from cluster store
func (c *ClusterStateImpl) getInstanceState(instanceId string) string {
	val, ok := ClusterStateStore.Load(instanceId)
	if ok {
		state, _ := val.(*InstanceData)
		return state.InstanceState
	}
	return ""
}
