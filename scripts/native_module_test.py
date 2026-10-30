"""Strict native module report contracts, without agent or provider calls."""
import json
from pathlib import Path
import runpy
import unittest


HERE = Path(__file__).resolve().parent
MODULE = runpy.run_path(str(HERE / 'native_module.py')) if (HERE / 'native_module.py').exists() else {}


def report():
    return {'success': True, 'manifest': {'errors': []}, 'contents': [
        {'type': 'hooks', 'errors': [], 'gatingHooks': [
            {'module': './register.js', 'hook': name} for name in ('prompt.submit', 'session.receive')]}]}


class NativeModuleTests(unittest.TestCase):
    def test_exact_binary_version_and_scanned_hooks_required(self):
        calls = []

        def command(argv, timeout):
            calls.append((argv, timeout))
            return b'2.1.308 (Claude Code)\n' if '--version' in argv else json.dumps(report()).encode()

        MODULE['validate']('/native/claude', '2.1.308', '/source/plugin', command)
        self.assertEqual(calls, [(['/native/claude', '--version'], 15),
                                 (['/native/claude', 'plugin', 'validate', '/source/plugin', '--json'], 90)])

    def test_exit_zero_does_not_authorize_false_or_unscanned_reports(self):
        missing = report()
        missing['contents'][0]['gatingHooks'].pop()
        for broken in ({'success': True}, dict(report(), success='true'), dict(report(), success=False),
                       dict(report(), contents=[]), dict(report(), contents=[{'type': 'hooks', 'errors': ['source error']}]),
                       dict(report(), manifest={'errors': ['bad manifest']}), missing):
            with self.subTest(report=broken):
                with self.assertRaises(ValueError):
                    MODULE['check_report'](broken)

    def test_wrong_version_stops_before_validation(self):
        calls = []

        def command(argv, timeout):
            calls.append(argv)
            return b'2.1.306 (Claude Code)'

        with self.assertRaisesRegex(ValueError, 'version_mismatch'):
            MODULE['validate']('/native/claude', '2.1.308', '/source/plugin', command)
        self.assertEqual(calls, [['/native/claude', '--version']])

    def test_malformed_and_duplicate_json_never_pass(self):
        for data in (b'{', b'[]', b'{"success":false,"success":true}', b'null'):
            def command(argv, timeout):
                return b'2.1.308 (Claude Code)' if '--version' in argv else data
            with self.subTest(data=data), self.assertRaises(ValueError):
                MODULE['validate']('/native/claude', '2.1.308', '/source/plugin', command)


if __name__ == '__main__':
    unittest.main()
