# Archivist Ansible Roles / Playbooks

This directory contains playbooks and Ansible roles to deploy both Ark and OpenShift.

## Requirements

You will need AWS credentials with permission to create S3 buckets, write to
S3, and create PV snapshots.

Store your AWS credentials as a separate profile in ~/.aws/credentials. (suggest "ark" for this example)

You will need to manually create an S3 bucket beforehand:

```
$ aws s3api create-bucket --bucket dgoodwin-ark-testing --region us-east-1 --profile ark
```


Create vars/zz_custom_vars.yml:

```
arko_aws_secret_access_key: ""
arko_aws_access_key_id: ""
arko_aws_region: us-east-1
arko_aws_availability_zone: us-east-1d
arko_aws_bucket: dgoodwin-ark-testing
```

The zz prefix should ensure it's always loaded last and thus overrides all
other vars. This file should *never* be comitted to git and is added to
.gitignore.

You're now ready to deploy Ark and the Archivist, assuming you've already done
a standard Online team devenv deploy, point this command to your inventory
(likely generated with aws-launcher):

```
$ gogitit sync && ansible-playbook -i ~/src/online/ansible/hosts archivist.yml
```

NOTE: gogitit should be installed if you've used the devenv-launch playbook.

NOTE: these playbooks and roles will be vendored in for integration into the
standard devenv deploy.
