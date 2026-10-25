"""Source validation regressions; fixtures do not grant platform qualification."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('windows_artifact', Path(__file__).with_name('package-windows-artifact.py'))
windows = importlib.util.module_from_spec(spec)
spec.loader.exec_module(windows)


class WindowsInspectionTests(unittest.TestCase):
    def test_current_bundle_schema_preserves_fixed_source_checks(self):
        for profile in ('spec', 'backend'):
            inspection = dict(schemaVersion=5, profile=profile, templateCommit='a' * 40,
                              templateVersion='4.7.2', sourceState='committed')
            try:
                windows.validate_inspection_source(inspection, profile, 5, 'a' * 40, '4.7.2')
            except ValueError as error:
                self.fail(f'current Bundle5 inspection rejected: {error}')
            for key, wrong in [('schemaVersion', 6), ('profile', 'design'),
                               ('templateCommit', 'b' * 40), ('templateVersion', '4.7.1'),
                               ('sourceState', 'working-tree')]:
                with self.subTest(profile=profile, field=key), self.assertRaises(ValueError):
                    windows.validate_inspection_source(dict(inspection, **{key: wrong}),
                                                       profile, 5, 'a' * 40, '4.7.2')
            for legacy in (2, 3):
                with self.subTest(lock=legacy), self.assertRaises(ValueError):
                    windows.validate_inspection_source(inspection, profile, legacy, 'a' * 40, '4.7.2')
