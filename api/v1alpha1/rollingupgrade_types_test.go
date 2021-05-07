/*

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

package v1alpha1

import (
	"sync"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	. "github.com/onsi/gomega"

	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// These tests are written in BDD-style using Ginkgo framework. Refer to
// http://onsi.github.io/ginkgo to learn more.

var _ = Describe("RollingUpgrade", func() {
	var (
		key              types.NamespacedName
		created, fetched *RollingUpgrade
	)

	BeforeEach(func() {
		// Add any setup steps that needs to be executed before each test
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
	})

	Context("NamespacedName", func() {
		It("generates qualified name", func() {
			ru := &RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Namespace: "namespace-foo", Name: "object-bar"}}
			Expect(ru.NamespacedName()).To(Equal("namespace-foo/object-bar"))
		})
	})

	// Add Tests for OpenAPI validation (or additonal CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Create API", func() {

		It("should create an object successfully", func() {

			key = types.NamespacedName{
				Name:      "foo",
				Namespace: "default",
			}
			created = &RollingUpgrade{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
				}}

			By("creating an API obj")
			Expect(k8sClient.Create(context.TODO(), created)).To(Succeed())

			fetched = &RollingUpgrade{}
			Expect(k8sClient.Get(context.TODO(), key, fetched)).To(Succeed())
			Expect(fetched).To(Equal(created))

			By("deleting the created object")
			Expect(k8sClient.Delete(context.TODO(), created)).To(Succeed())
			Expect(k8sClient.Get(context.TODO(), key, created)).ToNot(Succeed())
		})

	})

})

// Test
func TestNodeTurnsOntoStep(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &RollingUpgradeStatus{}
	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string][]NodeStepDuration)
	inProcessingNodes := make(map[string]*NodeInProcessing)
	mutex := &sync.Mutex{}

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationKickoff, mutex)

	g.Expect(inProcessingNodes).NotTo(gomega.BeNil())
	g.Expect(nodeSteps["node-1"]).To(gomega.BeNil())

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)

	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready again
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Completed
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationCompleted, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))
	g.Expect(nodeSteps["node-1"][1].StepName).To(gomega.Equal(NodeRotationDesiredNodeReady))
	g.Expect(nodeSteps["node-1"][2].StepName).To(gomega.Equal(NodeRotationTotal))

	//Second node
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", NodeRotationKickoff, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))

	r.UpdateLastBatchNodes(inProcessingNodes)
	g.Expect(len(r.LastBatchNodes)).To(gomega.Equal(2))

	r.UpdateStatistics(nodeSteps)
	g.Expect(r.Statistics).ToNot(gomega.BeEmpty())
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))

}
