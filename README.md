# RollingUpgrade

![Build Status](https://github.com/keikoproj/upgrade-manager/workflows/Build-Test/badge.svg) ![Build Status](https://github.com/keikoproj/upgrade-manager/workflows/BDD/badge.svg) [![codecov](https://codecov.io/gh/keikoproj/upgrade-manager/branch/master/graph/badge.svg)](https://codecov.io/gh/keikoproj/upgrade-manager)

> Reliable, extensible rolling-upgrades of Autoscaling groups in Kubernetes

RollingUpgrade provides a Kubernetes native mechanism for doing rolling-updates of instances in an AutoScaling group using a CRD and a controller.

## What does it do?

- RollingUpgrade is highly inspired by the way kops does rolling-updates.

- It provides similar options for the rolling-updates as kops and more.

- The RollingUpgrade Kubernetes custom resource has the following options in the spec:
  - `asgName`: Name of the autoscaling group to perform the rolling-update.
  - `preDrain.script`: The script to run before draining a node.
  - `postDrain.script`: The script to run after draining a node. This allows for performing actions such as quiescing network traffic, adding labels, etc.
  - `postDrain.waitSeconds`: The seconds to wait after a node is drained.
  - `postDrain.postWaitScript`: The script to run after the node is drained and the waitSeconds have passed. This can be used for ensuring that the drained pods actually were able to start elsewhere.
  - `nodeIntervalSeconds`: The amount of time in seconds to wait after each node in the ASG is terminated.
  - `postTerminate.script`: Optional bash script to execute after the node has terminated.
  - `strategy.mode`: This field is optional and allows for two possible modes
    - `lazy` - this is the default mode, upgrade will terminate an instance first.
    - `eager` - upgrade will launch an instance prior to terminating.
  - `strategy.type`: This field is optional and currently two strategies are supported
    - `randomUpdate` - Default is type is not specified. Picks nodes randomly for updating. Refer to [random_update_strategy.yaml](examples/random_update_strategy.yaml) for sample custom resource definition.
    - `uniformAcrossAzUpdate` - Picks same number of nodes or same percentage of nodes from each AZ for update. Refer to [uniform_across_az_update_strategy.yaml](examples/uniform_across_az_update_strategy.yaml) for sample custom resource definition.
  - `strategy.maxUnavailable`: Optional field. The number of nodes that can be unavailable during rolling upgrade, can be specified as number of nodes or the percent of total number of nodes. Default is "1".
  - `strategy.drainTimeout`: Optional field. Node will be terminated after drain timeout even if `kubectl drain` has not been completed and value has to be specified in seconds. Default is -1.
  - `ignoreDrainFailures` : Optional field. Default value is false. If this field is set to true, drain failures will be ignored and the instances will proceed to termination.

- After performing the rolling-update of the nodes in the ASG, RollingUpgrade puts the following data in the "Status" field.
  - `currentStatus`: Whether the rolling-update completed or errored out.
  - `startTime`: The RFC3339 timestamp when the rolling-update began. E.g. 2019-01-15T23:51:10Z
  - `endTime`: The RFC3339 timestamp when the rolling-update completed. E.g. 2019-01-15T00:35:10Z
  - `nodesProcessed`: The number of ec2 instances that were processed.
  - `conditions`: Conditions describing the lifecycle of the rolling-update.

## Design

For each RollingUpgrade custom resource that is submitted, the following flowchart shows the sequence of actions taken to perform the rolling-update. [part-1](docs/reconcile.png), [part-2](docs/replaceNodeBatch.png)

## Dependencies

- Kubernetes cluster on AWS with nodes in AutoscalingGroups. rolling-upgrades have been tested with Kubernetes clusters v1.12+.
- An IAM role with at least the policy specified below. The upgrade-manager should be run with that IAM role.

## Installing

### Complete step by step guide to create a cluster and run rolling-upgrades

For a complete, step by step guide for creating a cluster with kops, editing it and then running rolling-upgrades, please see [this](docs/step-by-step-example.md)

### Existing cluster in AWS

If you already have an existing cluster created using kops, follow the instructions below.

- Ensure that you have a Kubernetes cluster on AWS.
- Install the CRD using: `kubectl apply -f https://raw.githubusercontent.com/keikoproj/upgrade-manager/master/config/crd/bases/upgrademgr.keikoproj.io_rollingupgrades.yaml`
- Install the controller using:
`kubectl create -f https://raw.githubusercontent.com/keikoproj/upgrade-manager/master/deploy/rolling-upgrade-controller-deploy.yaml`

- Note that the rolling-upgrade controller requires an IAM role with the following policy

``` json
{
    "Effect": "Allow",
    "Action": [
        "ec2:CreateTags",
        "ec2:DescribeInstances",
        "autoscaling:EnterStandby",
        "autoscaling:DescribeAutoScalingGroups",
        "autoscaling:TerminateInstanceInAutoScalingGroup"
    ],
    "Resource": [
        "*"
    ]
}
```

- If the rolling-upgrade controller is directly using the IAM role of the node it runs on, the above policy will have to be added to the IAM role of the node.
- If the rolling-upgrade controller is using it's own role created using KIAM, that role should have the above policy in it.

## For more details and FAQs, refer to [this](docs/faq.md)
