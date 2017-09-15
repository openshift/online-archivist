# oso_archivist

An Ansible role to deploy the OpenShift Online Archivist application.

The Archivist is responsible for monitoring last activity time for each
project, monitoring cluster capacity, and archiving the least active projects
when certain thresholds are met.

Today this role uses an OpenShift template which builds an Archivist image from
the upstream git repository. In the future there may be an optional deployment
method that uses a published image and tag in a registry.

This role expects to run with system:admin credentials as it creates roles with
certain elevated privileges to manage the cluster. Typically this is
accomplished by running as root on a master host.

*WARNING*: When run, this role will always update the template and reprocess it, leading
to recreating the build and deployment configs, and thus restarting the running
application. *NOT FOR USE IN ONGOING CONFIG MANAGEMENT LOOPS*

## Dependencies

- lib_openshift role from openshift-ansible must be loaded in a playbook prior to running this role.

## Role Variables

### Required

* osoa_min_inactive_days - Number of days after which a project will be *eligible* for archival, if we need capacity.
* osoa_max_inactive_days - Number of days after which a project *will* be archived, regardless if we need capacity or not.
* osoa_namespace_high_watermark - Number of namespaces/projects that will trigger archival to get to low watermark.
* osoa_namespace_low_watermark - Number of namespaces/projects we will archive until we reach.

### Optional

See defaults/main.yml for default values.

* osoa_appname - Name for the application used in all OpenShift objects created. Allows multiple deployments of the archivist in one namespace.
* osoa_namespace - OpenShift namespace to deploy to.
* osoa_git_repo - The git repository to deploy from.
* osoa_git_ref - A git branch, tag, or commit to deploy from the repo.
* osoa_template_path - Location on master where we will store the template and oc process it from.
* osoa_uninstall - Set to 'true' to uninstall before running role. (can also include_role with tasks_from to just uninstall without re-installing from a playbook)
