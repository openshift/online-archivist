'''
Module to check an OpenShift Git BuildConfig and determine if the latest build matches the requested git ref. If not, an oc start-build will be run.
'''

import subprocess
import yaml

from ansible.module_utils.basic import AnsibleModule

def start_build(module):
    run_command(['oc', 'start-build', '-n', module.params['namespace'], module.params['buildconfig']])


def main():
    module = AnsibleModule(argument_spec=dict(
        namespace=dict(required=True, type='str'),
        buildconfig=dict(required=True, type='str'),
        git_ref=dict(required=True, type='str'),
    ))

    output_lines = []
    changed = False

    bc_out = run_command(['oc', 'get', '-n', module.params['namespace'], 'BuildConfig',
        module.params['buildconfig'], '-o', 'yaml'])
    buildconfig = yaml.safe_load(bc_out)

    # Handle buildconfig not from git source
    if 'source' not in buildconfig['spec'] or \
            buildconfig['spec']['source']['type'] != 'Git':
            module.fail_json(msg="BuildConfig '%s' does not use Git source" % module.params['buildconfig'],
                    output=output_lines)

    if 'status' not in buildconfig or 'lastVersion' not in buildconfig['status']:
        output_lines.append("No status or lastVersion on BuildConfig, starting build.")
        changed = True
        output_lines.append(start_build(module))
    else:
        expected_latest_build = "%s-%s" % (module.params['buildconfig'], buildconfig['status']['lastVersion'])
        output_lines.append("Expected latest build: %s" % expected_latest_build)
        build_out = run_command(['oc', 'get', '-n', module.params['namespace'], 'Build',
            expected_latest_build, '-o', 'yaml'])
        latest_build = yaml.safe_load(build_out)

        if latest_build['status']['phase'] == 'Failed':
            output_lines.append("Last build failed, starting another.")
            changed = True
            output_lines.append(start_build(module))
        elif latest_build['status']['phase'] == 'Running':
            output_lines.append("Build already running, skipping.")
        else:
            if module.params['git_ref'] != latest_build['spec']['revision']['git']['commit']:
                output_lines.append("Git refs do not match: %s != %s" % (module.params['git_ref'],
                    latest_build['spec']['revision']['git']['commit']))
                changed = True
                output_lines.append(start_build(module))
            else:
                output_lines.append("Git ref matches.")

    module.exit_json(changed=changed, output=output_lines)

def run_command(cmds):
    output = subprocess.check_output(cmds)
    return output

if __name__ == '__main__':
    main()

