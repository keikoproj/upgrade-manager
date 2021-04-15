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
	"time"

	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	logr.Logger
	Scheme           *runtime.Scheme
	AdmissionMap     sync.Map
	CacheConfig      *cache.Config
	Auth             *RollingUpgradeAuthenticator
	Cloud            *DiscoveredState
	EventWriter      *kubeprovider.EventWriter
	maxParallel      int
	ScriptRunner     ScriptRunner
	DrainGroupMapper sync.Map
	DrainErrorMapper sync.Map
}

type RollingUpgradeAuthenticator struct {
	*awsprovider.AmazonClientSet
	*kubeprovider.KubernetesClientSet
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
	rollingUpgrade := &v1alpha1.RollingUpgrade{}
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
		r.AdmissionMap.Delete(rollingUpgrade.NamespacedName())
		r.Info("rolling upgrade deleted", "name", rollingUpgrade.NamespacedName())
		return reconcile.Result{}, nil
	}

	// Stop processing upgrades which are in finite state
	currentStatus := rollingUpgrade.CurrentStatus()
	if common.ContainsEqualFold(v1alpha1.FiniteStates, currentStatus) {
		r.AdmissionMap.Delete(rollingUpgrade.NamespacedName())
		r.Info("rolling upgrade ended", "name", rollingUpgrade.NamespacedName(), "status", currentStatus)
		return reconcile.Result{}, nil
	}

	if ok, err := rollingUpgrade.Validate(); !ok {
		return reconcile.Result{}, err
	}

	var (
		scalingGroupName = rollingUpgrade.ScalingGroupName()
		inProgress       bool
	)

	// Defer a status update on the resource
	defer r.UpdateStatus(rollingUpgrade)

	// handle condition where multiple resources submitted targeting the same scaling group by requeing
	r.AdmissionMap.Range(func(k, v interface{}) bool {
		val := v.(string)
		resource := k.(string)
		if strings.EqualFold(val, scalingGroupName) && !strings.EqualFold(resource, rollingUpgrade.NamespacedName()) {
			r.Info("object already being processed by existing resource", "resource", resource, "scalingGroup", scalingGroupName)
			inProgress = true
			return false
		}
		return true
	})

	if inProgress {
		// requeue any resources which are already being processed by a different resource, until the resource is completed/deleted
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	r.Info("admitted new rollingupgrade", "name", rollingUpgrade.NamespacedName(), "scalingGroup", scalingGroupName)
	r.AdmissionMap.Store(rollingUpgrade.NamespacedName(), scalingGroupName)
	rollingUpgrade.SetCurrentStatus(v1alpha1.StatusInit)

	r.Cloud = NewDiscoveredState(r.Auth, r.Logger)
	if err := r.Cloud.Discover(); err != nil {
		rollingUpgrade.SetCurrentStatus(v1alpha1.StatusError)
		return ctrl.Result{}, err
	}

	// process node rotation
	if err := r.RotateNodes(rollingUpgrade); err != nil {
		rollingUpgrade.SetCurrentStatus(v1alpha1.StatusError)
		return ctrl.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Second * 10}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RollingUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RollingUpgrade{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.maxParallel}).
		Complete(r)
}

func (r *RollingUpgradeReconciler) SetMaxParallel(n int) {
	if n >= 1 {
		r.Info("setting max parallel reconcile", "value", n)
		r.maxParallel = n
	}
}

func (r *RollingUpgradeReconciler) UpdateStatus(rollingUpgrade *v1alpha1.RollingUpgrade) {
	if err := r.Status().Update(context.Background(), rollingUpgrade); err != nil {
		r.Error(err, "failed to update status", "name", rollingUpgrade.NamespacedName())
	}
}
