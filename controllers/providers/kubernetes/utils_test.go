package kubernetes

import (
	"testing"

	"github.com/onsi/gomega"
)

func TestGetKubernetesClient(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	client, err = GetKubernetesClient()
	g.Expect(err).To(gomega.BeNil())
	g.Expect(client).NotTo(gomega.BeNil())
}
