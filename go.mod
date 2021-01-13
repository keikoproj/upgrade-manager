module github.com/keikoproj/upgrade-manager

go 1.15

require (
	github.com/aws/aws-sdk-go v1.36.24
	github.com/go-logr/logr v0.3.0
	github.com/keikoproj/aws-sdk-go-cache v0.0.0-20201118182730-f6f418a4e2df
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	go.uber.org/zap v1.15.0
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	sigs.k8s.io/controller-runtime v0.7.0
)
