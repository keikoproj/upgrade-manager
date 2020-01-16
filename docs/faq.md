# Frequently Asked Questions

## What happens when:

_1. Draining of a node fails?_

RollingUpgrade logs will show the reason why the drain failed (output of `kubectl drain`). It then marks the custom resource status as "error". After marking the object status as "error", the rolling-upgrade controller will ignore the object.

Users will have to manually "fix" the condition that was causing the drain to fail and remove the "error" status on the custom resource to resume the rolling updates.

_2. Node is drained but the postDrain script fails?_

RollingUpgrade controller will "uncordon" the node, mark the custom resource status as "error" and stop processing the rolling-update.

Users will have to rectify the postDrain script and remove the "error" status so as to resume the rolling-updates.

_3. RollingUpgrade controller gets terminated while it was performing the rolling-updates?_

- RollingUpgrade controller is run as a deployment. Therefore, if the pod dies, it will get scheduled again by Kubernetes.
- The RollingUpgrade custom resource objects that were being processed will get sent to the "Reconcile" method of the newly running pod.
- Then, the rolling-upgrade controller resumes performing the rolling-updates from where it left. Care is taken to ensure that if any nodes had already been updated, they won't go through the update again.

## Other details

_1. Instead of rolling-upgrade controller, why not simply run `kops rolling-update` in a container on the master node?_

- kops does not provide additional hooks for `preDrain`, `postDrain` or `postTerminate`, etc.
- Moreover, kops requires additional IAM privileges (such as S3 access) even when running a rolling-update.

_2. What command line tools can be used in the scripts?_

The rolling-upgrade-controller comes with busybox and kubectl binaries in it.

_3. What types of testing has rolling-upgrade gone through?_

- RollingUpgrade has unit tests that ensure that the functionality is robust and can be tested without a real environment. Simply run `make test`.
- RollingUpgrade has been tested for upgrades of 100s of ASGs across several different Kubernetes clusters in dev, pre-prod and production environments.
- The typical sequence of steps for the testing was as follows:
  - Create a cluster with multiple different IGs using kops.
  - Run `kops edit cluster` to update the Kubernetes version or `kops edit ig` to modify the ASG (instance type)
  - Run `kops update cluster` to ensure that the LaunchConfigurations have been updated.
  - Create a new RollingUpgrade CR in the cluster for each ASG.

_4. Are there any best practices for running RollingUpgrade?_

- Ensure that DNS resolution does not fail in the cluster during upgrades. RollingUpgrade requires making calls to ec2.amazonaws.com or autoscaling.amazonaws.com.
- Ensure that the rolling-upgrade controller is run as Kubernetes deployment.

_5. What are the specific cases where the preDrain, postDrain, postDrainWait and postTerminate scripts could be used?_

- postDrain: This could be used to ensure that all the pods on that node (or maybe all pods in the cluster) have been successfully scheduled on a different node/s. Additional sleeps could be added to ensure that this condition is being met. In case there are no other nodes in the cluster to run the pods, this script could also add more nodes in the cluster (or wait for cluster-autoscaler to spin-up the nodes). Also, any additional "draining" actions required on the node could be performed here. E.g. The node could be taken out of the target group of ALB by adding an additional label to the node.
- postDrainWait: This could be used to ensure that all pods that have migrated to different nodes are actually running and service client requests.
- postTerminate: This is executed after an ec2 instance is terminated and the `nodeIntervalSeconds` have passed. In the postTerminate script, additional checks such as ensuring that all the nodes (including the newly created one) has successfully joined the cluster can be performed.
