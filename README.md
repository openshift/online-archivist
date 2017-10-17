# OpenShift Online Archivist

The Archivist is an application for monitoring the capacity of an OpenShift
cluster, and archiving dormant or least active projects. It requires a running
instance of [Heptio Ark](https://github.com/heptio/ark/) which performs the
actual archival/unarchival for a specific namespace. (including all cluster
resources, and PV snapshots) Ansible roles are included for deploying both
applications in tandem.


## Current Status

A cluster monitoring loop is implemented in the Archivist and is successfully
submitting requests to archive dormant projects to Ark.

In future we expect to reserve namespaces for a given user

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

This method will run the archivist as whatever oc user is currently logged in.
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

Ansible is provided to deploy both the Archivist by building from git source,
as well as Ark.

  1. Validate settings in ansible/vars/
  1. Create an Ansible inventory matching the standard [openshift-ansible format](https://github.com/openshift/openshift-ansible/blob/master/inventory/byo/hosts.example).
     (as created for online team devenvs with aws-launcher, etc)
  1. ansible-playbook -i hosts ansible/archivist.yml

# Manual Unarchival

Until we have API calls to unarchive you can manually initiate one:

 1. 'exec' into the running ark pod (in heptio-ark namespace)
 1. Lookup the backup for username you want to unarchive: `/ark backup get -l "openshift.io/requester=username"`
 1. Trigger an Ark restore: `/ark restore create [backupname]`

