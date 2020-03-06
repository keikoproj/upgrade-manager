cache restore $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh)
# checks if the packages are already installed
GO_VERSION=1.13.8
KUBE_BUILDER_VERSION=2.3.0
GO_PACKAGE="/packages/go${GO_VERSION}.linux-amd64.tar.gz"
export os=$(go env GOOS)
export arch=$(go env GOARCH)
KUBE_BUILDER_PACKAGE="/packages/kubebuilder_${KUBE_BUILDER_VERSION}_${os}_${arch}.tar.gz"

if [ ! -d '/packages' -o ! -f "${GO_PACKAGE}" -o ! -f "${KUBE_BUILDER_PACKAGE}" ]; then
  sudo mkdir -p /packages
  curl -sL "https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz" -o "${GO_PACKAGE}"
  curl -sL "https://go.kubebuilder.io/dl/${KUBE_BUILDER_VERSION}/${os}/${arch}" -o "${KUBE_BUILDER_PACKAGE}"
  ls -l /packages
  cache store $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh) /packages
fi
