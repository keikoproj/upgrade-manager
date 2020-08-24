Feature: UM's RollingUpgrade Create
  In order to create RollingUpgrades
  As an EKS cluster operator
  I need to submit the custom resource

  Background:
    Given valid AWS Credentials
    And a Kubernetes cluster
    And an Auto Scaling Group named upgrademgr-eks-nightly-ASG

  Scenario: The ASG had a launch config update that allows nodes to join
    Given the current Auto Scaling Group has the required initial settings
    Then 1 node(s) with selector bdd-test=preUpgrade-label should be ready
    Given I update the current Auto Scaling Group with LaunchConfigurationName set to upgrade-eks-nightly-LC-postUpgrade
    And I submit the resource rolling-upgrade.yaml
    Then the resource rolling-upgrade.yaml should be created
    When the resource rolling-upgrade.yaml converge to selector .status.currentStatus=completed
    Then 1 node(s) with selector bdd-test=postUpgrade-label should be ready
