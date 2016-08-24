package integration_tests_test

import (
	"integration_tests/tools/helpers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"testing"
)

func TestIntegrationTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Logfin Suite")
}

var _ = BeforeSuite(func() {
	helpers.BuildLogfin()
})
var _ = AfterSuite(func() {
	gexec.CleanupBuildArtifacts()
})
