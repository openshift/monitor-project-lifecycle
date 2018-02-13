Project Lifecycle Monitoring Application
============================

Building
--------

To build the binary, run

```
$ make
```

To build the RPM and images, run

```
$ make build-images
```

If you are running on a non-Linux platform, you can build the images in a
container with this command

```
$ OS_BUILD_ENV_PRESERVE=_output/local/bin hack/env make build-images
```

Updating Go Tooling
-------------------

See https://github.com/openshift/release/tree/master/tools/hack/golang for
instructions on how to update the Go tooling used by this project.
