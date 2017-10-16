# ark_openshift

An Ansible role to deploy [Ark](https://github.com/heptio/ark) on OpenShift for
managing cluster backup and restore. This role currently assumes you are
deploying to a cluster on AWS.

## Dependencies

## Role Variables

### Required

* arko_aws_secret_access_key
* arko_aws_access_key_id
* arko_aws_region
* arko_aws_availability_zone
* arko_aws_bucket - A S3 bucket for this specific cluster. (NOTE: do not have multiple clusters using the same S3 bucket) Must be created beforehand.

