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

package controllers

import (
	"context"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	logr.Logger
	Scheme       *runtime.Scheme
	AdmissionMap sync.Map
	CacheConfig  *cache.Config
	Auth         *RollingUpgradeAuthenticator
}

type RollingUpgradeAuthenticator struct {
	awsprovider.AmazonClientSet
	kubeprovider.KubernetesClientSet
}

// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=list
// +kubebuilder:rbac:groups=core,resources=events,verbs=create
// +kubebuilder:rbac:groups=core,resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=extensions;apps,resources=daemonsets;replicasets;statefulsets,verbs=get
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get

// Reconcile reads that state of the cluster for a RollingUpgrade object and makes changes based on the state read
// and the details in the RollingUpgrade.Spec
func (r *RollingUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rollingUpgrade := &upgrademgrv1alpha1.RollingUpgrade{}
	err := r.Get(ctx, req.NamespacedName, rollingUpgrade)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.AdmissionMap.Delete(req.NamespacedName)
			r.Info("deleted object from admission map", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// If the resource is being deleted, remove it from the admissionMap
	if !rollingUpgrade.DeletionTimestamp.IsZero() {
		r.AdmissionMap.Delete(req.NamespacedName)
		r.Info("deleted object from admission map", "name", req.NamespacedName)
		return reconcile.Result{}, nil
	}

	// TODO: VALIDATION + SET DEFAULTS

	// Migrate r.setDefaultsForRollingUpdateStrategy & r.validateRollingUpgradeObj into v1alpha1 RollingUpgrade.Validate()
	// Include setting of defaults in validation

	// if ok, err := rollingUpgrade.Validate(); !ok {
	//   return reconcile.Result{}, err
	// }

	var (
		scalingGroupName = rollingUpgrade.ScalingGroupName()
		inProgress       bool
	)

	r.AdmissionMap.Range(func(k, v interface{}) bool {
		val := v.(string)
		if strings.EqualFold(val, scalingGroupName) {
			r.Info("object already being processed", "name", k, "scalingGroup", scalingGroupName)
			inProgress = true
			return false
		}
		return true
	})

	if inProgress {
		return ctrl.Result{}, nil
	}

	r.Info("admitted new rollingupgrade", "name", req.NamespacedName, "scalingGroup", scalingGroupName)
	r.AdmissionMap.Store(req.NamespacedName, scalingGroupName)

	// TODO: Cloud Discovery - discover AWS / K8s resources

	// TODO: State - determine state / requeue

	// TODO: Process - rotate nodes

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RollingUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&upgrademgrv1alpha1.RollingUpgrade{}).
		Complete(r)
}
