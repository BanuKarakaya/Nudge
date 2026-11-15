#!/usr/bin/env python3
"""Exercise configuration generation in temporary directories, never the real .env."""
import base64
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class InstallationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nudgy-install-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        shutil.copy2(ROOT / "scripts/setup-env.py", self.root / "scripts/setup-env.py")
        shutil.copy2(ROOT / ".env.example", self.root / ".env.example")

    def setup_env(self):
        return subprocess.run([sys.executable, str(self.root / "scripts/setup-env.py")],
                              cwd=self.root, capture_output=True, text=True)

    def test_private_independent_keys_and_no_live_credentials(self):
        self.assertEqual(self.setup_env().returncode, 0)
        target = self.root / ".env"
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        values = dict(line.split("=", 1) for line in target.read_text().splitlines()
                      if line and not line.startswith("#"))
        names = ["NUDGE_LINK_SIGNING_KEY", "FEEDBIN_ENCRYPTION_KEY", "NUDGY_AI_ENCRYPTION_KEY",
                 "NUDGY_CODING_ENCRYPTION_KEY", "NUDGY_TRACKER_ENCRYPTION_KEY"]
        self.assertEqual(len({values[k] for k in names}), 5)
        for name in names:
            self.assertEqual(len(base64.b64decode(values[name], validate=True)), 32)
        self.assertEqual(values["SLACK_CLIENT_SECRET"], "")
        self.assertEqual(values["NUDGE_REMINDER_USER_ID"], "0")

    def test_second_run_preserves_every_byte(self):
        self.assertEqual(self.setup_env().returncode, 0)
        original = (self.root / ".env").read_bytes()
        self.assertNotEqual(self.setup_env().returncode, 0)
        self.assertEqual((self.root / ".env").read_bytes(), original)

    def test_existing_symlink_is_not_followed(self):
        existing = self.root / "existing-secret"
        existing.write_text("synthetic-do-not-change")
        (self.root / ".env").symlink_to(existing)
        self.assertNotEqual(self.setup_env().returncode, 0)
        self.assertEqual(existing.read_text(), "synthetic-do-not-change")

    @unittest.skipUnless(os.environ.get("NUDGY_TEST_COMPOSE") == "1", "enable Docker Compose configuration check")
    def test_host_ports_can_change_without_changing_internal_database(self):
        self.assertEqual(self.setup_env().returncode, 0)
        shutil.copy2(ROOT / "docker-compose.yml", self.root / "docker-compose.yml")
        with (self.root / ".env").open("a") as env:
            env.write("NUDGY_HTTP_PORT=18083\nNUDGY_POSTGRES_PORT=55434\n")
        result = subprocess.run(["docker", "compose", "--profile", "app", "config", "--format=json"],
                                cwd=self.root, capture_output=True, text=True, check=True)
        services = json.loads(result.stdout)["services"]
        self.assertEqual(services["app"]["ports"][0]["published"], "18083")
        self.assertEqual(services["postgres"]["ports"][0]["published"], "55434")
        self.assertEqual(services["app"]["ports"][0]["target"], 8080)
        self.assertIn("@postgres:5432/", services["app"]["environment"]["DATABASE_URL"])

    @unittest.skipUnless(os.environ.get("NUDGY_TEST_COMPOSE") == "1", "enable Docker Compose configuration check")
    def test_external_database_is_preserved_without_local_postgres(self):
        self.assertEqual(self.setup_env().returncode, 0)
        compose = "docker-compose.external.yml"
        shutil.copy2(ROOT / compose, self.root / compose)
        env = self.root / ".env"
        database = "postgres://audit:synthetic@company-db.example:5432/nudgy?sslmode=require"
        env.write_text("\n".join("DATABASE_URL="+database if line.startswith("DATABASE_URL=") else line
                                for line in env.read_text().splitlines())+"\n")
        result = subprocess.run(["docker", "compose", "-f", compose, "config", "--format=json"],
                                cwd=self.root, capture_output=True, text=True, check=True)
        services = json.loads(result.stdout)["services"]
        self.assertEqual(set(services), {"app"})
        self.assertEqual(services["app"]["environment"]["DATABASE_URL"], database)
        self.assertNotIn("depends_on", services["app"])
        self.assertEqual(services["app"]["ports"][0]["host_ip"], "127.0.0.1")


if __name__ == "__main__":
    unittest.main(verbosity=2)
