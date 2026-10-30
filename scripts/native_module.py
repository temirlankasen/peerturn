"""Exact-version, static native module validation; never a live capability proof."""
import json
from pathlib import Path
import re
import runpy
import shutil
import sys
import tempfile


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('native_contract_changed')
        result[key] = value
    return result


def check_report(report):
    if (not isinstance(report, dict) or report.get('success') is not True or
            not isinstance(report.get('manifest'), dict) or report['manifest'].get('errors') != []):
        raise ValueError('native_contract_changed')
    contents = report.get('contents')
    if not isinstance(contents, list) or not contents:
        raise ValueError('native_contract_changed')
    hooks = set()
    for item in contents:
        if not isinstance(item, dict) or item.get('errors') != []:
            raise ValueError('native_contract_changed')
        if item.get('type') != 'hooks':
            continue
        gates = item.get('gatingHooks')
        if not isinstance(gates, list):
            raise ValueError('native_contract_changed')
        for gate in gates:
            if not isinstance(gate, dict):
                raise ValueError('native_contract_changed')
            if gate.get('module') == './register.js' and isinstance(gate.get('hook'), str):
                hooks.add(gate['hook'])
    if not {'prompt.submit', 'session.receive'} <= hooks:
        raise ValueError('native_contract_changed')


def validate(binary, version, plugin, command):
    if not isinstance(version, str) or not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise ValueError('invalid_version')
    if not Path(binary).is_absolute() or not Path(plugin).is_absolute():
        raise ValueError('invalid_input')
    if command([binary, '--version'], timeout=15).decode().strip() != version + ' (Claude Code)':
        raise ValueError('version_mismatch')
    raw = command([binary, 'plugin', 'validate', str(plugin), '--json'], timeout=90)
    check_report(json.loads(raw, object_pairs_hook=unique_fields))


def main():
    if len(sys.argv) != 3 or not sys.argv[2]:
        raise ValueError('Set CLAUDE_VERSION to the exact runtime under test')
    root = Path(__file__).resolve().parent.parent
    binary = shutil.which(sys.argv[1])
    if binary is None:
        raise ValueError('missing_binary')
    binary = str(Path(binary).resolve(strict=True))
    core = runpy.run_path(str(root / 'research/lab/qualify/core.py'))
    with tempfile.TemporaryDirectory(prefix='pt-native-', dir='/tmp') as temporary:
        cwd = Path(temporary).resolve()
        env = core['child_environment'](cwd / 'profile')
        validate(binary, sys.argv[2], str(root / 'adapters/claude'),
                 lambda argv, timeout: core['command'](argv, env, cwd, timeout=timeout))
    print(json.dumps({'runtime': 'claude', 'version': sys.argv[2], 'claude_module': 'passed',
                      'api_existence': 'not_tested', 'live_semantics': 'not_tested'}))
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        print('Native module check failed: require the exact version and an error-free scanned module report.',
              file=sys.stderr)
        raise SystemExit(1) from None
