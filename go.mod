module github.com/keikoproj/upgrade-manager

go 1.13

require (
	github.com/aws/aws-sdk-go v1.25.0
	github.com/go-logr/logr v0.1.0
	github.com/keikoproj/upgrade-manager/pkg/log v0.0.0
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/pkg/errors v0.8.1
	golang.org/x/net v0.0.0-20190812203447-cdfb69ac37fc
	gopkg.in/yaml.v2 v2.2.4
	k8s.io/api v0.0.0-20191114100352-16d7abae0d2a
	k8s.io/apimachinery v0.0.0-20191028221656-72ed19daf4bb
	k8s.io/client-go v0.0.0-20191114101535-6c5935290e33
	sigs.k8s.io/controller-runtime v0.4.0
)

replace github.com/keikoproj/upgrade-manager/pkg/log => ./pkg/log
