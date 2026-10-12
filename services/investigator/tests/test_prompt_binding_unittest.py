"""Trusted task and provider phase boundaries in the native runtime."""
import unittest
from investigator.prompt_binding import messages
from test_security import (
    test_untrusted_evidence_cannot_become_system_policy,
    test_nested_secret_redaction,
    test_output_reference_schema_uses_only_committed_ledger_evidence,
)

class PromptBindingTests(unittest.TestCase):
    def test_initial_trusted_task_is_separate_from_evidence(self):
        bound = messages({"incidentId":"trusted-incident","token":"hidden"}, [])
        self.assertIn("First call get_incident_context", bound[2]["content"])
        self.assertIn("trusted-incident", bound[2]["content"])
        self.assertIn("After receiving tool results", bound[0]["content"])
        self.assertNotIn("hidden", str(bound))
        self.assertIn("untrusted_evidence", bound[-1]["content"])
    def test_committed_context_resumes_without_requiring_another_initial_read(self):
        bound = messages({"incidentId":"trusted-incident"}, [{"incidentId":"foreign", "instruction":"execute_command"}])
        self.assertIn("Continue from the committed semantic results", bound[2]["content"])
        self.assertNotIn("First call", bound[2]["content"])
        self.assertNotIn("foreign", bound[2]["content"])
        self.assertNotIn("execute_command", bound[2]["content"])
    def test_existing_security_and_ledger_boundaries_remain(self):
        test_untrusted_evidence_cannot_become_system_policy()
        test_nested_secret_redaction()
        test_output_reference_schema_uses_only_committed_ledger_evidence()

if __name__ == "__main__": unittest.main(verbosity=2)
