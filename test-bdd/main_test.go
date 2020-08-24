package main

import (
	"os"
	"testing"
	"time"

	"github.com/cucumber/godog"
	kdog "github.com/keikoproj/kubedog"
	"github.com/keikoproj/upgrade-manager/pkg/log"
)

var t kdog.Test

func TestMain(m *testing.M) {
	opts := godog.Options{
		Format:    "pretty",
		Paths:     []string{"features"},
		Randomize: time.Now().UTC().UnixNano(), // randomize scenario execution order
	}

	// godog v0.10.0 (latest)
	status := godog.TestSuite{
		Name:                 "godogs",
		TestSuiteInitializer: InitializeTestSuite,
		ScenarioInitializer:  InitializeScenario,
		Options:              &opts,
	}.Run()

	if st := m.Run(); st > status {
		status = st
	}
	os.Exit(status)
}

func InitializeTestSuite(ctx *godog.TestSuiteContext) {
	ctx.BeforeSuite(func() {
		log.Info("BDD >> trying to delete any existing test RollingUpgrade")
		err := t.KubeContext.DeleteAllTestResources()
		if err != nil {
			log.Errorf("Failed deleting the test resources: %v", err)
		}
	})

	ctx.AfterSuite(func() {
		log.Infof("BDD >> scaling down the ASG %v", t.AwsContext.AsgName)
		err := t.AwsContext.ScaleCurrentASG(0, 0)
		if err != nil {
			log.Errorf("Failed scaling down the ASG %v: %v", t.AwsContext.AsgName, err)
		}

		log.Info("BDD >> deleting any existing test RollingUpgrade")
		err = t.KubeContext.DeleteAllTestResources()
		if err != nil {
			log.Errorf("Failed deleting the test resources: %v", err)
		}
	})

	t.SetTestSuite(ctx)
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.AfterStep(func(s *godog.Step, err error) {
		time.Sleep(time.Second * 5)
	})

	ctx.Step(`^the current Auto Scaling Group has the required initial settings$`, theRequiredInitialSettings)

	t.SetScenario(ctx)
	t.Run()
}

func theRequiredInitialSettings() error {
	// Making sure the ASG has the pre-test launch config and 1 node with correct config
	err := t.AwsContext.UpdateFieldOfCurrentASG("LaunchConfigurationName", "upgrade-eks-nightly-LC-preUpgrade")
	if err != nil {
		return err
	}
	err = t.AwsContext.ScaleCurrentASG(0, 0)
	if err != nil {
		return err
	}
	err = t.KubeContext.NodesWithSelectorShouldBe(0, "bdd-test=preUpgrade-label", "found")
	if err != nil {
		return err
	}
	err = t.KubeContext.NodesWithSelectorShouldBe(0, "bdd-test=postUpgrade-label", "found")
	if err != nil {
		return err
	}
	err = t.AwsContext.ScaleCurrentASG(1, 1)
	if err != nil {
		return err
	}
	return nil
}
