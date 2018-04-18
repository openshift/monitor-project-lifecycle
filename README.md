# Project Lifecycle Monitoring Application

This app measures availability of the application creation workflow in OpenShift.

## Running

To run out of cluster, make sure the `KUBECONFIG` environment variable is set and then:

```bash
$ monitor run --config /some/config.yaml

# verbose logging
$ monitor run --config /some/config.yaml --alsologtostderr --v 2
```

Here's an example config file:

```yaml
listenAddress: "127.0.0.1:8080"
runInterval: 1m
availabilityTimeout: 5m
template:
  namespace: openshift
  name: django-psql-persistent
  availabilityRoute: django-psql-persistent
  parameters: # Empty, use template defaults
```

## Building

To build the binary, run

```
$ make
```

To build the RPM and images with Docker, run

```
$ OS_BUILD_ENV_PRESERVE=_output/local/bin hack/env make build-images
```

Updating Go Tooling
-------------------

See https://github.com/openshift/release/tree/master/tools/hack/golang for
instructions on how to update the Go tooling used by this project.
