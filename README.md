Project Lifecycle Monitoring Application
============================

Development Setup
-----------------

  * Install required packages:
    * Fedora: `dnf install -y golang git gcc rpm-build make docker createrepo`

Building
--------

To build the binary, run

```
$ make
```

To build the RPM and images, run

```
$ OS_BUILD_ENV_PRESERVE=_output/local/bin hack/env make build-images
```

Updating Go Tooling
-------------------

See https://github.com/openshift/release/tree/master/tools/hack/golang for
instructions on how to update the Go tooling used by this project.
