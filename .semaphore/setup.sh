cache restore $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh)
KUBE_BUILDER_VERSION=2.3.0
export os=$(go env GOOS)
export arch=$(go env GOARCH)
KUBE_BUILDER_PACKAGE="/packages/kubebuilder.tar.gz"
KUBE_BUILDER_MARKER="/packages/kubebuilder_${KUBE_BUILDER_VERSION}.marker"
# checks if the packages are already installed
if [ ! -d '/packages' -o ! -f "${KUBE_BUILDER_MARKER}" ]; then
  sudo mkdir -p /packages
  curl -sL "https://go.kubebuilder.io/dl/${KUBE_BUILDER_VERSION}/${os}/${arch}" -o "${KUBE_BUILDER_PACKAGE}"
  touch "${KUBE_BUILDER_MARKER}"
  ls -l /packages
  cache store $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh) /packages
fi
