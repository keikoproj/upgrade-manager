package controllers

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
