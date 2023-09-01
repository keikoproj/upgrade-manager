package controllers

import (
	"context"
	"testing"

	//AWS

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestMaxParallel(t *testing.T) {
	rollingUpgrade := createRollingUpgrade()
	request := reconcile.Request{NamespacedName: types.NamespacedName{Name: rollingUpgrade.Name, Namespace: rollingUpgrade.Namespace}}

	var tests = []struct {
		TestDescription     string
		Reconciler          *RollingUpgradeReconciler
		ExpectedStatusValue string
		maxParallel         int
	}{
		{
			"Number of running rolling upgrades is within the max-parallel limit, reconcile() should admit the new rolling upgrade CR",
			createRollingUpgradeReconciler(t, rollingUpgrade),
			v1alpha1.StatusComplete,
			10,
		},
		{
			"Number of running rolling upgrades is greate or euqal to the max-parallel limit, reconcile() should not admit the new rolling upgrade CR",
			createRollingUpgradeReconciler(t, rollingUpgrade),
			v1alpha1.StatusInit,
			4,
		},
	}
	for _, test := range tests {
		test.Reconciler.maxParallel = test.maxParallel
		_, err := test.Reconciler.Reconcile(context.Background(), request)
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}
		rollingUpgradeAfterReconcile := &v1alpha1.RollingUpgrade{}
		err = test.Reconciler.Client.Get(context.Background(), request.NamespacedName, rollingUpgradeAfterReconcile)
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}
		if rollingUpgradeAfterReconcile.CurrentStatus() != test.ExpectedStatusValue {
			t.Errorf("Test Description: %s \n expected value: %s, actual value: %s", test.TestDescription, test.ExpectedStatusValue, rollingUpgradeAfterReconcile.CurrentStatus())
		}
	}
}
