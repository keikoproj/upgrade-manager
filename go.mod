module github.com/keikoproj/upgrade-manager

go 1.13

require (
	github.com/aws/aws-sdk-go v1.25.0
	github.com/go-logr/logr v0.1.0
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/keikoproj/upgrade-manager/pkg/log v0.0.0
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/pkg/errors v0.8.1
	golang.org/x/crypto v0.0.0-20191011191535-87dc89f01550 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/sys v0.0.0-20191228213918-04cbcbbfeed8 // indirect
	golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898 // indirect
	gopkg.in/yaml.v2 v2.2.7
	k8s.io/api v0.17.0
	k8s.io/apiextensions-apiserver v0.17.0 // indirect
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/utils v0.0.0-20191218082557-f07c713de883 // indirect
	sigs.k8s.io/controller-runtime v0.4.0
)

replace github.com/keikoproj/upgrade-manager/pkg/log => ./pkg/log
