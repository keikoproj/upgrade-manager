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
	"github.com/keikoproj/upgrade-manager/controllers/common"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
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
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("main")
)

var (
	CacheDefaultTTL                     = time.Second * 0
	DescribeAutoScalingGroupsTTL        = 60 * time.Second
	DescribeLaunchTemplatesTTL          = 60 * time.Second
	CacheMaxItems                int64  = 5000
	CacheItemsToPrune            uint32 = 500
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

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(newLogger(logMode))

	ctrlOpts := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d6edb06e.keikoproj.io",
	}

	if namespace != "" {
		ctrlOpts.Namespace = namespace
		setupLog.Info("starting watch in namespaced mode", "namespace", namespace)
	} else {
		setupLog.Info("starting watch in all namespaces")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrlOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var region string
	if region, err = awsprovider.DeriveRegion(); err != nil {
		setupLog.Error(err, "unable to get region")
		os.Exit(1)
	}

	if debugMode {
		log.SetLevel("debug")
	}

	retryer := client.DefaultRetryer{
		NumMaxRetries:    maxAPIRetries,
		MinThrottleDelay: time.Second * 5,
		MaxThrottleDelay: time.Second * 60,
		MinRetryDelay:    time.Second * 1,
		MaxRetryDelay:    time.Second * 5,
	}

	config := aws.NewConfig().WithRegion(region)
	config = config.WithCredentialsChainVerboseErrors(true)
	config = request.WithRetryer(config, log.NewRetryLogger(retryer))
	sess, err := session.NewSession(config)
	if err != nil {
		setupLog.Error(err, "failed to create an AWS session")
		os.Exit(1)
	}

	cacheCfg := cache.NewConfig(CacheDefaultTTL, CacheMaxItems, CacheItemsToPrune)
	cache.AddCaching(sess, cacheCfg)
	cacheCfg.SetCacheTTL("autoscaling", "DescribeAutoScalingGroups", DescribeAutoScalingGroupsTTL)
	cacheCfg.SetCacheTTL("ec2", "DescribeLaunchTemplates", DescribeLaunchTemplatesTTL)
	sess.Handlers.Complete.PushFront(func(r *request.Request) {
		ctx := r.HTTPRequest.Context()
		log.Debugf("cache hit => %v, service => %s.%s",
			cache.IsCacheHit(ctx),
			r.ClientInfo.ServiceName,
			r.Operation.Name,
		)
	})

	kube, err := kubeprovider.GetKubernetesClient()
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes client")
		os.Exit(1)
	}

	awsClient := &awsprovider.AmazonClientSet{
		Ec2Client: ec2.New(sess),
		AsgClient: autoscaling.New(sess),
	}

	kubeClient := &kubeprovider.KubernetesClientSet{
		Kubernetes: kube,
	}

	logger := ctrl.Log.WithName("controllers").WithName("RollingUpgrade")

	reconciler := &controllers.RollingUpgradeReconciler{
		Client:      mgr.GetClient(),
		Logger:      logger,
		Scheme:      mgr.GetScheme(),
		CacheConfig: cacheCfg,
		Auth: &controllers.RollingUpgradeAuthenticator{
			AmazonClientSet:     awsClient,
			KubernetesClientSet: kubeClient,
		},
		EventWriter: kubeprovider.NewEventWriter(kubeClient, logger),
		ScriptRunner: controllers.ScriptRunner{
			Logger: logger,
		},
	}

	reconciler.SetMaxParallel(maxParallel)

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
