package common

import (
	"sync"
	"testing"

	"github.com/onsi/gomega"
)

func TestGetSyncMapLen(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	var m sync.Map
	g.Expect(GetSyncMapLen(&m)).To(gomega.Equal(0))

	m.Store("k", "v")
	g.Expect(GetSyncMapLen(&m)).To(gomega.Equal(1))

	m.Store("k1", "v1")
	g.Expect(GetSyncMapLen(&m)).To(gomega.Equal(2))

	m.Delete("k")
	g.Expect(GetSyncMapLen(&m)).To(gomega.Equal(1))
}
