import base64
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("rebuild", Path(__file__).with_name("rebuild-platform-runtime.py"))
rebuild = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rebuild)


class RuntimeRebuildBoundaryTests(unittest.TestCase):
    def test_cached_metadata_must_match_the_tracked_go_mod_checksum(self):
        original = b"module example.com/fixture\n\ngo 1.27.1\n"
        expected = "h1:" + base64.b64encode(hashlib.sha256(
            (hashlib.sha256(original).hexdigest() + "  go.mod\n").encode()).digest()).decode()
        rebuild.verify_go_mod(original, expected)
        with self.assertRaises(ValueError):
            rebuild.verify_go_mod(original + b"// changed\n", expected)

    def test_archive_writes_cannot_escape_the_build_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ("../escape", "/absolute"):
                with self.assertRaises(ValueError):
                    rebuild.write_file(root, name, b"source")
            rebuild.write_file(root, "cache/!upper/@v/v1.0.0.mod", b"metadata")
            self.assertEqual((root / "cache/!upper/@v/v1.0.0.mod").read_bytes(), b"metadata")
        self.assertEqual(rebuild.escaped("github.com/RaveNoX/module"), "github.com/!rave!no!x/module")


if __name__ == "__main__":
    unittest.main()
