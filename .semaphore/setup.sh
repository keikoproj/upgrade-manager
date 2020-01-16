cache restore $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh)
# checks if the packages are already installed
if [ ! -d '/packages' -o ! -f '/packages/go1.13.6.linux-amd64.tar.gz' ]; then
  sudo mkdir -p /packages
  wget -P /packages/ https://dl.google.com/go/go1.13.6.linux-amd64.tar.gz
  export os=$(go env GOOS)
  export arch=$(go env GOARCH)
  curl -sL https://go.kubebuilder.io/dl/latest/${os}/${arch} -o /packages/kubebuilder_master_${os}_${arch}.tar.gz
  ls -l /packages
  cache store $SEMAPHORE_PROJECT_NAME-dep-$(checksum .semaphore/setup.sh) /packages
fi
