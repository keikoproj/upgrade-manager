**Is this a BUG REPORT or FEATURE REQUEST?**:

**What happened**:

**What you expected to happen**:

**How to reproduce it (as minimally and precisely as possible)**:

**Anything else we need to know?**:

**Environment**:
- rolling-upgrade-controller version
- Kubernetes version :
```
$ kubectl version -o yaml
```

**Other debugging information (if applicable)**:
- RollingUpgrade status:
```
$ kubectl describe rollingupgrade <rollingupgrade-name>
```
- controller logs:
```
$ kubectl logs <rolling-upgrade-controller pod>
```
