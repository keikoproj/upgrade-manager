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
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	"github.com/keikoproj/upgrade-manager/controllers/common/log"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	logr.Logger
	Scheme              *runtime.Scheme
	AdmissionMap        sync.Map
	CacheConfig         *cache.Config
	EventWriter         *kubeprovider.EventWriter
	maxParallel         int
	ScriptRunner        ScriptRunner
	Auth                *RollingUpgradeAuthenticator
	DrainGroupMapper    *sync.Map
	DrainErrorMapper    *sync.Map
	ClusterNodesMap     *sync.Map
	ReconcileMap        *sync.Map
	DrainTimeout        int
	IgnoreDrainFailures bool
	ReplacementNodesMap *sync.Map
	MaxReplacementNodes int
}

// RollingUpgradeAuthenticator has the clients for providers
type RollingUpgradeAuthenticator struct {
	*awsprovider.AmazonClientSet
	*kubeprovider.KubernetesClientSet
}

// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;patch;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=list
// +kubebuilder:rbac:groups=core,resources=events,verbs=create
// +kubebuilder:rbac:groups=core,resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=extensions;apps,resources=daemonsets;replicasets;statefulsets,verbs=get
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get

// reconcile reads that state of the cluster for a RollingUpgrade object and makes changes based on the state read
// and the details in the RollingUpgrade.Spec
func (r *RollingUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Info("***Reconciling***")
	rollingUpgrade := &v1alpha1.RollingUpgrade{}
	err := r.Get(ctx, req.NamespacedName, rollingUpgrade)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.AdmissionMap.Delete(req.NamespacedName.String())
			r.Info("rolling upgrade resource not found, deleted object from admission map", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// if the resource is being deleted, remove it from the admissionMap
	if !rollingUpgrade.DeletionTimestamp.IsZero() {
		r.AdmissionMap.Delete(rollingUpgrade.NamespacedName())
		r.Info("rolling upgrade deleted", "name", rollingUpgrade.NamespacedName())
		return reconcile.Result{}, nil
	}

	// stop processing upgrades which are in finite state
	currentStatus := rollingUpgrade.CurrentStatus()
	if common.ContainsEqualFold(v1alpha1.FiniteStates, currentStatus) {
		r.AdmissionMap.Delete(rollingUpgrade.NamespacedName())
		r.Info("rolling upgrade ended", "name", rollingUpgrade.NamespacedName(), "status", currentStatus)
		return reconcile.Result{}, nil
	}

	// validate the rolling upgrade resource
	if ok, err := rollingUpgrade.Validate(); !ok {
		return reconcile.Result{}, err
	}

	// defer a status update on the resource
	defer r.Update(rollingUpgrade)

	// set the current status to init to indicate that the rolling upgrade is waiting to be processed (not in admissionMap yet)
	if currentStatus == "" {
		rollingUpgrade.SetCurrentStatus(v1alpha1.StatusInit)
		rollingUpgrade.SetLabel(v1alpha1.LabelKeyRollingUpgradeCurrentStatus, v1alpha1.StatusInit)
		common.SetMetricRollupInitOrRunning(rollingUpgrade.Name)
	}

	var (
		scalingGroupName = rollingUpgrade.ScalingGroupName()
		inProgress       bool
	)

	// at any given point in time, there should be only one reconcile operation running per ASG
	if _, present := r.ReconcileMap.LoadOrStore(rollingUpgrade.NamespacedName(), scalingGroupName); present {
		r.Info("a reconcile operation is already in progress for this ASG, requeuing", "scalingGroup", scalingGroupName, "name", rollingUpgrade.NamespacedName())
		return ctrl.Result{RequeueAfter: v1alpha1.DefaultRequeueTime}, nil
	}

	// if the admissionMap is empty, it is possible that the controller was restarted, fetch all running CRs
	if common.GetSyncMapLen(&r.AdmissionMap) == 0 {
		r.Info("admission map is empty, fetching all running CRs")
		var rollingUpgradeList v1alpha1.RollingUpgradeList
		err := r.List(ctx, &rollingUpgradeList, client.MatchingLabels{v1alpha1.LabelKeyRollingUpgradeCurrentStatus: v1alpha1.StatusRunning})
		if err != nil {
			r.Error(err, "failed to fetch running CRs")
			return ctrl.Result{}, err
		}
		for _, cr := range rollingUpgradeList.Items {
			if cr.CurrentStatus() == v1alpha1.StatusRunning {
				r.AdmissionMap.Store(cr.NamespacedName(), cr.ScalingGroupName())
			}
		}
	}

	// handle condition where multiple resources submitted targeting the same scaling group by requeing
	r.AdmissionMap.Range(func(k, v interface{}) bool {
		val := v.(string)
		resource := k.(string)
		if strings.EqualFold(val, scalingGroupName) && !strings.EqualFold(resource, rollingUpgrade.NamespacedName()) {
			r.Info("object already being processed by existing resource", "resource", resource, "scalingGroup", scalingGroupName, "name", rollingUpgrade.NamespacedName())
			inProgress = true
			return false
		}
		return true
	})

	if inProgress {
		// requeue any resources which are already being processed by a different resource, until the resource is completed/deleted
		return ctrl.Result{RequeueAfter: v1alpha1.DefaultRequeueTime}, nil
	}

	// only reconcile maxParallel number of CRs at a time
	runningCRs := common.GetSyncMapLen(&r.AdmissionMap)
	if runningCRs >= r.maxParallel {
		if _, present := r.AdmissionMap.Load(rollingUpgrade.NamespacedName()); !present {
			r.Info("number of running rolling upgrades has reached the max-parallel limit, not able to admit new, requeuing", "running", runningCRs, "max-parallel", r.maxParallel, "name", rollingUpgrade.NamespacedName())
			return ctrl.Result{RequeueAfter: v1alpha1.DefaultRequeueTime}, nil
		} else {
			r.Info("number of running rolling upgrades has reached the max-parallel limit, but this is already admitted, proceeding", "running", runningCRs, "max-parallel", r.maxParallel, "name", rollingUpgrade.NamespacedName())
		}
	} else {
		r.Info("number of running rolling upgrades is within the max-parallel limit, proceeding", "running", runningCRs, "max-parallel", r.maxParallel, "name", rollingUpgrade.NamespacedName())
	}

	// store the rolling upgrade in admission map
	if _, present := r.AdmissionMap.LoadOrStore(rollingUpgrade.NamespacedName(), scalingGroupName); !present {
		r.Info("admitted new rolling upgrade", "scalingGroup", scalingGroupName, "update strategy", rollingUpgrade.Spec.Strategy, "name", rollingUpgrade.NamespacedName())
		r.CacheConfig.FlushCache("autoscaling")
		r.CacheConfig.FlushCache("ec2")
	} else {
		r.Info("operating on existing rolling upgrade", "scalingGroup", scalingGroupName, "update strategy", rollingUpgrade.Spec.Strategy, "name", rollingUpgrade.NamespacedName())
	}

	// setup the RollingUpgradeContext needed for node rotations.
	drainGroup, _ := r.DrainGroupMapper.LoadOrStore(rollingUpgrade.NamespacedName(), &sync.WaitGroup{})
	drainErrs, _ := r.DrainErrorMapper.LoadOrStore(rollingUpgrade.NamespacedName(), make(chan error))

	rollupCtx := &RollingUpgradeContext{
		Logger:       r.Logger,
		Auth:         r.Auth,
		ScriptRunner: r.ScriptRunner,
		DrainManager: &DrainManager{
			DrainErrors: drainErrs.(chan error),
			DrainGroup:  drainGroup.(*sync.WaitGroup),
		},
		RollingUpgrade: rollingUpgrade,
		metricsMutex:   &sync.Mutex{},

		// discover the K8s cluster at controller level through watch
		Cloud: func() *DiscoveredState {
			var c = NewDiscoveredState(r.Auth, r.Logger)
			c.ClusterNodes = r.getClusterNodes()
			return c
		}(),

		DrainTimeout:        r.DrainTimeout,
		IgnoreDrainFailures: r.IgnoreDrainFailures,
		ReplacementNodesMap: r.ReplacementNodesMap,
		MaxReplacementNodes: r.MaxReplacementNodes,
	}

	// process node rotation
	if err := rollupCtx.RotateNodes(); err != nil {
		rollingUpgrade.SetCurrentStatus(v1alpha1.StatusError)
		rollingUpgrade.SetLabel(v1alpha1.LabelKeyRollingUpgradeCurrentStatus, v1alpha1.StatusError)
		common.SetMetricRollupFailed(rollingUpgrade.Name)
		return ctrl.Result{}, err
	}

	return reconcile.Result{RequeueAfter: v1alpha1.DefaultRequeueTime}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RollingUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RollingUpgrade{}).
		Watches(&source.Kind{Type: &corev1.Node{}}, nil).
		WithEventFilter(r.NodeEventsHandler()).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.maxParallel}).
		Complete(r)
}

// NodesEventHandler will fetch us the nodes on corresponding events, an alternative to doing explicit API calls.
func (r *RollingUpgradeReconciler) NodeEventsHandler() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			nodeObj, ok := e.Object.(*corev1.Node)
			if ok {
				nodeName := e.Object.GetName()
				log.Debug("nodeEventsHandler[create] nodeObj created, stored in sync map", "nodeName", nodeName)
				r.ClusterNodesMap.Store(nodeName, nodeObj)
				return false
			}
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			nodeObj, ok := e.ObjectNew.(*corev1.Node)
			if ok {
				nodeName := e.ObjectNew.GetName()
				log.Debug("nodeEventsHandler[update] nodeObj updated, updated in sync map", "nodeName", nodeName)
				r.ClusterNodesMap.Store(nodeName, nodeObj)
				return false
			}
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Object.(*corev1.Node)
			if ok {
				nodeName := e.Object.GetName()
				r.ClusterNodesMap.Delete(nodeName)
				log.Debug("nodeEventsHandler[delete] - nodeObj not found, deleted from sync map", "name", nodeName)
				return false
			}
			return true
		},
	}
}

// number of reconciles the upgrade-manager should handle in parallel
func (r *RollingUpgradeReconciler) SetMaxParallel(n int) {
	if n >= 1 {
		r.Info("setting max parallel reconcile", "value", n)
		r.maxParallel = n
	}
}

// at the end of every reconcile, update the RollingUpgrade object
func (r *RollingUpgradeReconciler) Update(rollingUpgrade *v1alpha1.RollingUpgrade) {
	r.ReconcileMap.LoadAndDelete(rollingUpgrade.NamespacedName())
	// update the status subresource
	if err := r.Status().Update(context.Background(), rollingUpgrade); err != nil {
		r.Info("failed to update the status", "message", err.Error(), "name", rollingUpgrade.NamespacedName())
	}
	// patch the label
	if err := r.Client.Patch(context.Background(), rollingUpgrade, client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, v1alpha1.LabelKeyRollingUpgradeCurrentStatus, rollingUpgrade.CurrentStatus())))); err != nil {
		r.Info("failed to patch the label", "message", err.Error(), "name", rollingUpgrade.NamespacedName())
	}
}

// max number of replacement nodes allowed in a cluster. This will ensure we avoid cluster ballooning.
func (r *RollingUpgradeReconciler) SetMaxReplacementNodes(n int) {
	if n >= 1 {
		r.Info("setting max replacement nodes", "value", n)
		r.MaxReplacementNodes = n
	}
}

// extract node objects from syncMap to a slice
func (r *RollingUpgradeReconciler) getClusterNodes() []*corev1.Node {
	var clusterNodes []*corev1.Node

	m := map[string]interface{}{}
	r.ClusterNodesMap.Range(func(key, value interface{}) bool {
		m[fmt.Sprint(key)] = value
		return true
	})
	for _, value := range m {
		clusterNodes = append(clusterNodes, value.(*corev1.Node))
	}
	return clusterNodes
}
