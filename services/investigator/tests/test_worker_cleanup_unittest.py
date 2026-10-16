"""Exercise the production cleanup function without loading model/TLS dependencies."""
import ast
import io
import sys
import threading
import unittest
from pathlib import Path
from types import SimpleNamespace

source = Path(__file__).parents[1] / "src/investigator/worker.py"
function = next(node for node in ast.parse(source.read_text()).body
                if isinstance(node, ast.FunctionDef) and node.name == "_release_job_slot")
namespace = {"sys": SimpleNamespace(stderr=io.StringIO())}
exec(compile(ast.Module(body=[function], type_ignores=[]), str(source), "exec"), namespace)
release = namespace["_release_job_slot"]


class Cleanup(unittest.TestCase):
    def test_both_close_failures_still_return_slot(self):
        closed = []
        class Broken:
            def close(self):
                closed.append(True)
                raise RuntimeError("private detail must not be logged")
        gate = threading.BoundedSemaphore(1)
        gate.acquire()
        release(Broken(), Broken(), gate)
        self.assertTrue(gate.acquire(blocking=False))
        self.assertEqual(len(closed), 2)
        self.assertNotIn("private detail", namespace["sys"].stderr.getvalue())

    def test_interrupt_during_close_still_returns_slot(self):
        class Interrupted:
            def close(self): raise KeyboardInterrupt()
        gate = threading.BoundedSemaphore(1)
        gate.acquire()
        with self.assertRaises(KeyboardInterrupt):
            release(Interrupted(), None, gate)
        self.assertTrue(gate.acquire(blocking=False))
