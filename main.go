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

package main

import (
	"flag"
	"os"
	"sync"

	"github.com/keikoproj/upgrade-manager/controllers/common"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/go-logr/logr"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers"
	"github.com/keikoproj/upgrade-manager/controllers/common/log"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("main")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(upgrademgrv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	common.InitMetrics()
}

func main() {

	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		namespace            string
		maxParallel          int
		maxAPIRetries        int
		debugMode            bool
		logMode              string
		drainTimeout         int
		ignoreDrainFailures  bool
		maxReplacementNodes  int
		earlyCordonNodes     bool
	)

	flag.BoolVar(&debugMode, "debug", false, "enable debug logging")
	flag.StringVar(&logMode, "log-format", "text", "Log mode: supported values: text, json.")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&namespace, "namespace", "", "The namespace in which to watch objects")
	flag.IntVar(&maxParallel, "max-parallel", 10, "The max number of parallel rolling upgrades")
	flag.IntVar(&maxAPIRetries, "max-api-retries", 12, "The number of maximum retries for failed/rate limited AWS API calls")
	flag.IntVar(&drainTimeout, "drain-timeout", 900, "when the drain command should timeout")
	flag.BoolVar(&ignoreDrainFailures, "ignore-drain-failures", false, "proceed with instance termination despite drain failures.")
	flag.IntVar(&maxReplacementNodes, "max-replacement-nodes", 0, "The max number of replacement nodes allowed in a cluster. Avoids cluster-ballooning")
	flag.BoolVar(&earlyCordonNodes, "early-cordon-nodes", false, "when enabled, will cordon all the nodes in the node-group even before processing the nodes")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(newLogger(logMode))

	ctrlOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d6edb06e.keikoproj.io",
	}

	if namespace != "" {
		ctrlOpts.Cache.DefaultNamespaces = map[string]ctrlcache.Config{
			namespace: {},
		}
		setupLog.Info("starting watch in namespaced mode", "namespace", namespace)
	} else {
		setupLog.Info("starting watch in all namespaces")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrlOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if debugMode {
		log.SetLevel("debug")
	}

	// Load AWS config
	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRetryMaxAttempts(maxAPIRetries),
		config.WithRetryMode(aws.RetryModeAdaptive),
	)
	if err != nil {
		setupLog.Error(err, "failed to load AWS config")
		os.Exit(1)
	}

	kube, err := kubeprovider.GetKubernetesClient()
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes client")
		os.Exit(1)
	}

	awsClient := &awsprovider.AmazonClientSet{
		Ec2Client: ec2.NewFromConfig(cfg),
		AsgClient: autoscaling.NewFromConfig(cfg),
	}

	kubeClient := &kubeprovider.KubernetesClientSet{
		Kubernetes: kube,
	}

	logger := ctrl.Log.WithName("controllers").WithName("RollingUpgrade")

	reconciler := &controllers.RollingUpgradeReconciler{
		Client: mgr.GetClient(),
		Logger: logger,
		Scheme: mgr.GetScheme(),
		Auth: &controllers.RollingUpgradeAuthenticator{
			AmazonClientSet:     awsClient,
			KubernetesClientSet: kubeClient,
		},
		EventWriter: kubeprovider.NewEventWriter(kubeClient, logger),
		ScriptRunner: controllers.ScriptRunner{
			Logger: logger,
		},
		DrainGroupMapper:    &sync.Map{},
		DrainErrorMapper:    &sync.Map{},
		ClusterNodesMap:     &sync.Map{},
		ReconcileMap:        &sync.Map{},
		DrainTimeout:        drainTimeout,
		IgnoreDrainFailures: ignoreDrainFailures,
		ReplacementNodesMap: &sync.Map{},
		EarlyCordonNodes:    earlyCordonNodes,
	}

	reconciler.SetMaxParallel(maxParallel)
	reconciler.SetMaxReplacementNodes(maxReplacementNodes)

	if err = (reconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RollingUpgrade")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("registering prometheus")

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

}

func newLogger(logMode string) logr.Logger {
	var encoder *zapcore.Encoder = nil
	if logMode == "json" {
		log.SetJSONFormatter()
		jsonEncoder := zapcore.NewJSONEncoder(uberzap.NewProductionEncoderConfig())
		encoder = &jsonEncoder
	}

	opts := []zap.Opts{zap.UseDevMode(true), zap.WriteTo(os.Stderr)}
	if encoder != nil {
		opts = append(opts, zap.Encoder(*encoder))
	}
	logger := zap.New(opts...)
	return logger
}
