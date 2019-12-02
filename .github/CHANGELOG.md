# Change Log
All notable changes to this project will be documented in this file.


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
