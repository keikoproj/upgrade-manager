# Change Log
All notable changes to this project will be documented in this file.

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
