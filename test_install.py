"""python3 -m unittest test_install.py — runs the installer against a temp HOME and PET_HOME."""
import os, sys, json, shutil, subprocess, tempfile, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
INSTALL = os.path.join(HERE, "install.py")
STATUSLINE = os.path.join(HERE, "pet_statusline.py")


class Installer(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.pet_home = tempfile.mkdtemp()
        self.settings = os.path.join(self.home, ".claude", "settings.json")
        self.env = dict(os.environ, HOME=self.home, PET_HOME=self.pet_home,
                        PATH=f"{self.home}/.local/bin:/usr/bin:/bin")

    def tearDown(self):
        shutil.rmtree(self.home, ignore_errors=True)
        shutil.rmtree(self.pet_home, ignore_errors=True)

    def run_install(self, *args, stdin=""):
        return subprocess.run([sys.executable, INSTALL, *args], input=stdin, text=True,
                              capture_output=True, env=self.env)

    def write_settings(self, s):
        os.makedirs(os.path.dirname(self.settings), exist_ok=True)
        with open(self.settings, "w") as f:
            json.dump(s, f)

    def read_settings(self):
        with open(self.settings) as f:
            return json.load(f)

    def our_hooks(self, s, ev):
        return [h for m in s.get("hooks", {}).get(ev, []) for h in m["hooks"] if "hook.py" in h["command"]]

    def test_express_fresh(self):
        r = self.run_install("--express")
        self.assertEqual(r.returncode, 0, r.stderr)
        s = self.read_settings()
        self.assertIn("pet_statusline.py", s["statusLine"]["command"])
        self.assertNotIn("--wrap", s["statusLine"]["command"])
        self.assertEqual(s["statusLine"]["refreshInterval"], 5000)
        for ev in ("PreCompact", "SessionEnd"):
            self.assertEqual(len(self.our_hooks(s, ev)), 1)
        self.assertTrue(os.path.islink(os.path.join(self.home, ".local", "bin", "pet")))
        self.assertIn("Change it later", r.stdout)

    def test_express_wraps_existing_and_keeps_everything_else(self):
        orig = {"model": "opus", "statusLine": {"type": "command", "command": "bash ~/my status.sh", "padding": 1},
                "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
                          "SessionEnd": [{"hooks": [{"type": "command", "command": "echo bye"}]}]}}
        self.write_settings(orig)
        self.assertEqual(self.run_install("--express").returncode, 0)
        s = self.read_settings()
        self.assertIn("--wrap", s["statusLine"]["command"])
        self.assertEqual(s["statusLine"]["padding"], 1)
        self.assertEqual(s["model"], "opus")
        self.assertEqual(s["hooks"]["Stop"], orig["hooks"]["Stop"])
        self.assertEqual(s["hooks"]["SessionEnd"][0], orig["hooks"]["SessionEnd"][0])
        backups = [n for n in os.listdir(os.path.dirname(self.settings)) if ".bak-claude-pet-" in n]
        self.assertEqual(len(backups), 1)

    def test_rerun_is_idempotent(self):
        self.run_install("--express")
        first = self.read_settings()
        r = self.run_install("--express")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.read_settings(), first)
        self.assertIn("already", r.stdout)

    def test_uninstall_restores_exactly(self):
        orig = {"statusLine": {"type": "command", "command": "bash ~/my status.sh"},
                "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]}}
        self.write_settings(orig)
        self.run_install("--express")
        r = self.run_install("--uninstall")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.read_settings(), orig)
        self.assertFalse(os.path.lexists(os.path.join(self.home, ".local", "bin", "pet")))

    def test_uninstall_from_fresh_removes_statusline(self):
        self.run_install("--express")
        self.run_install("--uninstall")
        self.assertEqual(self.read_settings(), {})

    def test_uninstall_without_record_still_unwraps(self):
        self.write_settings({"statusLine": {"type": "command", "command": "node ~/sl.js --x 'y z'"}})
        self.run_install("--express")
        os.remove(os.path.join(self.pet_home, "install.json"))
        self.run_install("--uninstall")
        self.assertEqual(self.read_settings()["statusLine"]["command"], "node ~/sl.js --x 'y z'")

    def test_hand_integrated_statusline_is_left_alone(self):
        script = os.path.join(self.home, "sl.py")
        with open(script, "w") as f:
            f.write("import petlib\n")
        self.write_settings({"statusLine": {"type": "command", "command": f"python3 {script}"}})
        r = self.run_install("--express")
        self.assertEqual(self.read_settings()["statusLine"]["command"], f"python3 {script}")
        self.assertIn("already shows the pet", r.stdout)

    def test_bad_settings_are_not_touched(self):
        os.makedirs(os.path.dirname(self.settings))
        with open(self.settings, "w") as f:
            f.write("{ not json")
        r = self.run_install("--express")
        self.assertNotEqual(r.returncode, 0)
        with open(self.settings) as f:
            self.assertEqual(f.read(), "{ not json")

    def test_custom_flags(self):
        r = self.run_install("--hours", "8-16", "--statusline", "skip", "--no-hooks", "--no-bin", "--name", "Pip")
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(os.path.join(self.pet_home, "config.json")) as f:
            self.assertEqual(json.load(f), {"work_start": 8, "work_end": 16})
        with open(os.path.join(self.pet_home, "state.json")) as f:
            self.assertEqual(json.load(f)["name"], "Pip")
        self.assertFalse(os.path.exists(self.settings))
        self.assertFalse(os.path.lexists(os.path.join(self.home, ".local", "bin", "pet")))

    def test_custom_interactive_defaults(self):
        # not a tty, so prompts fall back to defaults; --custom must still work
        r = self.run_install("--custom")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("pet_statusline.py", self.read_settings()["statusLine"]["command"])

    def test_no_flags_non_interactive_shows_help(self):
        r = self.run_install()
        self.assertEqual(r.returncode, 2)
        self.assertIn("--express", r.stdout)
        self.assertFalse(os.path.exists(self.settings))

    def test_dry_run_writes_nothing(self):
        r = self.run_install("--express", "--dry-run")
        self.assertIn("Would do", r.stdout)
        self.assertFalse(os.path.exists(self.settings))
        self.assertEqual(os.listdir(self.pet_home), [])


class Wrap(unittest.TestCase):
    def setUp(self):
        self.pet_home = tempfile.mkdtemp()
        self.env = dict(os.environ, PET_HOME=self.pet_home)

    def tearDown(self):
        shutil.rmtree(self.pet_home, ignore_errors=True)

    def sl(self, *args, stdin='{"session_id":"s","model":{"display_name":"Opus"}}'):
        return subprocess.run([sys.executable, STATUSLINE, *args], input=stdin, text=True,
                              capture_output=True, env=self.env)

    def test_wrap_prefixes_and_passes_stdin(self):
        r = self.sl("--wrap", "python3 -c 'import sys,json;print(json.load(sys.stdin)[\"model\"][\"display_name\"])'")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.endswith(" Opus"), r.stdout)
        self.assertIn("(", r.stdout.split(" Opus")[0])

    def test_wrap_survives_broken_command(self):
        r = self.sl("--wrap", "exit 3")
        self.assertEqual(r.returncode, 0)
        self.assertIn("(", r.stdout)
        r = self.sl("--wrap")
        self.assertEqual(r.returncode, 0)

    def test_wrap_multiline_prefixes_first_line(self):
        r = self.sl("--wrap", "printf 'one\\ntwo\\n'")
        first, second = r.stdout.split("\n")
        self.assertTrue(first.endswith(" one"))
        self.assertEqual(second, "two")


if __name__ == "__main__":
    unittest.main()
