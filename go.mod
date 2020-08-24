module github.com/keikoproj/upgrade-manager

go 1.13

require (
	github.com/aws/aws-sdk-go v1.33.8
	github.com/cucumber/godog v0.10.0
	github.com/go-logr/logr v0.1.0
	github.com/keikoproj/aws-sdk-go-cache v0.0.0-20200124200058-ab3c8c94044a
	github.com/keikoproj/inverse-exp-backoff v0.0.0-20191216014651-04523236b6ca
	github.com/keikoproj/kubedog v0.0.1
	github.com/keikoproj/upgrade-manager/pkg/log v0.0.0
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.1
	github.com/pkg/errors v0.9.1
	go.uber.org/zap v1.10.0
	golang.org/x/crypto v0.0.0-20191011191535-87dc89f01550 // indirect
	golang.org/x/net v0.0.0-20200202094626-16171245cfb2
	golang.org/x/sys v0.0.0-20191228213918-04cbcbbfeed8 // indirect
	golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898 // indirect
	gopkg.in/yaml.v2 v2.3.0
	k8s.io/api v0.17.2
	k8s.io/apiextensions-apiserver v0.17.0 // indirect
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	k8s.io/utils v0.0.0-20191218082557-f07c713de883 // indirect
	sigs.k8s.io/controller-runtime v0.4.0
)

replace github.com/keikoproj/upgrade-manager/pkg/log => ./pkg/log
