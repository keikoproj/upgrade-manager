# Reference Installation and Test

This guide will provide a step by step walkthrough for creating a Kubernetes cluster with Instance Groups and then upgrading those using this rolling-upgrade-controller.

### Create a Kubernetes cluster using kops

#### Prerequisites

* Ensure that you have AWS credentials downloaded. A simple test is to run `aws s3 ls` and ensure that you have some output.
* Ensure that you have at least 1 S3 bucket ready for using the [kops state store](https://github.com/kubernetes/kops/blob/master/docs/state.md)
* Ensure that the kops executable is ready to be used. A simple test for this is to run `kops version` and ensure that there is some output.
* Ensure that the kubectl executable is ready to be used. A simple tests for this is to run `kubectl version` and ensure that output shows the client version.
* Copy the following script into a local file; say `/tmp/create_cluster.sh`

```bash
#!/bin/bash

set -ex

export CLUSTER_NAME=test-cluster-kops.cluster.k8s.local

if [[ -z "${KOPS_STATE_STORE}" ]]; then
  echo "The KOPS_STATE_STORE environment variable needs to be set"
  exit 1
fi

# Create cluster config.
kops create cluster \
--name=${CLUSTER_NAME} \
--state=${KOPS_STATE_STORE} \
--node-count=2 \
--zones=us-west-2a,us-west-2b,us-west-2c \
--master-zones=us-west-2c \
--node-size=c4.large \
--master-size=c4.large \
--master-count=1 \
--networking=calico \
--topology=private \
--ssh-public-key=~/.ssh/id_rsa.pub \
--kubernetes-version=1.13.5

kops create secret --name ${CLUSTER_NAME} sshpublickey admin -i ~/.ssh/id_rsa.pub

kops update cluster ${CLUSTER_NAME} --yes

```

#### Actually create the cluster

* Create the cluster by running the script above as follows:
    * Edit the cluster name by modifying the CLUSTER_NAME variable in the scrip above if needed.
    * Change `my-bucket-name` below with the actual name of your s3 bucket.
 
`$ KOPS_STATE_STORE=s3://my-bucket-name /tmp/create_cluster.sh`

* This takes several minutes for the cluster to actually come up.
* Ensure that the cluster is up by running the command `kubectl cluster-info`. You should see some information like the url for the master and maybe endpoint for KubeDNS.
* Congratulations! Your Kubernetes cluster is ready and can be now used for testing.

### Run the rolling-upgrade-manager

#### Update the IAM role for the rolling-upgrade-controller

* Before we actually install the rolling-upgrade-controller, we need make sure that the IAM that will be used has enough privileges for the rolling-upgrade-controller to work.
* To do this, let's edit the IAM role used by the master nodes in the above cluster.
* Copy the following into a file; say `/tmp/rolling-upgrade-policy`

```
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "ec2:TerminateInstances",
                "autoscaling:DescribeAutoScalingGroups",
                "autoscaling:TerminateInstanceInAutoScalingGroup"
            ],
            "Resource": [
             "*"
            ]
        }
    ]
}
```

* Create an IAM policy using the document above.
`$ aws iam create-policy --policy-name rolling-upgrade-policy --policy-document file:///tmp/rolling-upgrade-policy`

* This will output a bunch of details such as

```
{
    "Policy": {
        "PolicyName": "rolling-upgrade-policy",
        "PermissionsBoundaryUsageCount": 0,
        "CreateDate": "2019-01-01T01:01:01Z",
        "AttachmentCount": 0,
        "IsAttachable": true,
        "PolicyId": "ABCDZ1243FDS5432J",
        "DefaultVersionId": "v1",
        "Path": "/",
        "Arn": "arn:aws:iam::0123456789:policy/rolling-upgrade-policy",
        "UpdateDate": "2019-01-01T01:01:01Z"
    }
}
```
* The "Arn" field from the above output is required in the next command.
* Attach this policy to the role of the Kubernetes master nodes using the Arn from above. The role-name is `masters.<name of the cluster used in the kops script above>`

`$ aws iam attach-role-policy --role-name masters.test-cluster-kops.cluster.k8s.local --policy-arn arn:aws:iam::0123456789:policy/rolling-upgrade-policy`

#### Install the rolling-upgrade-controller

* Install the CRD using: `$ kubectl apply -f config/crd/bases`
* Install the controller using:
`$ kubectl create -f deploy/rolling-upgrade-controller-deploy.yaml`
* Ensure that the rolling-upgrade-controller deployment is running.

### Actually perform rolling-upgrade of one InstanceGroup

#### Update the nodes AutoScalingGroup 

* Update the `nodes` instance-group so that it needs a rolling-upgrade. The following command will open the specification for the nodes instance-group in an editor. Change the instance type from `c4.large` to `r4.large`.
`$ KOPS_STATE_STORE=s3://my-bucket-name kops edit ig nodes`

* The run kops upgrade to make these changes persist and have kops modify the ASG's launch configuration
`$ KOPS_STATE_STORE=s3://my-bucket-name kops update cluster --yes`

#### Create the RollingUpgrade custom-resource (CR) that will actually do the rolling-upgrade.

* Run the following script:

```
#!/bin/bash

set -ex

cat << EOF > /tmp/crd.yaml
apiVersion: upgrademgr.orkaproj.io/v1alpha1
kind: RollingUpgrade
metadata:
  generateName: rollingupgrade-sample-
spec:
    asgName: nodes.test-cluster-kops.cluster.k8s.local
    region: us-west-2
    nodeIntervalSeconds: 300
    preDrain:
        script: |
            kubectl get pods --all-namespaces --field-selector spec.nodeName=${INSTANCE_NAME}
    postDrain:
        script: |
            echo "Pods at PostDrain:"
            kubectl get pods --all-namespaces --field-selector spec.nodeName=${INSTANCE_NAME}
        waitSeconds: 90
        postWaitScript: |
            echo "Pods at postWait:"
            kubectl get pods --all-namespaces --field-selector spec.nodeName=${INSTANCE_NAME}
    postTerminate:
        script: |
            echo "PostTerminate:"
            kubectl get pods --all-namespaces
EOF

kubectl create -f /tmp/crd.yaml
```

#### Ensure nodes are getting updated

* As soon as the above CR is submitted, the rolling-upgrade-controller will pick it up and start the rolling-upgrade process.
* There are multiple ways to ensure that rolling-upgrades are actually happening.
    * Watch the AWS console. Existing nodes should be seen getting Terminated. New nodes coming up should be of type r4.large.
    * Run `kubectl get nodes`. Some node will either have SchedulingDisabled or it could be terminated and new node should be seen coming up.
    * Check the status in the actual CR. It has details of how many nodes are going to be upgraded and how many have been completed. `$ kubectl get rollingupgrade -o yaml`
* Checks the logs of the rolling-upgrade-controller for minute details of how the CR is being processed.

### Deleting the cluster

* Before deleting the cluster, the policy that was created explicitly will have to be deleted.

```
$ aws iam detach-role-policy --role-name masters.test-cluster-kops.cluster.k8s.local --policy-arn arn:aws:iam::0123456789:policy/rolling-upgrade-policy
$ aws iam delete-policy --policy-arn arn:aws:iam::0123456789:policy/rolling-upgrade-policy
```

* Now delete the cluster

`$ KOPS_STATE_STORE=s3://my-bucket-name kops delete cluster test-cluster-kops.cluster.k8s.local --yes`

