"""python3 -m unittest ~/.claude/pet/test_petlib.py — never touches the real pet (PET_HOME override)."""
import os, sys, json, time, tempfile, shutil, subprocess, unittest
import datetime as dt

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import petlib as P


def setUpModule():
    os.environ["TZ"] = "Australia/Melbourne"   # DST starts Sun 4 Oct 2026
    time.tzset()


def ts(y, mo, d, h=0, mi=0):
    return dt.datetime(y, mo, d, h, mi).timestamp()


MON = (2026, 9, 28)   # a Monday


def payload(sid, lines=None, ctx=None, five=None, cwd=""):
    d = {"session_id": sid, "workspace": {"current_dir": cwd}}
    if lines is not None:
        d["cost"] = {"total_lines_added": lines, "total_lines_removed": 0}
    if ctx is not None:
        d["context_window"] = {"used_percentage": ctx}
    if five is not None:
        d["rate_limits"] = {"five_hour": {"used_percentage": five}}
    return d


def work(s, start, minutes, ev, five=100, lines=None, step=60):
    """Tick once a minute with ctx changing — what real use looks like to the pet."""
    for i in range(0, minutes * 60 + 1, step):
        ln = None if lines is None else lines + i
        P.tick(s, start + i, ev, data=payload("W", ctx=40 + (i // step) % 2, five=five, lines=ln), poll=False)


class PetCase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        os.environ["PET_HOME"] = self.dir
        os.environ.pop("PET_DISABLE", None)

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)
        os.environ.pop("PET_HOME", None)

    def fresh(self, now):
        s = P.new_state(now)
        s["streak"]["through"] = (P._date(now) - dt.timedelta(days=1)).isoformat()
        return s


class WorkClock(PetCase):
    def test_weekday_clipped_to_hours(self):
        self.assertAlmostEqual(P.work_minutes(ts(*MON, 8), ts(*MON, 18)), 480)

    def test_overnight(self):
        self.assertAlmostEqual(P.work_minutes(ts(*MON, 16), ts(2026, 9, 29, 10)), 120)

    def test_weekend_is_free(self):
        self.assertEqual(P.work_minutes(ts(2026, 10, 3, 9), ts(2026, 10, 4, 23)), 0)

    def test_dst_weekend(self):
        # Fri 16:00 → Mon 10:00 across the DST jump: still exactly 2 work hours
        self.assertAlmostEqual(P.work_minutes(ts(2026, 10, 2, 16), ts(2026, 10, 5, 10)), 120)

    def test_reversed_or_none(self):
        self.assertEqual(P.work_minutes(ts(*MON, 12), ts(*MON, 10)), 0)
        self.assertEqual(P.work_minutes(None, ts(*MON, 10)), 0)


class Baselines(PetCase):
    def test_two_sessions_no_double_count(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A", lines=100), poll=False)
        P.tick(s, now + 5, ev, data=payload("B", lines=150), poll=False)
        P.tick(s, now + 10, ev, data=payload("A", lines=150), poll=False)
        P.tick(s, now + 15, ev, data=payload("B", lines=150), poll=False)
        P.tick(s, now + 20, ev, data=payload("A", lines=150), poll=False)
        self.assertEqual(s["days"]["2026-09-28"]["lines"], 50)

    def test_new_session_nonzero_start_adds_nothing(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A", lines=900), poll=False)
        P.tick(s, now + 5, ev, data=payload("A", lines=900), poll=False)
        self.assertEqual(s["days"]["2026-09-28"]["lines"], 0)
        self.assertEqual(s["stats"]["xp"], 0)

    def test_missing_cost_then_total_is_baseline(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A"), poll=False)
        P.tick(s, now + 5, ev, data=payload("A", lines=500), poll=False)
        P.tick(s, now + 10, ev, data=payload("A", lines=525), poll=False)
        self.assertEqual(s["days"]["2026-09-28"]["lines"], 25)
        self.assertEqual(s["stats"]["xp"], 1)

    def test_fed_lines_capped_per_tick(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        s["stats"]["fed"] = 50
        P.tick(s, now, ev, data=payload("A", lines=0), poll=False)
        P.tick(s, now, ev, data=payload("A", lines=10_000), poll=False)
        self.assertAlmostEqual(s["stats"]["fed"], 60)


def git(*args, cwd, env=None):
    subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", *args],
                   cwd=cwd, check=True, capture_output=True, env=dict(os.environ, **(env or {})))


class Commits(PetCase):
    def make_repo(self, name):
        path = os.path.join(self.dir, name)
        os.makedirs(path)
        git("init", "-q", "-b", "main", cwd=path)
        git("config", "user.email", "pet@test.local", cwd=path)
        git("config", "user.name", "Pet", cwd=path)
        git("commit", "-q", "--allow-empty", "-m", f"init {name}", cwd=path)
        return os.path.realpath(path)

    def test_dedupe_across_worktrees_and_repos(self):
        a = self.make_repo("a")
        wt = os.path.join(self.dir, "a-wt")
        git("worktree", "add", "-q", "-b", "feat", wt, cwd=a)
        b = self.make_repo("b")
        now = time.time()   # git's --since=midnight uses the real clock
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("S1", ctx=10, cwd=a))
        P.tick(s, now + 1, ev, data=payload("S2", ctx=10, cwd=wt))   # same SHA via worktree
        P.tick(s, now + 2, ev, data=payload("S3", ctx=10, cwd=b))
        day = s["days"][P._date(now).isoformat()]
        self.assertEqual(day["commits"], 2)
        self.assertEqual(s["stats"]["xp"], 30)   # 2 × 10 × 1.5 (ctx < 50)
        # re-poll after throttle: nothing new
        P.tick(s, now + 120, ev, data=payload("S1", ctx=10, cwd=a))
        self.assertEqual(day["commits"], 2)

    def poll(self, s, ev, t, cwd, sid="S1"):
        P.tick(s, t, ev, data=payload(sid, cwd=cwd))
        return s["days"][P._date(t).isoformat()]["commits"]

    def test_amend_and_rebase_not_recounted(self):
        a = self.make_repo("a")
        git("commit", "-q", "--allow-empty", "-m", "two", cwd=a)
        now = time.time()
        s, ev = self.fresh(now), []
        self.assertEqual(self.poll(s, ev, now, a), 2)
        before = subprocess.run(["git", "-C", a, "rev-parse", "HEAD"], capture_output=True, text=True).stdout
        later = {"GIT_COMMITTER_DATE": f"@{int(now) + 5} +0000"}   # same second would reproduce the same sha
        git("commit", "-q", "--amend", "--no-edit", "--allow-empty", cwd=a, env=later)
        after = subprocess.run(["git", "-C", a, "rev-parse", "HEAD"], capture_output=True, text=True).stdout
        self.assertNotEqual(before, after)
        self.assertEqual(self.poll(s, ev, now + 61, a), 2)

    def test_author_is_exact(self):
        a = self.make_repo("a")                               # pet@test.local
        git("-c", "user.email=teampet@test.local", "commit", "-q", "--allow-empty", "-m", "theirs", cwd=a)
        git("-c", "user.email=pet@test_local", "commit", "-q", "--allow-empty", "-m", "dot trap", cwd=a)
        now = time.time()
        s, ev = self.fresh(now), []
        self.assertEqual(self.poll(s, ev, now, a), 1)

    def test_plus_in_email(self):
        a = self.make_repo("a")
        git("config", "user.email", "pet+cc@test.local", cwd=a)
        git("commit", "-q", "--allow-empty", "-m", "mine", cwd=a)
        now = time.time()
        s, ev = self.fresh(now), []
        self.assertEqual(self.poll(s, ev, now, a), 1)

    def test_old_sha_only_state_does_not_recount(self):
        a = self.make_repo("a")
        now = time.time()
        s, ev = self.fresh(now), []
        s["seen_commits"] = {subprocess.run(["git", "-C", a, "rev-parse", "HEAD"], capture_output=True,
                                            text=True).stdout.strip(): P._date(now).isoformat()}
        self.assertEqual(self.poll(s, ev, now, a), 0)

    def test_poll_throttled(self):
        a = self.make_repo("a")
        now = time.time()
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("S1", cwd=a))
        git("commit", "-q", "--allow-empty", "-m", "two", cwd=a)
        P.tick(s, now + 10, ev, data=payload("S1", cwd=a))
        self.assertEqual(s["days"][P._date(now).isoformat()]["commits"], 1)
        P.tick(s, now + 61, ev, data=payload("S1", cwd=a))
        self.assertEqual(s["days"][P._date(now).isoformat()]["commits"], 2)

    def test_xp_multiplier(self):
        self.assertEqual(P.xp_multiplier(49), 1.5)
        self.assertEqual(P.xp_multiplier(69), 1.0)
        self.assertEqual(P.xp_multiplier(95), 0.7)
        self.assertEqual(P.xp_multiplier(None), 1.5)


class Hooks(PetCase):
    def test_auto_vs_manual_compact(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, hook=("PreCompact", {"trigger": "manual"}))
        self.assertEqual(s["stats"]["fit"], 70)
        self.assertEqual(P.mood(s, now), "content")
        P.tick(s, now, ev, hook=("PreCompact", {"trigger": "auto"}))
        self.assertEqual(s["stats"]["fit"], 50)
        self.assertEqual(P.mood(s, now + 60), "bloated")
        self.assertNotEqual(P.mood(s, now + 31 * 60), "bloated")
        self.assertEqual(s["days"]["2026-09-28"]["auto_compacts"], 1)

    def wrap(self, ctx, lines, reason="clear"):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A", lines=0, ctx=ctx), poll=False)
        P.tick(s, now, ev, data=payload("A", lines=lines, ctx=ctx), poll=False)
        P.tick(s, now, ev, hook=("SessionEnd", {"session_id": "A", "reason": reason}))
        return s

    def test_clean_wrap_threshold(self):
        self.assertEqual(self.wrap(69, 10)["stats"]["fit"], 82)
        self.assertEqual(self.wrap(70, 10)["stats"]["fit"], 70)
        self.assertEqual(self.wrap(20, 0)["stats"]["fit"], 70)          # nothing shipped
        self.assertEqual(self.wrap(20, 10, "resume")["stats"]["fit"], 70)
        s = self.wrap(20, 10, "prompt_input_exit")
        self.assertEqual(s["stats"]["fit"], 82)
        self.assertNotIn("A", s["sessions"])


class Moods(PetCase):
    def test_priority(self):
        now = ts(*MON, 10)
        s = self.fresh(now)
        s["last_commit_at"] = now
        st = s["stats"]
        st.update(fed=10, fit=10, rested=10)
        s["status"]["bloated_until"] = now + 10
        self.assertEqual(P.mood(s, now), "bloated")
        s["status"]["bloated_until"] = 0
        self.assertEqual(P.mood(s, now), "exhausted")
        st["rested"] = 30
        self.assertEqual(P.mood(s, now), "starving")
        st["fed"] = 30
        self.assertEqual(P.mood(s, ts(*MON, 20)), "sleepy")
        self.assertEqual(P.mood(s, now), "sulky")
        st.update(fed=90, fit=90, rested=90)
        self.assertEqual(P.mood(s, now), "content")    # no streak yet
        s["streak"]["count"] = 3
        self.assertEqual(P.mood(s, now), "thriving")

    def test_missed_you_until_commit(self):
        now = ts(*MON, 10)
        s = self.fresh(now)
        s["last_commit_at"] = ts(2026, 9, 23, 10)   # previous Wednesday
        self.assertEqual(P.mood(s, now), "missed")
        s["last_commit_at"] = now
        self.assertNotEqual(P.mood(s, now), "missed")

    def test_rested_target(self):
        self.assertEqual(P.rested_target(None), 80)
        self.assertEqual(P.rested_target(40), 100)
        self.assertAlmostEqual(P.rested_target(75), 55)
        self.assertAlmostEqual(P.rested_target(100), 10)

    def test_floor_and_decay(self):
        now = ts(*MON, 9)
        s, ev = self.fresh(now), []
        P.tick(s, ts(*MON, 17), ev)
        self.assertEqual(s["stats"]["fed"], 5)

    def test_redline_drains_fit(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A", ctx=90), poll=False)
        P.tick(s, now + 100, ev, data=payload("A", ctx=91), poll=False)
        P.tick(s, now + 200, ev, data=payload("A", ctx=92), poll=False)
        self.assertLess(s["stats"]["fit"], 70)

    def test_idle_parked_session_is_not_you(self):
        # a multiplexer keeps a parked 90% session rendering all day: no fit drain, no steady credit
        now = ts(*MON, 9)
        s, ev = self.fresh(now), []
        for i in range(0, 8 * 3600, 60):
            P.tick(s, now + i, ev, data=payload("A", ctx=90, five=10, lines=5), poll=False)
        self.assertEqual(s["stats"]["fit"], 70)
        self.assertEqual(s["temper"]["calm"], 0)


class Streaks(PetCase):
    def test_weekends_skipped(self):
        now = ts(2026, 10, 6, 10)   # Tue
        s = self.fresh(now)
        s["streak"]["through"] = "2026-09-30"
        good = {"commits": 1, "fit": 60}
        s["days"].update({"2026-10-01": good, "2026-10-02": good, "2026-10-05": good})
        P.update_streak(s, now)
        self.assertEqual(s["streak"]["count"], 3)
        self.assertEqual(s["streak"]["through"], "2026-10-05")

    def test_bad_weekday_breaks(self):
        now = ts(2026, 10, 6, 10)
        s = self.fresh(now)
        s["streak"].update(count=4, through="2026-10-01")
        s["days"]["2026-10-02"] = {"commits": 1, "fit": 40}
        s["days"]["2026-10-05"] = {"commits": 2, "fit": 90}
        P.update_streak(s, now)
        self.assertEqual(s["streak"]["count"], 1)

    def test_provisional_today(self):
        now = ts(2026, 10, 6, 10)
        s = self.fresh(now)
        s["streak"]["count"] = 4
        s["days"]["2026-10-06"] = {"commits": 1, "fit": 80}
        self.assertEqual(P.current_streak(s, now), 5)
        self.assertIn(P.CROWN, P.render(s, now))


class Levels(PetCase):
    def test_curve(self):
        self.assertEqual(P.level(0), 1)
        for lvl in (2, 5, 15, 30):
            x = P.xp_for_level(lvl)
            self.assertEqual(P.level(x), lvl)
            self.assertEqual(P.level(x - 1), lvl - 1)
        self.assertTrue(80 < P.xp_for_level(5) < 130)       # ~1–2 days at 80/day
        self.assertTrue(700 < P.xp_for_level(15) < 900)     # ~2 weeks
        self.assertTrue(2400 < P.xp_for_level(30) < 3000)   # ~7 weeks

    def test_faces_keep_mood_mouths(self):
        self.assertEqual(P.face("bear", "content"), "ʕ•ᴥ•ʔ")
        self.assertEqual(P.face("bear", "bloated"), "ʕ°□°ʔ")
        self.assertEqual(P.face("great owl", "sleepy", blink=True), "{(-)_(-)}")
        self.assertEqual(P.face("no-such-form", "content"), "(•ᴗ•)")

    def test_every_form_reachable(self):
        finals = {f for d in P.FINALS.values() for f in d.values()}
        self.assertEqual({"hatchling"} | set(P.SPECIES.values()) | finals, set(P.FORMS))


class Evolution(PetCase):
    def evolve_with(self, temper, xp, form="hatchling"):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        s["form"] = form
        s["temper"].update(temper)
        s["stats"]["xp"] = xp
        P.tick(s, now, ev)
        return s, ev

    def test_species_by_trait(self):
        L5 = P.xp_for_level(5)
        self.assertEqual(self.evolve_with({"ship": 2}, L5)[0]["form"], "bear")
        self.assertEqual(self.evolve_with({"tidy": 1}, L5)[0]["form"], "cat")
        self.assertEqual(self.evolve_with({"calm": 200}, L5)[0]["form"], "owl")
        self.assertEqual(self.evolve_with({"ship": 2, "tidy": 1, "calm": 200}, L5)[0]["form"], "fox")
        self.assertEqual(self.evolve_with({"ship": 1}, L5 - 1)[0]["form"], "hatchling")

    def test_final_by_trait_at_l15(self):
        L15 = P.xp_for_level(15)
        s, ev = self.evolve_with({"tidy": 1}, L15, form="bear")
        self.assertEqual(s["form"], "koala")
        self.assertTrue(any(e["kind"] == "evolve" for e in ev))
        self.assertIn("koala", s["status"]["react"]["text"])
        self.assertEqual(self.evolve_with({"ship": 2}, L15, form="owl")[0]["form"], "penguin")
        self.assertEqual(self.evolve_with({"calm": 200}, L15, form="fox")[0]["form"], "kitsune")

    def test_skipped_tiers_still_branch(self):
        s, _ = self.evolve_with({"ship": 2}, P.xp_for_level(20))
        self.assertEqual(s["form"], "seal")

    def test_final_is_locked(self):
        s, _ = self.evolve_with({"tidy": 5}, P.xp_for_level(30), form="seal")
        self.assertEqual(s["form"], "seal")

    def test_auto_compacts_cancel_tidy(self):
        self.assertEqual(P.trait_scores({"temper": {"tidy": -2}})["tidy"], 0)

    def test_sliver_of_calm_is_not_steady(self):
        self.assertLess(P.trait_scores({"temper": {"calm": 0.01}})["steady"], 0.01)
        self.assertEqual(P.trait_scores({"temper": {"calm": 120}})["steady"], 1.0)

    def test_hot_usage_is_not_steady(self):
        self.assertEqual(P.trait_scores({"temper": {"calm": 200, "hot": 200}})["steady"], 0)

    def test_temper_tracks_and_decays(self):
        now = ts(*MON, 10)
        s, ev = self.fresh(now), []
        P.tick(s, now, ev, data=payload("A", ctx=10, five=20), poll=False)
        P.tick(s, now + 600, ev, data=payload("A", ctx=11, five=20), poll=False)
        self.assertAlmostEqual(s["temper"]["calm"], 10, delta=0.5)
        s["temper"]["ship"] = 2
        P.tick(s, now + 600 + 4 * 3600, ev, poll=False)     # one tau of work time
        self.assertAlmostEqual(s["temper"]["ship"], 2 / 2.718, delta=0.05)
        P.tick(s, ts(2026, 10, 3, 12), ev, poll=False)      # Saturday: no work time, no decay
        frozen = s["temper"]["ship"]
        P.tick(s, ts(2026, 10, 4, 12), ev, poll=False)
        self.assertEqual(s["temper"]["ship"], frozen)


class Devolution(PetCase):
    def wilt(self, form, xp, stats):
        now = ts(*MON, 9)
        s, ev = self.fresh(now), []
        s["form"], s["stats"]["xp"] = form, xp
        s["stats"].update(stats)
        return s, ev, now

    def test_misuse_devolves_final_to_species(self):
        s, ev, now = self.wilt("koala", P.xp_for_level(16), dict(fed=5, fit=10, rested=10))
        work(s, now, 60, ev)
        self.assertEqual(s["form"], "koala")                # 60 min: wilting, not yet
        self.assertEqual(P.mood(s, now + 3600), "wilting")
        work(s, now + 3660, 31, ev)
        self.assertEqual(s["form"], "bear")
        self.assertEqual(P.level(s["stats"]["xp"]), P.T["final_level"] - P.T["devolve_levels"])
        self.assertEqual(s["devolved"], 1)
        self.assertTrue(any(e["kind"] == "devolve" for e in ev))

    def test_species_to_hatchling_then_rebranch(self):
        s, ev, now = self.wilt("bear", P.xp_for_level(8), dict(fed=5, fit=10, rested=10))
        work(s, now, 91, ev)
        self.assertEqual(s["form"], "hatchling")
        s["temper"] = {"ship": 0, "tidy": 1, "calm": 0, "hot": 0}
        s["stats"]["xp"] = P.xp_for_level(5)
        P.tick(s, now + 92 * 60, ev, poll=False)
        self.assertEqual(s["form"], "cat")

    def test_absence_with_low_stats_never_devolves(self):
        # reviewer's repro: avg 50 → away a workday → fed floors; must not count as misuse
        s, ev, now = self.wilt("koala", P.xp_for_level(16), dict(fed=70, fit=40, rested=40))
        P.tick(s, now, ev, data=payload("A", ctx=30), poll=False)
        P.tick(s, now + 86400, ev, data=payload("A", ctx=30), poll=False)
        self.assertEqual(s["form"], "koala")
        self.assertEqual(s["wilt"], 0)

    def test_busy_day_without_commits_does_not_wilt(self):
        # actively working, fed floored from no commits yet, fit/rested middling: not misuse
        s, ev, now = self.wilt("koala", P.xp_for_level(16), dict(fed=5, fit=40, rested=40))
        work(s, now, 180, ev, five=75)                       # rested target 55
        self.assertEqual(s["form"], "koala")
        self.assertEqual(s["wilt"], 0)

    def test_idle_misuse_state_does_not_accrue(self):
        s, ev, now = self.wilt("koala", P.xp_for_level(16), dict(fit=10, rested=10))
        for i in range(0, 4 * 3600, 60):
            P.tick(s, now + i, ev, data=payload("A", ctx=30, five=100), poll=False)
        self.assertEqual(s["form"], "koala")

    def test_devolve_caps_same_tick_gains(self):
        s, ev, now = self.wilt("bear", P.xp_for_level(8), dict(fed=5, fit=10, rested=10))
        work(s, now, 89, ev, lines=0)
        self.assertEqual(s["form"], "bear")
        # the crossing tick also carries a huge line gain: devolve must still win
        P.tick(s, now + 90 * 60 + 30, ev, data=payload("W", ctx=77, five=100, lines=100_000), poll=False)
        self.assertTrue(any(e["kind"] == "devolve" for e in ev))
        self.assertEqual(s["form"], "hatchling")
        self.assertLess(P.level(s["stats"]["xp"]), P.T["species_level"])

    def test_absence_never_wilts(self):
        s, ev, now = self.wilt("seal", P.xp_for_level(16), {})
        P.tick(s, now + 5 * 86400, ev, poll=False)          # fed floors, fit/rested hold
        self.assertEqual(s["form"], "seal")
        self.assertEqual(s["wilt"], 0)

    def test_recovery_unwinds_wilt(self):
        s, ev, now = self.wilt("seal", P.xp_for_level(16), dict(fed=5, fit=10, rested=10))
        work(s, now, 60, ev)
        self.assertGreater(s["wilt"], 0)
        s["stats"].update(fed=90, fit=90, rested=90)
        P.tick(s, now + 120 * 60, ev, poll=False)
        self.assertEqual(s["wilt"], 0)
        self.assertEqual(s["form"], "seal")

    def test_manual_devolve_matches_preview(self):
        s, ev, now = self.wilt("koala", P.xp_for_level(18), {})
        back, keep = P.devolve_preview(s)
        P.devolve(s, now, ev, reason="chose to devolve")
        self.assertEqual((s["form"], s["stats"]["xp"]), (back, keep))
        self.assertEqual(ev[-1]["msg"], "chose to devolve back into a bear")
        self.assertIsNone(P.devolve_preview(dict(s, form="hatchling")))

    def test_cli_devolve_prompt(self):
        s = P.new_state(ts(*MON, 10))
        s["form"], s["stats"]["xp"] = "seal", P.xp_for_level(16)
        P.save_state(s)
        cli = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pet_cli.py")
        run = lambda inp: subprocess.run([sys.executable, cli, "devolve"], input=inp, text=True,
                                         capture_output=True, env=dict(os.environ))
        self.assertIn("Left as is", run("n\n").stdout)
        self.assertEqual(P.read_state()["form"], "seal")
        self.assertIn("bear again", run("y\n").stdout)
        self.assertEqual(P.read_state()["form"], "bear")

    def test_hatchling_cannot_devolve(self):
        s, ev, now = self.wilt("hatchling", 10, dict(fed=5, fit=10, rested=10))
        work(s, now, 91, ev)
        self.assertEqual(s["form"], "hatchling")
        self.assertEqual(s["stats"]["xp"], 10)


class Looks(PetCase):
    def test_look_is_deterministic_and_backfilled(self):
        a = P.roll_look(1234.5)
        self.assertEqual(a, P.roll_look(1234.5))
        old = P.new_state(1234.5)
        del old["look"]
        self.assertEqual(P.ensure(old)["look"], a)

    def test_looks_vary(self):
        looks = [P.roll_look(t) for t in range(500)]
        self.assertGreater(len({l["eyes"] for l in looks}), 3)
        self.assertGreater(len({l["charm"] for l in looks}), 3)
        self.assertTrue(any(l["shiny"] for l in looks))

    def test_render_uses_look(self):
        now = ts(*MON, 10, 0) + 1                            # frame not a blink frame
        s = self.fresh(now)
        s["last_commit_at"] = now
        s["look"] = {"eyes": "◕", "charm": "✿", "shiny": True}
        out = P.render(s, now, {"good": "G", "meh": "M", "bad": "B", "reset": "R"})
        self.assertEqual(out, f"{P.SHINY}(◕ᴗ◕)✿ contentR")

    def test_reaction_then_mood(self):
        now = ts(*MON, 10) + 1
        s = self.fresh(now)
        s["last_commit_at"] = now
        P.react(s, now, "nom +15xp")
        self.assertIn("nom +15xp", P.render(s, now))
        self.assertIn("(^ᴗ^)", P.render(s, now))
        self.assertIn("content", P.render(s, now + 31))


class Storage(PetCase):
    def test_corrupt_moved_aside(self):
        with open(os.path.join(self.dir, "state.json"), "w") as f:
            f.write("{nope")
        out = P.segment({}, now=ts(*MON, 10))
        self.assertTrue(out)
        names = os.listdir(self.dir)
        self.assertTrue(any(n.startswith("state.json.corrupt-") for n in names))
        self.assertIsNotNone(P.read_state())

    def test_garbage_payload_is_fine(self):
        for junk in ({}, [], "x", None, {"session_id": "A", "cost": "bad", "context_window": 3}):
            try:
                P.segment(junk, now=ts(*MON, 10))
            except Exception as e:
                self.fail(f"{junk!r}: {e}")

    def test_lock_contention_is_read_only(self):
        now = ts(*MON, 10)
        P.segment(payload("A", lines=0), now=now)
        with open(os.path.join(self.dir, "state.json")) as f:
            before = f.read()
        holder = subprocess.Popen(
            [sys.executable, "-c",
             "import fcntl,os,sys,time\n"
             f"fd=os.open({os.path.join(self.dir, '.lock')!r}, os.O_RDWR|os.O_CREAT)\n"
             "fcntl.flock(fd, fcntl.LOCK_EX); print('held', flush=True); time.sleep(5)"],
            stdout=subprocess.PIPE, text=True)
        try:
            self.assertEqual(holder.stdout.readline().strip(), "held")
            out = P.segment(payload("A", lines=500), now=now + 60)
            self.assertTrue(out)
            with open(os.path.join(self.dir, "state.json")) as f:
                self.assertEqual(f.read(), before)
        finally:
            holder.kill()
            holder.wait()
            holder.stdout.close()

    def test_config_overrides_numbers_only(self):
        with open(os.path.join(self.dir, "config.json"), "w") as f:
            json.dump({"work_start": 7, "work_end": 15, "xp_mult": "nope", "bogus": 1, "floor": True}, f)
        saved = dict(P.T)
        try:
            P.load_config()
            self.assertEqual((P.T["work_start"], P.T["work_end"]), (7, 15))
            self.assertEqual(P.T["xp_mult"], saved["xp_mult"])
            self.assertEqual(P.T["floor"], saved["floor"])
            self.assertNotIn("bogus", P.T)
            self.assertTrue(P.is_work_time(ts(*MON, 7, 30)))
            self.assertFalse(P.is_work_time(ts(*MON, 15, 30)))
        finally:
            P.T.clear()
            P.T.update(saved)

    def test_bad_config_ignored(self):
        with open(os.path.join(self.dir, "config.json"), "w") as f:
            f.write("{nope")
        P.load_config()
        self.assertTrue(P.segment({}, now=ts(*MON, 10)))

    def test_segment_mode(self):
        sl = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pet_statusline.py")
        run = lambda *a: subprocess.run([sys.executable, sl, *a], input='{"model":{"display_name":"Opus"}}',
                                        text=True, capture_output=True, env=dict(os.environ)).stdout
        self.assertIn("Opus", run())
        seg = run("--segment")
        self.assertNotIn("Opus", seg)
        self.assertIn("(", seg)

    def test_disable(self):
        os.environ["PET_DISABLE"] = "1"
        try:
            self.assertIsNone(P.segment({}, now=ts(*MON, 10)))
            self.assertFalse(os.path.exists(os.path.join(self.dir, "state.json")))
        finally:
            del os.environ["PET_DISABLE"]

    def test_events_logged(self):
        P.segment({}, now=ts(*MON, 10))
        evs = P.read_events(5)
        self.assertEqual(evs[0]["kind"], "hatch")


if __name__ == "__main__":
    unittest.main()
