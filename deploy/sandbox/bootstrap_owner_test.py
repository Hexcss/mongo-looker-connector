import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import bootstrap_owner


class OwnerBootstrapTest(unittest.TestCase):
    def test_credential_file_and_idempotence(self):
        def response(status, data):
            return Mock(status=status, read=Mock(return_value=json.dumps(data).encode()))

        password = "x" * 43
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(bootstrap_owner, "service_url", return_value="https://fixture.run.app"), \
                patch.object(bootstrap_owner, "admin_tokens", return_value=("iam", "proof")), \
                patch.object(bootstrap_owner.http.client, "HTTPSConnection") as https:
            conn = https.return_value
            conn.getresponse.side_effect = [response(200, {"users": []}),
                                            response(201, {"temporaryPassword": password}),
                                            response(200, {"users": [{"primary": True}]})]
            dest = Path(directory) / "owner-password"
            args = ("example-project", "operator", "region", "owner@example.invalid", dest)
            self.assertTrue(bootstrap_owner.bootstrap(*args))
            self.assertEqual(os.stat(dest).st_mode & 0o777, 0o600)
            self.assertEqual(dest.read_text(), password + "\n")
            self.assertFalse(bootstrap_owner.bootstrap(*args))
            self.assertEqual(dest.read_text(), password + "\n")
            self.assertEqual(sum(c.args[0] == "POST" for c in conn.request.call_args_list), 1)
            conn.getresponse.side_effect = [response(200, {"users": []})]
            with self.assertRaises(FileExistsError):
                bootstrap_owner.bootstrap(*args)
            self.assertEqual(sum(c.args[0] == "POST" for c in conn.request.call_args_list), 1)
