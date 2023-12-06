# Change Log
All notable changes to this project will be documented in this file.

## [v1.0.9] - 2023-12-06
c2fdb37 Early cordon (#405)
1201813 Bump sigs.k8s.io/controller-runtime from 0.12.1 to 0.12.3 (#416)
239f1d6 Bump github.com/aws/aws-sdk-go from 1.48.2 to 1.48.4 (#415)
fe3a5dc Bump github.com/aws/aws-sdk-go from 1.47.9 to 1.48.2 (#414)
1e87eea Bump k8s.io/kubectl from 0.25.15 to 0.25.16 (#412)
1ee2d1e Bump github.com/go-logr/logr from 1.2.4 to 1.3.0 (#411)
c7c300d Bump k8s.io/client-go from 0.25.15 to 0.25.16 (#409)
151bd33 Update BDD dependencies (#408)
48def40 Bump github.com/onsi/gomega from 1.20.1 to 1.30.0 (#407)
d6cfbdc Bump go.uber.org/zap from 1.15.0 to 1.26.0 (#386)
aa0a3db Bump github.com/prometheus/client_golang from 1.16.0 to 1.17.0 (#401)
26075c8 Bump github.com/aws/aws-sdk-go from 1.46.2 to 1.47.9 (#406)

## [v1.0.8] - 2023-10-30
* e4a79bf Process drain-failure nodes at the end (#394)
* 7c245ca Update aws-sdk-go-cache to v0.0.2 (#399)
* 4c5949f Update K8s client-go and kubectl to v0.25.15 (#398)
* 7801676 Bump github.com/aws/aws-sdk-go from 1.45.19 to 1.46.2 (#396)
* 1f5011c Fix: modified UniformAcrossAzUpdateStrategy update strategy to be as expected (#395)
* 84dd4a7 Bump github.com/go-logr/logr from 0.3.0 to 0.4.0 (#384)
* a1fef90 Bump github.com/aws/aws-sdk-go from 1.45.14 to 1.45.19 (#389)
* a91fc0e Update to new aws-sdk-go-cache version v0.0.2 (#387)
* 062f732 Bump k8s.io/kubectl from 0.20.4 to 0.20.15 (#369)
* a5abef4 Bump github.com/emicklei/go-restful (#382)
* 0006697 Bump sigs.k8s.io/controller-runtime from 0.7.0 to 0.7.2 (#368)
* 3cf7079 Bump gopkg.in/yaml.v3 from 3.0.0-20200615113413-eeeca48fe776 to 3.0.0 (#377)
* e72cfc6 Bump github.com/sirupsen/logrus from 1.6.0 to 1.9.3 (#370)
* 80f7f55 Bump golang.org/x/crypto from 0.0.0-20201002170205-7f63de1d35b0 to 0.1.0 (#378)
* 7c6e516 Bump golang.org/x/net from 0.0.0-20201110031124-69a78807bb2b to 0.7.0 (#375)
* 5a05e91 Bump busybox from 1.32.1 to 1.36.1 (#362)
* 4452f9c Bump golang.org/x/text from 0.3.4 to 0.3.8 (#376)
* 5bf555a Bump k8s.io/client-go from 0.20.4 to 0.20.15 (#372)
* b413a61 Bump codecov/codecov-action from 1 to 3 (#381)
* 645b834 Bump docker/setup-qemu-action from 2 to 3 (#380)
* dd6adac Bump docker/setup-buildx-action from 2 to 3 (#366)
* 76b0603 Bump docker/build-push-action from 4 to 5 (#367)
* 656e654 Bump actions/checkout from 2 to 4 (#364)
* 1a9feb1 Bump aws-actions/configure-aws-credentials from 1 to 4 (#365)
* 3f9ed7f Bump docker/login-action from 2 to 3 (#363)
* 2e2b323 Add CODEOWNERS (#379)
* 86b11a9 Update to Golang 1.19 (#373)
* 5d940a3 Create dependabot.yml (#361)
* 7e9a401 Chore: updating kube-rbac-proxy to 0.13.0 (#343)
* 9f9d313 Bump github.com/prometheus/client_golang from 1.7.1 to 1.11.1 (#355)

## [v1.0.7] - 2023-07-05
* 23f4ba8 Only allow maxParallel number of running CRs (#351)
* 79b38c0 Add Apache-2.0 license (#344)
* fc20675 Fix ci (#340)
* f73f5c1 Update docker actions to v2 (#339)

## [v1.0.6] - 2022-06-02
* c45365b multi arch support

## [v1.0.5] - 2022-06-02
* d6994b8 fix minor bugs (#334)
* b33f5ae "max-replacement-nodes" feature to avoid cluster ballooning during upgrades. (#328)
* 51fef36 fix error 'failed to set instances to stand-by' (#329)
* af9c12d Fix bdd (#332)
* fa47143 Add a "clean" target in Makefile (#331)
* 16ed0fc Fix CI Github action (#330)
* 8e0f67d Update rollingupgrade_controller.go (#322)
* 9b1d11c add README (#320)
* 167e10b EOL of upgrade-manager-v0 and make upgrade-manager-v1 the default. (#319)
## [v1.0.4] - 2021-10-04
* 995b81b controller flags for ignoreDrainFailures and drainTimeout (#307)


## [v1.0.3] - 2021-09-03
* 6252725 revert #300 (#305)
* df08ab0 Set Instances to StandBy in batches (#303)
* e77431c fix: fix panic when using MixedInstancesPolicy (#298)
* 1e6d29d Add ignoreDrainFailure and DrainTimeout as controller arguements (#300)


## [v1.0.2] - 2021-08-05
* d73da1b replace launchTemplate latest string with version number (#296)

## [v1.0.1] - 2021-08-05
* 52d80d9 check for ASG's launch template version instead latest. (#293)
* c35445d Controller v2: fix BDD template and update Dockerfile with bash  (#292)
* db54e0b Controller v2: fix BDD template (#291)
* b698dd6 Controller v2: remove cleaning up ruObject as BDD already does. (#290)
* 86412d5 Controller v2: increase memory/CPU limit and update args (#289)
* 2d8651c Controller v2: update args (#288)
* 835fd0d V2 bdd (#286)
* 998de0d V2 bdd (#285)
* 3841cc7 #2122: bdd changes for v2 (#284)
* 93626b4 Controller v2: BDD cron update (#283)
* 1be8190 Controller v2: BDD cron update (#282)
* 62c2255 Controller v2: BDD cron update (#280)
* 42abe52 Controller v2: BDD cron update (#279)
* 5bdc134 Controller v2 bdd changes (#278)

## [v1.0.0] - 2021-07-21
* 7a4766d (HEAD -> controller-v2, origin/controller-v2) upgrade-manager-v2: Add CI github action, fix lint errors. (#276)
* 00f7e89 upgrade-manager-v2: Fix unit tests (#275)
* 0e64929 upgrade-manager-v2: Process next batch while waiting on nodeInterval period. (#273)
* b2b39a0 upgrade-manager-v2: Add nodeEvents handler instead of a watch handler (#272)
* c0a163b move cloud discovery after nodeInterval / drainInterval wait (#270)
* b15838e Carry the metrics status in RollingUpgrade CR (#267)
* 610f454 upgrade-manager-v2: remove function duplicate declaration. (#266)
* a4e0e84 upgrade-manager-v2: expose totalProcessing time and other metrics (#265)
* 2390ea0 and CR end time (#264)
* 79db022 (tag: v1.0.0-RC1) Add a mock for test and update version in Makefile (#262)
* 3eafd00 Fix metrics calculation issue (#258)
* 376657f Revert "Fix metrics collecting issue (#249)" (#256)
* f5dd1cb Fix metrics collecting issue (#249)
* 066731d final push before RC release. (#254)
* 18e0e75 upgrade-manager-v2: Load test fixes (#245)
* 1fc5847 metricsMutex should be initialized (#240)
* a9ac50f add missing parenthesis (#239)
* 6fef5fd V2 controller metrics concurrency fix (#231)
* a490333 upgrade-manager-v2: Move DrainManager back to Reconciler (#236)
* b659e0f Resolve compile errors caused by merge conflict. (#235)
* b664fdd Create RollingUpgradeContext (#234)
* b8d0e72 #2286: removed version from metric namespace (#227)
* c445af9 #2285: renamed some methods related to metrics (#224)
* 1f0f075 #2285: rollup CR statistic metrics in v2 (#218)
* d5935e3 Unit tests for controller-v2 (#215)
* 665c64b Fix bug in deleting the entry in syncMap (#203)
* 77f985c Ignore generated code  (#201)
* 71b310a Refine metrics implementation to support goroutines (#196)
* 668c5d8 Move the DrainManager within ReplaceBatch(), to access one per RollingUpgrade CR (#195)
* 728dae9 Process the batch rotation in parallel (#192)
* 14e950e Metrics features (#189)
* 11d3ae6 Eager mode implementation (#183)
* 57df5a5 Implemented node drain. (#181)
* dd6a332 Migrate Script Runner (#179)
* 2c1d8e7 Controller v2: Implementation of Instance termination (#178)
* 7cb15b0 Fix all the "make vet" errors in Controller V2 branch. (#177)
* 59e9b0d Implemented RollingUpgrade object validation. (#176)
* 5cb9efb initial rotation logic
* 6b8dad5 AWS API calls & Drift detection
* 335fb4f aws API calls
* 41bd571 Add kubernetes API calls
* 8f33f1e add more scaffolding
* 25644a6 initial code
* 87afbd6 add API
* 2816490 scaffolding
* 3ad13b8 delete all
* 6ce7953 Delete README.md

## [v0.17] - 2020-12-11

* aa2b73b - use NamespacedName (#160)
* 6f57dcf - Abort on strategy failure instead of continuing (#152)
* f84c3a2 - Don't uncordon node on failure to run postDrain script when IgnoreDrainFailures set (#151)

## [v0.16] - 2020-12-8

* Caching improvements to DescribeAutoScalinGroups (#142)
* Various code refactoring (#138, #145, #148, #149)
* Propagate existing environment variables (#144)
* Documentation Fixes (#147)
* CI Improvements (#146)
* Fix standby node cleanup (#150)
* Fix template version change detection (#153)

## [v0.15] - 2020-11-12

* Extract script runner to a separate type; fix work with env. variables (#132)
* Fix bug when switching to launch templates (#136)
* During upgrade, ignore terminated instance. (#134)
* Readiness gates implementation for eager mode (#130)
* Fix few typos and simplify error returns, remove redundant types (#131)
* Upgrade to Go 1.15 (#128)
* Fix typo in README.md. (#125)

## [v0.14] - 2020-09-23

* Terminate unjoined nodes (#120)
* Repo selection for CI and BDD workflows & CI step for releases (#117)

## [v0.13] - 2020-08-25

* Post upgrade validation step (#112)

## [v0.12] - 2020-07-01

* Fix log arguments for 0 node ASGs (#105)

## [v0.11] - 2020-06-17

* Add missing RBAC docs (#89)
* Enable forceRefresh of nodes. (#92)
* set firsttimestamp and count (#93)
* add retry for TerminateInstanceInAutoScalingGroup (#94)
* Check for CR before updating each instance. (#97)
* upgrade aws sdk to latest & reduce retries (#101)

## [v0.10] - 2020-03-30

* support for v1.Event (#87)
* Allow to ignore drain failures (#86)
* Bump dependencies to new version (#85)
* add support for conditions (#82)
* Improve logging - tag messages with RollingUpgrade name and use structured logging. (#79)
* Allow to use JSON logging (#78)

## [v0.9] - 2020-03-11

* Add SDK caching & Refactor (#75)

## [v0.8] - 2020-03-10

* Refactor & Improvements (#63)
* Fix shadowing of error. (#65)
* Use existing go version, simplify maintenance (#66)
* stabilize tests (#69)
* Fix errors reported by golangci-lint (#72)

## [v0.7] - 2020-02-28

* Fix parallel RU bug (#57)
* Upgrade golang version to 1.13.8 (#56)
* Check version by value rather than ptr (#55)
* Add missing RBAC for node list/get (#53)

## [v0.6] - 2020-01-27

* Eager mode: Launch Before Terminate (#49)

## [v0.5] - 2020-01-16

* RollingUpgrade idempotency fix (#41)
* Migrate deprecated code (#44)
* Add support for LaunchTemplates (#43)
* Use go 1.13 for building (#46)

## [v0.4] - 2020-01-06

* Parallel Reconcile Limit (#38)
* Use apps/v1, remove unnecessary fields (#39)
* Add build badge (#36)
* wait for node unjoined (#35)
* Added uniformAcrossAzUpdate strategy (#27)

## [v0.3] - 2019-12-02

* Fix #30 and release v0.3 (#31)

## [v0.2] - 2019-12-02

* Fixes for autoscaling API changes (#26)
* update Go modules & mirate to Go 1.13.1 (#23)
* using autoscaling:TerminateInstanceInAutoScalingGroup instead of ec2:TerminateInstances while terminating instances to support lifecycle hooks. Testing Done, ran unit tests for rollingupgrade_controller (#20)
* Fix instances not available error (#21)
* added comment (#13)
* Mark maxUnavailable and drainTimeout as optional. (#12)
* Added sample CRD and spec field definitions for random update strategy (#9)
* Add semaphore ci (#10)

## [v0.1] - 2019-12-02

* Add semaphore ci (#10)
* Added sample CRD and spec field definitions for random update strategy (#9)
* Mark maxUnavailable and drainTimeout as optional. (#12)
* Fix instances not available error (#21)
* using autoscaling:TerminateInstanceInAutoScalingGroup (#20)
* update Go modules & mirate to Go 1.13.1 (#23)
* Fixes for autoscaling API changes (#26)

## [v0.1] - 2019-08-28

### Added

* Introduce VERSION in Makefile.
* Update org name to keikoproj
* Changes to support multiple upgrade strategies(Random update strategy is implemented)
* Update instructions for installing CRD and controller directly
* Changes to .github
* Update Docker image to build in Makefile
* Minor formatting changes
* More minor formatting changes for markdownlint
* DOC: Update kubernetes version to 1.13.9 and instance type to c5.large Other minor formatting changes to pass markdownlint
* Fix link to design image
* Initial implementation of upgrade-manager
