import unittest
from migrate import plan_rules


class AffinityMigrationTests(unittest.TestCase):
    def test_legacy_flag_requires_review_and_does_not_silently_enable_strict(self):
        old = [{"name": "codex cli trace", "skip_retry_on_failure": True,
                "key_sources": [{"type": "gjson", "path": "prompt_cache_key"}]}]
        pending, _, missing = plan_rules(old, {})
        self.assertEqual(missing, ["codex cli trace"])
        self.assertNotIn("session_mode", pending[0])
        updated, _, missing = plan_rules(old, {"codex cli trace": "prefer"})
        self.assertFalse(missing)
        self.assertEqual(updated[0]["session_mode"], "prefer")
        self.assertFalse(updated[0]["skip_retry_on_failure"])
        self.assertEqual(updated[0]["key_sources"], old[0]["key_sources"])
        self.assertTrue(old[0]["skip_retry_on_failure"])
        self.assertEqual(plan_rules(updated, {})[0], updated)

    def test_explicit_strict_and_custom_payload_are_preserved(self):
        rules = [{"name": "stateful", "session_mode": "strict", "skip_retry_on_failure": True,
                  "param_override_template": {"operations": [{"value": "not-in-report"}]}}]
        result, report, pending = plan_rules(rules, {})
        self.assertEqual(result, rules)
        self.assertEqual(pending, [])
        self.assertNotIn("not-in-report", str(report))

    def test_ambiguous_and_unknown_decisions_are_rejected(self):
        for rules, decisions in [([{"name": "a"}, {"name": "a"}], {}),
                                  ([{"name": "a"}], {"b": "prefer"}),
                                  ([{"name": "a"}], {"a": "guess"})]:
            with self.assertRaises(ValueError):
                plan_rules(rules, decisions)


if __name__ == "__main__":
    unittest.main()
