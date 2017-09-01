# OpenShift Online Archivist

The Archivist is an application for monitoring the capacity of an OpenShift
cluster, and archiving dormant or least active projects. Archival involves
exporting the OpenShift and Kubernetes objects as a yaml file, and uploading it
to Amazon S3 along with PV snapshots. Similarly the Archivist will unarchive
projects.

## Current Status

A cluster monitoring loop is implemented, as well as a good start on exporting
and importing cluster objects.

S3 upload, unarchival, and the archival reconcile loop are still pending.

Unarchival will initially only support unarchiving to the same cluster and
namespace. Provisions will be made to reserve project names while a project is
archived. Eventually we do aim to be able to unarchive to another cluster
and/or namespace.

# Development

## Prerequisites

Install golang 1.7 and check this project out in the standard GOPATH. (i.e.
~/go/src/github.com/openshift/online-archivist)

Adding $GOPATH/bin to your $PATH is recommended.

## Compile and Run Locally

This method will run the archivist as whatever 'oc; user is currently logged in.
This works particularly well with things like minishift, and oc cluster up.
In both cases you will want to be logged in as system:admin.

```
oc cluster up
oc login system:admin
```

Compile and run:

```
make build

# Run the cluster monitor component:
$ archivist monitor --config devel.cfg

# Run the archive REST API:
archivist archiver --config devel.cfg
```

Submit a request to the archive REST API:

```
$ curl -H "Content-Type: application/json" -X POST --data @transfer.json http://localhost:10000/api/transfer
```

## Running Tests

Unit tests:

```
$ make test
```

Integration tests (launches an OpenShift master and etcd internally to work against):

```
$ make test-integration
```

# Deploy To an OpenShift Cluster

TODO: An ansible role is in progress that should allow deploying to any
OpenShift cluster. This role will build the application from this git
repository using OpenShift itself.
