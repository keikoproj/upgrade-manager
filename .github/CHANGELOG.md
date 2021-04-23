# Change Log
All notable changes to this project will be documented in this file.

## [v0.18] - 2021-03-23

8b2d320 Fix for Launch definition validation. Consider only the "InService" instances. (#197)
42f810c Fail the CR for drain failures, when IgnoreDrainFailures isn't set. (#185)
f5c9457 output can contain other messages from API Server, so be more relaxed (#174)
391b2fb Expose template list and other execution errors to logs (#166)
757b669 Bump golang and busybox (#172)
b8f69e8 Add instance id to the logs (#173)
ac7be6b Fix namespaced name order (#170)
51f469d use standard fmt.Errorf to format error message; unify error format (#171)
b552c69 Bump dependencies. (#169)
36a2784 Remove separate module for pkg/log (#168)
237f93d Move constants to types so that they can be reused (#167)

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
