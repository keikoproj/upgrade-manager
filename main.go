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

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	"github.com/keikoproj/upgrade-manager/pkg/log"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

var (
	CacheDefaultTTL                     = time.Second * 0
	DescribeAutoScalingGroupsTTL        = 60 * time.Second
	DescribeLaunchTemplatesTTL          = 60 * time.Second
	CacheMaxItems                int64  = 5000
	CacheItemsToPrune            uint32 = 500
)

func init() {

	err := upgrademgrv1alpha1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}
	// +kubebuilder:scaffold:scheme
	common.InitMetrics()
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var namespace string
	var maxParallel int
	var maxAPIRetries int
	var debugMode bool
	var logMode string
	flag.BoolVar(&debugMode, "debug", false, "enable debug logging")
	flag.StringVar(&logMode, "log-format", "text", "Log mode: supported values: text, json.")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&namespace, "namespace", "", "The namespace in which to watch objects")
	flag.IntVar(&maxParallel, "max-parallel", 10, "The max number of parallel rolling upgrades")
	flag.IntVar(&maxAPIRetries, "max-api-retries", 12, "The number of maximum retries for failed/rate limited AWS API calls")
	flag.Parse()

	ctrl.SetLogger(newLogger(logMode))

	mgo := ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
	}
	if namespace != "" {
		mgo.Namespace = namespace
		setupLog.Info("Watch RollingUpgrade objects only in namespace " + namespace)
	} else {
		setupLog.Info("Watch RollingUpgrade objects in all namespaces")
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgo)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var region string
	if region, err = deriveRegion(); err != nil {
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
		log.Fatalf("failed to AWS session, %v", err)
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

	logger := ctrl.Log.WithName("controllers").WithName("RollingUpgrade")
	reconciler := &controllers.RollingUpgradeReconciler{
		Client:       mgr.GetClient(),
		Log:          logger,
		ClusterState: controllers.NewClusterState(),
		ASGClient:    autoscaling.New(sess),
		EC2Client:    ec2.New(sess),
		CacheConfig:  cacheCfg,
		ScriptRunner: controllers.NewScriptRunner(logger),
	}

	reconciler.SetMaxParallel(maxParallel)

	err = (reconciler).SetupWithManager(mgr)
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RollingUpgrade")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

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

func deriveRegion() (string, error) {

	if region := os.Getenv("AWS_REGION"); region != "" {
		return region, nil
	}

	var config aws.Config
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
		Config:            config,
	}))
	c := ec2metadata.New(sess)
	region, err := c.Region()
	if err != nil {
		return "", fmt.Errorf("cannot reach ec2metadata, if running locally export AWS_REGION: %w", err)
	}
	return region, nil
}
