import unittest
from investigator.failure_diagnostics import failure_frames

class DiagnosticsTests(unittest.TestCase):
    def test_nested_causes_are_visible_without_private_messages(self):
        private = RuntimeError("Bearer secret-password private-url")
        outer = ExceptionGroup("private-response",[ExceptionGroup("private-header",[private])])
        values = failure_frames(outer)
        self.assertIn(("RuntimeError",None),values)
        self.assertNotIn("secret-password",str(values))
        self.assertNotIn("private-response",str(values))
    def test_cycles_are_bounded(self):
        a=RuntimeError("a");b=RuntimeError("b")
        a.__cause__=b;b.__cause__=a
        self.assertEqual(len(failure_frames(a)),2)
