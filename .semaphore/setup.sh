cache restore $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh)
KUBE_BUILDER_VERSION=2.3.0
GOLANGCI_LINT_VERSION=1.23.8

export os=$(go env GOOS)
export arch=$(go env GOARCH)

KUBE_BUILDER_PACKAGE="/packages/kubebuilder.tar.gz"
KUBE_BUILDER_MARKER="/packages/kubebuilder_${KUBE_BUILDER_VERSION}.marker"

GOLANGCI_LINT_MARKER="/packages/golangci.${GOLANGCI_LINT_VERSION}.marker"

# checks if the packages are already installed
if [ ! -d '/packages' -o ! -f "${KUBE_BUILDER_MARKER}" -o ! -f "${GOLANGCI_LINT_MARKER}" ]; then
  sudo mkdir -p /packages

  curl -sL "https://go.kubebuilder.io/dl/${KUBE_BUILDER_VERSION}/${os}/${arch}" -o "${KUBE_BUILDER_PACKAGE}"
  touch "${KUBE_BUILDER_MARKER}"

  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /packages "v${GOLANGCI_LINT_VERSION}"
  touch "${GOLANGCI_LINT_MARKER}"

  ls -l /packages
  cache store $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh) /packages
fi
