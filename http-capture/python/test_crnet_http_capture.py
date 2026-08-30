import json
import unittest

import crnet_http_capture as cap


class RedactTests(unittest.TestCase):
    def test_json_password(self):
        out = cap.redact_body('{"password":"hunter2","user":"ada"}')
        self.assertNotIn("hunter2", out)
        self.assertIn("[redacted]", out)
        self.assertIn("ada", out)

    def test_form_token(self):
        out = cap.redact_body("user=ada&token=abc&ok=1")
        self.assertEqual(out, "user=ada&token=[redacted]&ok=1")

    def test_truncate(self):
        out = cap.redact_body("x" * 5000)
        self.assertTrue(out.endswith("…[truncated]"))
        self.assertLess(len(out), 5000)

    def test_nested_json(self):
        out = cap.redact_body(json.dumps({"user": {"access_token": "secret"}}))
        self.assertNotIn("secret", out)


if __name__ == "__main__":
    unittest.main()
