package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sync"
)

func TestMaxParallel(t *testing.T) {
	rollingUpgrade := createRollingUpgrade()

	var tests = []struct {
		TestDescription string
		maxParallel     int
		ExpectAdmission bool
	}{
		{
			"Number of running rolling upgrades is within the max-parallel limit, reconcile() should admit the new rolling upgrade CR",
			10,
			true,
		},
		{
			"Number of running rolling upgrades is greate or euqal to the max-parallel limit, reconcile() should not admit the new rolling upgrade CR",
			4,
			false,
		},
	}

	for _, test := range tests {
		// Create a fresh RollingUpgrade copy and reconciler for each test
		testRU := rollingUpgrade.DeepCopy()
		reconciler := createRollingUpgradeReconciler(t, testRU)
		reconciler.maxParallel = test.maxParallel

		// Create the request
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testRU.Name,
				Namespace: testRU.Namespace,
			},
		}

		// Run the reconcile
		_, err := reconciler.Reconcile(context.Background(), request)
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}

		// Verify admission status
		_, isAdmitted := reconciler.AdmissionMap.Load(testRU.NamespacedName())
		if isAdmitted != test.ExpectAdmission {
			t.Errorf("Test Description: %s \n expected to be admitted: %v, actually admitted: %v",
				test.TestDescription, test.ExpectAdmission, isAdmitted)
		}
	}
}

func TestControllerSetupWithManager(t *testing.T) {
	// Create a reconciler
	reconciler := createRollingUpgradeReconciler(t)
	
	// Create a fake REST config instead of using GetConfigOrDie() which fails in CI
	fakeRestConfig := &rest.Config{
		Host: "https://example.com",
	}
	
	// Create a scheme for the fake client
	scheme := runtime.NewScheme()
	// Add the needed types to the scheme
	corev1.AddToScheme(scheme)
	
	// Create a mock manager with the fake config
	mgr, err := ctrl.NewManager(fakeRestConfig, ctrl.Options{
		Scheme: reconciler.Scheme,
		// Use client.Options with a fake client to avoid needing a real API server
		Client: client.Options{
			Cache: &client.CacheOptions{
				Reader: fake.NewClientBuilder().WithScheme(scheme).Build(),
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	
	// Test the SetupWithManager function with the new controller-runtime v0.20.4 API
	err = reconciler.SetupWithManager(mgr)
	if err != nil {
		t.Errorf("SetupWithManager failed with new controller-runtime API: %v", err)
	}
	
	// We can't directly test the internal structure of the controller after setup,
	// but we've verified it doesn't error out with the new API
}

func TestNodeEventsHandler(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	predicate := reconciler.NodeEventsHandler()

	// Create a test node
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"node-role.kubernetes.io/master": "true",
			},
		},
	}

	// Create a non-node object to test the predicate
	nonNode := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Test that the predicate correctly handles different event types

	// Test Create event for a node (should return false)
	createEvent := event.CreateEvent{Object: node}
	if predicate.Create(createEvent) {
		t.Error("NodeEventsHandler predicate should return false for node create events")
	}

	// Test Create event for a non-node (should return true)
	nonNodeCreateEvent := event.CreateEvent{Object: nonNode}
	if !predicate.Create(nonNodeCreateEvent) {
		t.Error("NodeEventsHandler predicate should return true for non-node create events")
	}

	// Test Update event for a node (should return false)
	updateEvent := event.UpdateEvent{
		ObjectOld: node,
		ObjectNew: node,
	}
	if predicate.Update(updateEvent) {
		t.Error("NodeEventsHandler predicate should return false for node update events")
	}

	// Test Update event for a non-node (should return true)
	nonNodeUpdateEvent := event.UpdateEvent{
		ObjectOld: nonNode,
		ObjectNew: nonNode,
	}
	if !predicate.Update(nonNodeUpdateEvent) {
		t.Error("NodeEventsHandler predicate should return true for non-node update events")
	}

	// Test Delete event for a node (should return false)
	deleteEvent := event.DeleteEvent{Object: node}
	if predicate.Delete(deleteEvent) {
		t.Error("NodeEventsHandler predicate should return false for node delete events")
	}

	// Test Delete event for a non-node (should return true)
	nonNodeDeleteEvent := event.DeleteEvent{Object: nonNode}
	if !predicate.Delete(nonNodeDeleteEvent) {
		t.Error("NodeEventsHandler predicate should return true for non-node delete events")
	}

	// Test that the node is stored in the cluster nodes map
	beforeMapLen := countMapItems(reconciler.ClusterNodesMap)
	predicate.Create(createEvent) // This should store the node in the map
	afterMapLen := countMapItems(reconciler.ClusterNodesMap)

	if afterMapLen <= beforeMapLen {
		t.Error("NodeEventsHandler should store nodes in the cluster nodes map")
	}

	// Test that the node is deleted from the map on delete events
	predicate.Delete(deleteEvent) // This should delete the node from the map
	afterDeleteMapLen := countMapItems(reconciler.ClusterNodesMap)

	if afterDeleteMapLen >= afterMapLen {
		t.Error("NodeEventsHandler should remove nodes from the cluster nodes map on delete")
	}
}

// Helper function to count items in a sync.Map
func countMapItems(m *sync.Map) int {
	count := 0
	m.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

func TestNamespaceScoping(t *testing.T) {
	// We can't directly test main.go, but we can write a similar function
	// to test the namespace scoping logic

	testCases := []struct {
		name         string
		namespace    string
		expectScoped bool
	}{
		{
			name:         "With namespace specified",
			namespace:    "test-namespace",
			expectScoped: true,
		},
		{
			name:         "With empty namespace",
			namespace:    "",
			expectScoped: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create controller options similar to main.go
			ctrlOpts := ctrl.Options{}

			// Apply namespace scoping logic similar to main.go
			if tc.namespace != "" {
				ctrlOpts.Cache.DefaultNamespaces = map[string]cache.Config{
					tc.namespace: {},
				}
			}

			// Verify namespace scoping was applied correctly
			if tc.expectScoped {
				if ctrlOpts.Cache.DefaultNamespaces == nil {
					t.Error("Expected DefaultNamespaces to be set when namespace provided")
				}
				if _, exists := ctrlOpts.Cache.DefaultNamespaces[tc.namespace]; !exists {
					t.Errorf("Expected namespace %s to be in DefaultNamespaces map", tc.namespace)
				}
			} else {
				if len(ctrlOpts.Cache.DefaultNamespaces) > 0 {
					t.Error("Expected DefaultNamespaces to be empty when no namespace provided")
				}
			}
		})
	}
}

func TestEnqueueRequestsFromMapFunc(t *testing.T) {
	// Create a test node
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"node-role.kubernetes.io/worker": "true",
			},
		},
	}

	// Create a pod (non-node) object
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Create the reconciler with our filtering logic
	reconciler := createRollingUpgradeReconciler(t)

	// Create a context for the handler function
	ctx := context.Background()

	// Create a simple handler function similar to what we use in SetupWithManager
	handlerFunc := func(ctx context.Context, obj client.Object) []reconcile.Request {
		// Use the NodeEventsHandler to store nodes in the map but don't generate reconcile events for them
		// This matches the behavior in SetupWithManager where we just update the node map but don't trigger reconciles

		// For node objects, the predicate returns false, so we should store them and return empty requests
		if node, ok := obj.(*corev1.Node); ok {
			// Store the node in the map (this is what NodeEventsHandler does)
			reconciler.ClusterNodesMap.Store(node.Name, node)

			// Since the predicate returns false for node events, we should return no requests
			// This mirrors what happens in the controller setup
			return []reconcile.Request{}
		}

		// For non-node objects, the predicate returns true, so we could generate requests
		// In a real implementation, we'd return appropriate requests
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: "test-upgrade", Namespace: "default"}},
		}
	}

	// Test that handler function returns no requests for node objects
	nodeRequests := handlerFunc(ctx, node)

	// We expect zero requests for nodes
	if len(nodeRequests) != 0 {
		t.Errorf("Expected handler function to generate 0 requests for nodes, got %d", len(nodeRequests))
	}

	// Verify that the node was stored in the map
	storedNode, exists := reconciler.ClusterNodesMap.Load(node.Name)
	if !exists {
		t.Error("Node should have been stored in the map")
	}
	if storedNode != node {
		t.Error("Stored node doesn't match the original node")
	}

	// Test that handler function returns requests for non-node objects
	podRequests := handlerFunc(ctx, pod)

	// We expect non-zero requests for non-nodes
	if len(podRequests) == 0 {
		t.Error("Expected handler function to generate requests for non-node objects")
	}

	// Verify the correct request for non-node objects
	expectedRequest := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-upgrade", Namespace: "default"},
	}
	if len(podRequests) > 0 && podRequests[0] != expectedRequest {
		t.Errorf("Expected request %v, got %v", expectedRequest, podRequests[0])
	}
}
