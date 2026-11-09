import unittest
import json
import tempfile
from pathlib import Path
from unittest.mock import patch

import release


class WebRuntimeTests(unittest.TestCase):
    def test_native_config_survives_build_preparation_and_is_verified(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "web").mkdir()
            (root / "web/index.html").write_text("built app")
            with self.assertRaisesRegex(ValueError, "runtime-config.json required"):
                release.verify_web_runtime(root, {"humanAuthMode": "native"})
            digest = release.prepare_web_runtime(root, {"humanAuthMode": "native"})
            self.assertEqual(json.loads((root / "web/runtime-config.json").read_text()), {"auth": {"mode": "native"}})
            self.assertEqual(digest, release.verify_web_runtime(root, {"humanAuthMode": "native"}))
            self.assertEqual(digest, release.prepare_web_runtime(root, {"humanAuthMode": "native"}))

    def test_does_not_change_an_accepted_release_or_infer_auth_mode(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "web").mkdir()
            (root / "web/index.html").write_text("built app")
            (root / "manifest.json").write_text("{}")
            with self.assertRaisesRegex(ValueError, "immutable"):
                release.prepare_web_runtime(root, {"humanAuthMode": "native"})
            self.assertFalse((root / "web/runtime-config.json").exists())
            with self.assertRaisesRegex(ValueError, "humanAuthMode"):
                release.web_runtime_config({})
            with self.assertRaisesRegex(ValueError, "OIDC"):
                release.web_runtime_config({"humanAuthMode": "oidc"})

    def test_rejects_existing_config_from_another_auth_mode(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "web").mkdir()
            path = root / "web/runtime-config.json"
            path.write_text('{"auth":{"mode":"oidc"}}')
            with self.assertRaisesRegex(ValueError, "differs"):
                release.prepare_web_runtime(root, {"humanAuthMode": "native"})
            self.assertEqual(path.read_text(), '{"auth":{"mode":"oidc"}}')


class WorkerServiceTests(unittest.TestCase):
    def test_rejects_loopback_and_mismatched_service_route(self):
        service = {"spec": {"ports": [{"port": 8082, "targetPort": 18282}]}}
        with self.assertRaisesRegex(ValueError, "loopback-only"):
            release.verify_worker_service({"ListenAddress": "127.0.0.1:18282"}, service, {})
        with self.assertRaisesRegex(ValueError, "differs"):
            release.verify_worker_service({"ListenAddress": "0.0.0.0:8082"}, service, {})
        self.assertEqual(release.verify_worker_service({"ListenAddress": "0.0.0.0:18282"}, service, {}), 18282)

    def test_named_port_resolves_to_one_configured_listener(self):
        service = {"spec": {"ports": [{"port": 8082, "targetPort": "rpc"}]}}
        deployment = {"spec": {"template": {"spec": {"containers": [{"ports": [{"name": "rpc", "containerPort": 18282}]}]}}}}
        self.assertEqual(release.verify_worker_service({"ListenAddress": "0.0.0.0:18282"}, service, deployment), 18282)
        with self.assertRaisesRegex(ValueError, "ambiguous or missing"):
            release.verify_worker_service({"ListenAddress": "0.0.0.0:18282"}, service, {"spec": {"template": {"spec": {"containers": []}}}})


class ImagePreflightTests(unittest.TestCase):
    def setUp(self):
        self.images = {name: {"image": "ops.local/" + name + "@sha256:" + digit * 64}
                       for name, digit in [("api", "a"), ("worker", "b"), ("investigator", "c")]}

    def verify(self, targets, ready):
        table = "REF TYPE DIGEST\n" + "\n".join(name + " oci " + digest for name, digest in targets.items())
        with patch.object(release, "command", side_effect=[table.encode(), "\n".join(ready).encode()]):
            return release.verify_runtime_images(self.images)

    def targets(self):
        return {v["image"]: v["image"].split("@")[1] for v in self.images.values()}

    def test_accepts_complete_exact_pins(self):
        targets = self.targets()
        self.assertEqual(self.verify(targets, targets), list(targets))

    def test_cached_tag_does_not_satisfy_digest_reference(self):
        targets = self.targets()
        digest = targets.pop(self.images["investigator"]["image"])
        targets["ops.local/investigator:patch2"] = digest
        with self.assertRaisesRegex(ValueError, "investigator pinned image is not registered"):
            self.verify(targets, targets)

    def test_rejects_wrong_target_digest(self):
        targets = self.targets()
        targets[self.images["investigator"]["image"]] = "sha256:" + "d" * 64
        with self.assertRaisesRegex(ValueError, "exact digest"):
            self.verify(targets, targets)

    def test_rejects_incomplete_or_not_unpacked_image(self):
        targets = self.targets()
        with self.assertRaisesRegex(ValueError, "incomplete or not unpacked"):
            self.verify(targets, list(targets)[:2])

    def test_rejects_mutable_image_tag(self):
        targets = self.targets()
        self.images["investigator"]["image"] = "ops.local/investigator:patch2"
        with self.assertRaisesRegex(ValueError, "requires a pinned image digest"):
            self.verify(targets, targets)

    def test_rejects_missing_component(self):
        targets = self.targets()
        del self.images["investigator"]
        with self.assertRaisesRegex(ValueError, "investigator requires"):
            self.verify(targets, targets)
