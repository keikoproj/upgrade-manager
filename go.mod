module github.com/keikoproj/upgrade-manager

go 1.13

require (
	github.com/aws/aws-sdk-go v1.25.42
	github.com/go-logr/logr v0.1.0
	github.com/gogo/protobuf v1.2.1 // indirect
	github.com/json-iterator/go v1.1.6 // indirect
	github.com/keikoproj/upgrade-manager/pkg/log v0.0.0
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/pkg/errors v0.8.1
	github.com/spf13/pflag v1.0.3 // indirect
	golang.org/x/net v0.0.0-20190501004415-9ce7a6920f09
	golang.org/x/sys v0.0.0-20190429190828-d89cdac9e872 // indirect
	golang.org/x/text v0.3.2 // indirect
	gopkg.in/yaml.v2 v2.2.2
	k8s.io/api v0.0.0-20190409021203-6e4e0e4f393b
	k8s.io/apimachinery v0.0.0-20190404173353-6a84e37a896d
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	sigs.k8s.io/controller-runtime v0.2.0-beta.2
)

replace github.com/keikoproj/upgrade-manager/pkg/log => ./pkg/log
