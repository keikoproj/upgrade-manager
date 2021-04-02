module github.com/keikoproj/upgrade-manager

go 1.15

require (
	github.com/aws/aws-sdk-go v1.36.11
	github.com/cucumber/godog v0.10.0
	github.com/go-logr/logr v0.1.0
	github.com/keikoproj/aws-sdk-go-cache v0.0.0-20201118182730-f6f418a4e2df
	github.com/keikoproj/inverse-exp-backoff v0.0.0-20201007213207-e4a3ac0f74ab
	github.com/keikoproj/kubedog v0.0.1
	github.com/onsi/ginkgo v1.14.2
	github.com/onsi/gomega v1.10.4
	github.com/sirupsen/logrus v1.6.0
	go.uber.org/zap v1.16.0
	golang.org/x/net v0.0.0-20201216054612-986b41b23924
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.17.15
	k8s.io/apiextensions-apiserver v0.17.15 // indirect
	k8s.io/apimachinery v0.17.15
	k8s.io/client-go v0.17.15
	k8s.io/utils v0.0.0-20191218082557-f07c713de883 // indirect
	sigs.k8s.io/controller-runtime v0.4.0
	sigs.k8s.io/controller-tools v0.2.4 // indirect
)
