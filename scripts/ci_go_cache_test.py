#!/usr/bin/env python3
"""Exercise cache handoff without compiling application tests."""

import importlib.util
import json
import os
from pathlib import Path
import tempfile
import sys
import stat
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("ci_go_cache", Path(__file__).with_name("ci_go_cache.py"))
cache = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cache)

PAYLOAD = ".cache/ci-go"
CACHED_EXECUTABLE = b"cached executable"
TOOL_SUFFIX = "/templ"


class CacheTest(unittest.TestCase):
    def test_cold_export_and_warm_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            local = project / "helper"
            local.mkdir()
            original = "aa/" + "a" * 64 + "-d"
            added = "bb/" + "b" * 64 + "-d"
            (local / "aa").mkdir()
            (local / original).write_bytes(b"helper")
            with patch.dict(os.environ, CI_PROJECT_DIR=directory, GOCACHE=str(local)):
                cache.transfer("restore")
                (local / "bb").mkdir()
                (local / added).write_bytes(b"compiled")
                cache.transfer("export")
                payload = project / PAYLOAD
                self.assertFalse((payload / original).exists())
                self.assertEqual((payload / added).read_bytes(), b"compiled")
                (local / added).unlink()
                cache.transfer("restore")
                self.assertEqual((local / added).read_bytes(), b"compiled")

    def test_cached_tool_executable_round_trip_and_size_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            producer = project / "producer"
            producer.mkdir()
            relative = "cc/" + "c" * 64 + "-d/templ"
            executable = producer / relative
            with patch.dict(os.environ, CI_PROJECT_DIR=directory, GOCACHE=str(producer)):
                cache.transfer("restore")
                executable.parent.mkdir(parents=True)
                executable.write_bytes(CACHED_EXECUTABLE)
                executable.chmod(0o755)
                cache.transfer("export")
                payload = project / PAYLOAD
                self.assertEqual((payload / relative).read_bytes(), CACHED_EXECUTABLE)
                consumer = project / "consumer"
                with patch.dict(os.environ, GOCACHE=str(consumer)):
                    cache.transfer("restore")
                    restored = consumer / relative
                    self.assertEqual(restored.read_bytes(), CACHED_EXECUTABLE)
                    self.assertEqual(stat.S_IMODE(restored.stat().st_mode), 0o755)
                    with patch.object(cache, "LIMIT", 1):
                        cache.transfer("export")
                        self.assertEqual(cache.objects(payload), {})

    def test_cached_tool_directory_rejects_links_and_nested_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cached = root / "dd" / ("d" * 64 + "-d")
            cached.mkdir(parents=True)
            child = cached / "tool"
            child.symlink_to(root / "outside")
            with self.assertRaisesRegex(ValueError, "nonregular executable"):
                cache.objects(root)
            child.unlink()
            child.mkdir()
            with self.assertRaisesRegex(ValueError, "nonregular executable"):
                cache.objects(root)
            child.rmdir()
            (cached / "témpl").write_bytes(b"invalid name")
            with self.assertRaisesRegex(ValueError, "invalid executable directory"):
                cache.objects(root)

    def test_conflicting_payload_preserves_helper_objects(self):
        entry = "ee/" + "e" * 64 + "-d"
        for helper_name, incoming_name in [
            (entry + TOOL_SUFFIX, entry),
            (entry, entry + TOOL_SUFFIX),
            (entry + TOOL_SUFFIX, entry + "/other"),
        ]:
            with (
                self.subTest(helper=helper_name, incoming=incoming_name),
                tempfile.TemporaryDirectory() as directory,
            ):
                project = Path(directory)
                local = project / "helper"
                payload = project / PAYLOAD
                helper = local / helper_name
                incoming = payload / incoming_name
                helper.parent.mkdir(parents=True)
                incoming.parent.mkdir(parents=True)
                helper.write_bytes(b"helper")
                incoming.write_bytes(b"incoming")
                with patch.dict(os.environ, CI_PROJECT_DIR=directory, GOCACHE=str(local)):
                    cache.transfer("restore")
                self.assertEqual(set(cache.objects(local)), {helper_name})
                self.assertEqual(helper.read_bytes(), b"helper")

    def test_symlink_and_oversized_cache_fall_back_to_helper(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            local = project / "helper"
            local.mkdir()
            payload = project / PAYLOAD
            payload.mkdir(parents=True)
            (payload / "aa").symlink_to(local, target_is_directory=True)
            with patch.dict(os.environ, CI_PROJECT_DIR=directory, GOCACHE=str(local)):
                cache.transfer("restore")
                self.assertEqual(list(local.iterdir()), [])
                (payload / "aa").unlink()
                (payload / "aa").mkdir()
                path = "aa/" + "a" * 64 + "-d"
                (payload / path).write_bytes(b"too large")
                with patch.object(cache, "LIMIT", 1):
                    cache.transfer("restore")
                    self.assertFalse((local / path).exists())
                    cache.transfer("export")
                    self.assertEqual(cache.objects(payload), {})
                self.assertEqual(json.loads((project / ".cache/ci-go-baseline.json").read_text()), [])


if __name__ == "__main__":
    unittest.main()
