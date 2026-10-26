import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import ci_rootless_cgroup as setup


class DelegationTest(unittest.TestCase):
    relative = "/kubepods/burstable/pod-test/" + "a" * 64

    def run_main(self, relative=None, uid=0, owner=0, mount="cgroup2 /", user=1001, resolved=None):
        relative = self.relative if relative is None else relative
        directory = Path("/sys/fs/cgroup") / relative.lstrip("/")
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.dict(os.environ, {
                "GITLAB_CI": "true", "CI_RUNTIME": "podman",
                "CI_REGISTRY_PASSWORD": "never-print-this",
            }))
            stack.enter_context(mock.patch.object(setup.os, "geteuid", return_value=uid))
            stack.enter_context(mock.patch.object(setup.subprocess, "check_output", return_value=mount))
            stack.enter_context(mock.patch.object(Path, "read_text", return_value="0::" + relative + "\n"))
            stack.enter_context(mock.patch.object(Path, "resolve", return_value=directory if resolved is None else resolved))
            stack.enter_context(mock.patch.object(Path, "stat", return_value=SimpleNamespace(st_uid=owner)))
            stack.enter_context(mock.patch.object(setup.pwd, "getpwnam", return_value=SimpleNamespace(pw_uid=user, pw_gid=user)))
            prepared = stack.enter_context(mock.patch.object(setup, "prepare", return_value=directory / "durpdeploy-rootless"))
            output = stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            setup.main()
            return prepared, output.getvalue()

    def test_valid_build_leaf_only_prints_parent(self):
        prepared, output = self.run_main()
        directory = Path("/sys/fs/cgroup") / self.relative.lstrip("/")
        prepared.assert_called_once_with(directory, 1001, 1001)
        self.assertEqual(output, self.relative + "/durpdeploy-rootless\n")
        self.assertNotIn("never-print-this", output)

    def test_reject_unsafe_membership_before_delegation(self):
        for relative in [
            "/", "/kubepods", "/kubepods/burstable/pod-test",
            "/system.slice/" + "a" * 64,
            "/kubepods/../" + "a" * 64,
            "/kubepods/./" + "a" * 64,
            "/kubepods/burstable/pod-test/" + "a" * 63,
            "/kubepods/é/pod-test/" + "a" * 64,
            "/kubepods/burstable/pod-é/" + "a" * 64,
            "/kubepods/١/pod-test/" + "a" * 64,
        ]:
            with self.subTest(relative=relative), mock.patch.object(setup, "prepare") as prepared:
                with self.assertRaisesRegex(RuntimeError, "build-container leaf"):
                    self.run_main(relative=relative)
                prepared.assert_not_called()

    def test_reject_non_root_and_unsafe_mount_or_owner(self):
        for options in [{"uid": 1001}, {"owner": 1001}, {"mount": "cgroup /"},
                        {"mount": "cgroup2 /another-job"}, {"user": 0},
                        {"resolved": Path("/sys/fs/cgroup/another-job")}]:
            with self.subTest(options=options), self.assertRaises(RuntimeError):
                self.run_main(**options)

    def test_reject_non_ci_and_wrong_runtime_before_filesystem_access(self):
        for environment in [{"GITLAB_CI": "false", "CI_RUNTIME": "podman"},
                            {"GITLAB_CI": "true", "CI_RUNTIME": "docker"}]:
            with mock.patch.dict(os.environ, environment), \
                 mock.patch.object(setup.os, "geteuid", return_value=0), \
                 mock.patch.object(setup.subprocess, "check_output") as command:
                with self.assertRaises(RuntimeError):
                    setup.main()
                command.assert_not_called()

    def test_prepare_refuses_missing_controllers_threading_and_reused_paths(self):
        for controllers, kind, reused in [
            ("cpu pids", "domain", False),
            ("cpu memory pids", "threaded", False),
            ("cpu memory pids", "domain", True),
        ]:
            with self.subTest(controllers=controllers, kind=kind, reused=reused), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                (directory / "cgroup.controllers").write_text(controllers)
                (directory / "cgroup.type").write_text(kind)
                if reused:
                    (directory / "durpdeploy-rootless").mkdir()
                with self.assertRaises(RuntimeError):
                    setup.prepare(directory, 1001, 1001)
                self.assertFalse((directory / "durpdeploy-bootstrap").exists())

    def test_process_exit_during_migration_rechecks_membership(self):
        directory, bootstrap = mock.MagicMock(), mock.MagicMock()
        directory.__truediv__.return_value.read_text.side_effect = ["123\n", ""]
        bootstrap.__truediv__.return_value.write_text.side_effect = ProcessLookupError()
        setup.move_build_processes(directory, bootstrap)
        bootstrap.__truediv__.return_value.write_text.assert_called_once_with("123\n")

    def test_process_migration_must_finish_within_its_bound(self):
        directory, bootstrap = mock.MagicMock(), mock.MagicMock()
        directory.__truediv__.return_value.read_text.return_value = "123\n"
        with self.assertRaisesRegex(RuntimeError, "did not become empty"):
            setup.move_build_processes(directory, bootstrap)


if __name__ == "__main__":
    unittest.main()
