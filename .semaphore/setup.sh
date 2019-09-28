cache restore $SEMAPHORE_PROJECT_NAME-dep
# checks if the packages are already installed
if [ ! -d '/packages' ]; then
  sudo mkdir -p /packages
  wget https://dl.google.com/go/go1.12.7.linux-amd64.tar.gz
  sudo mv go1.12.7.linux-amd64.tar.gz /packages
  export os=$(go env GOOS)
  export arch=$(go env GOARCH)
  curl -sL https://go.kubebuilder.io/dl/latest/${os}/${arch} -o kubebuilder_master_${os}_${arch}.tar.gz
  sudo mv kubebuilder_master_${os}_${arch}.tar.gz /packages
  ls -l /packages
  cache store $SEMAPHORE_PROJECT_NAME-dep /packages
fi
