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
	"fmt"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"strings"
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
	markUpdateInitialized(asgName, instanceId string)
	markUpdateInProgress(asgName, instanceId string)
	markUpdateCompleted(asgName, instanceId string)
	instanceUpdateInitialized(asgName, instanceId string) bool
	instanceUpdateInProgress(asgName, instanceId string) bool
	instanceUpdateCompleted(asgName, instanceId string) bool
	deleteEntryOfAsg(asgName string) bool
	getNextAvailableInstanceId(asgName string) string
	initializeAsg(asgName string, instances []*autoscaling.Instance)
	updateInstanceState(asgName, instanceId, instanceState string)
	getInstanceState(asgName, instanceId string) string
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
func (c *ClusterStateImpl) markUpdateInProgress(asgName, instanceId string) {
	c.updateInstanceState(asgName, instanceId, updateInProgress)
}

// markUpdateCompleted updates the instance state to completed
func (c *ClusterStateImpl) markUpdateCompleted(asgName, instanceId string) {
	c.updateInstanceState(asgName, instanceId, updateCompleted)
}

// instanceUpdateInProgress returns true if the instance update is in progress
func (c *ClusterStateImpl) instanceUpdateInProgress(asgName, instanceId string) bool {
	return c.getInstanceState(asgName, instanceId) == updateInProgress
}

// instanceUpdateCompleted returns true if the instance update is completed
func (c *ClusterStateImpl) instanceUpdateCompleted(asgName, instanceId string) bool {
	return c.getInstanceState(asgName, instanceId) == updateCompleted
}

// deleteEntryOfAsg deletes the entry for an ASG in the cluster state map
func (c *ClusterStateImpl) deleteEntryOfAsg(asgName string) bool {
	deleted := false
	ClusterStateStore.Range(func(key interface{}, value interface{}) bool {
		keyName, _ := key.(string)
		if strings.Contains(keyName, asgName) {
			ClusterStateStore.Delete(keyName)
			deleted = true
		}
		return true
	})
	return deleted
}

// markUpdateInitialized updates the instance state to in-progresss
func (c *ClusterStateImpl) markUpdateInitialized(asgName, instanceId string) {
	c.updateInstanceState(asgName, instanceId, updateInitialized)
}

// instanceUpdateInitialized returns true if the instance update is in progress
func (c *ClusterStateImpl) instanceUpdateInitialized(asgName, instanceId string) bool {
	return c.getInstanceState(asgName, instanceId) == updateInitialized
}

// initializeAsg adds an entry for all the instances in an ASG with updateInitialized state
func (c *ClusterStateImpl) initializeAsg(asgName string, instances []*autoscaling.Instance) {
	for _, instance := range instances {
		c.updateInstanceState(asgName, *instance.InstanceId, updateInitialized)
	}
}

// getNextAvailableInstanceId returns the id of the next instance available for update in an ASG
// adding a mutex to avoid the race conditions and same instance returned for 2 go-routines
func (c *ClusterStateImpl) getNextAvailableInstanceId(asgName string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	instanceId := ""
	ClusterStateStore.Range(func(key interface{}, value interface{}) bool {
		keyName, _ := key.(string)
		state, _ := value.(string)
		if strings.HasPrefix(keyName, asgName) && strings.EqualFold(state, updateInitialized) {
			instanceId = strings.TrimPrefix(keyName, fmt.Sprintf("%s-", asgName))
			c.markUpdateInProgress(asgName, instanceId)
			return false
		}
		return true
	})
	return instanceId
}

// updateInstanceState updates the state of the instance in cluster store
func (c *ClusterStateImpl) updateInstanceState(asgName, instanceId, instanceState string) {
	key := fmt.Sprintf("%s-%s", asgName, instanceId)
	ClusterStateStore.Store(key, instanceState)
}

// getInstanceState returns the state of the instance from cluster store
func (c *ClusterStateImpl) getInstanceState(asgName, instanceId string) string {
	key := fmt.Sprintf("%s-%s", asgName, instanceId)
	val, ok := ClusterStateStore.Load(key)
	if !ok {

	}
	state, _ := val.(string)
	return state
}
