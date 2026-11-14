import json
import unittest
from unittest.mock import patch

import stash_silo_companion as plugin


class PlanTests(unittest.TestCase):
    def test_fill_only_preserves_existing_values(self):
        scene = {
            "id": "42", "title": "Curated title", "code": "", "details": "", "director": "Curated director",
            "date": "", "urls": ["https://example.invalid/existing"],
            "studio": {"id": "10", "name": "Curated studio"},
            "performers": [{"id": "11", "name": "Curated performer"}],
            "tags": [{"id": "12", "name": "Curated tag"}], "paths": {"screenshot": "https://stash/scene/42/screenshot"},
        }
        source = {
            "code": "ATID-705", "title": "Backend title", "details": "Backend details",
            "director": "Backend director", "date": "2026-10-01", "source_url": "https://jav.invalid/705",
            "studio": "Backend studio", "performers": ["Backend performer"], "tags": ["Backend tag"],
            "poster_path": "/covers/1/stash-poster",
        }
        got = plugin._plan(scene, source, cover_mode="missing")
        self.assertEqual(got, {"id": "42", "code": "ATID-705", "details": "Backend details", "date": "2026-10-01"})

    def test_cover_requires_explicit_mode_or_missing_screenshot(self):
        scene = {"id": "42", "paths": {"screenshot": "https://stash/screenshot"}}
        source = {"poster_path": "/covers/1/stash-poster"}
        self.assertNotIn("_poster_path", plugin._plan(scene, source, cover_mode="missing"))
        scene["paths"]["screenshot"] = None
        self.assertEqual(plugin._plan(scene, source, cover_mode="missing")["_poster_path"], source["poster_path"])

    def test_update_guards_existing_relations(self):
        scene = {"id": "42", "title": "", "code": "", "details": "", "director": "", "date": "", "urls": [],
                 "studio": {"id": "10"}, "performers": [{"id": "11"}], "tags": [{"id": "12"}], "paths": {"screenshot": "x"}}
        calls = []
        def graphql(_payload, query, variables=None):
            calls.append((query, variables))
            if "findScene" in query:
                return {"findScene": scene}
            return {"sceneUpdate": {"id": "42"}}
        with patch.object(plugin, "_stash_graphql", side_effect=graphql), patch.object(plugin, "_enrichment", return_value={"scene_id": "42", "title": "New title"}):
            result = plugin._enrich_scene({}, {}, "42", dry_run=False)
        self.assertEqual(result["state"], "updated")
        update = calls[-1][1]["input"]
        self.assertEqual(update["studio_id"], "10")
        self.assertEqual(update["performer_ids"], ["11"])
        self.assertEqual(update["tag_ids"], ["12"])
        self.assertEqual(update["title"], "New title")


class HookAndScanTests(unittest.TestCase):
    @patch.object(plugin, "_enrich_scene", return_value={"state": "updated"})
    @patch.object(plugin, "_settings", return_value={"auto_enrich": True, "javbeacon_url": "http://backend"})
    def test_hook_enriches_without_webhook_secret(self, _settings, enrich):
        with patch.object(plugin.sys, "stdin") as stdin:
            stdin.read.return_value = '{}'
            with patch.object(plugin.json, "load", return_value={"args": {"mode": "hook", "hookContext": {"id": "42", "type": "Scene.Update.Post"}}}):
                result = plugin.main()["output"]
        self.assertEqual(result["realtime"], {"state": "disabled"})
        self.assertEqual(result["enrichment"], {"state": "updated"})
        enrich.assert_called_once()

    def test_tag_only_save_syncs_watchlist_without_metadata_work(self):
        payload = {"args": {"mode": "hook", "hookContext": {"id": "42", "type": "Scene.Update.Post", "inputFields": ["ids", "tag_ids"]}}}
        with patch.object(plugin.json, "load", return_value=payload), \
             patch.object(plugin, "_settings", return_value={"watchlist_tag_id":"9"}), \
             patch.object(plugin, "_scene", return_value={"tags":[{"id":"9"}]}), \
             patch.object(plugin, "_stash_graphql", return_value={"runPluginTask":"job-1"}) as queued, \
             patch.object(plugin, "_sync_silo_watchlist_collection") as sync, \
             patch.object(plugin, "_enrich_scene") as enrich, \
             patch.object(plugin, "_refresh_silo_scene") as refresh:
            result = plugin.main()["output"]
        sync.assert_not_called()
        enrich.assert_not_called()
        refresh.assert_not_called()
        self.assertEqual(result["watchlist_job"], "job-1")
        self.assertTrue(queued.call_args.args[2]["args"]["desired"])

    @patch.object(plugin, "_enrich_scene", return_value={"state": "unchanged"})
    @patch.object(plugin, "_stash_graphql")
    def test_scan_cursor_resumes_exactly_after_limit(self, graphql, enrich):
        graphql.return_value = {"findScenes": {"scenes": [{"id": str(i)} for i in range(100)]}}
        result = plugin._scan({}, {"max_scenes_per_run": 2}, dry_run=True)
        self.assertEqual((result["next_page"], result["next_index"]), (1, 2))
        self.assertEqual([call.args[2] for call in enrich.call_args_list], ["0", "1"])
        enrich.reset_mock()
        result = plugin._scan({}, {"max_scenes_per_run": 2}, dry_run=True, start_page=result["next_page"], start_index=result["next_index"])
        self.assertEqual((result["next_page"], result["next_index"]), (1, 4))
        self.assertEqual([call.args[2] for call in enrich.call_args_list], ["2", "3"])


if __name__ == "__main__":
    unittest.main()


class SelfCheckTests(unittest.TestCase):
    SETTINGS = {"silo_url": "https://silo.example.com", "silo_api_key": "secret-silo", "javbeacon_url": "https://jav.example.com",
                "javbeacon_api_key": "secret-jav", "webhook_secret": "secret-hook", "watchlist_tag_id": "1355"}

    def run_check(self, settings=None, tag=None, silo=None, jav=None):
        tag = {"findTag": {"id": "1355", "name": "Watchlist"}} if tag is None else tag
        silo = silo if silo is not None else {"/api/v2/system/info": {"server_version": "abc"}, "/api/v2/admin/collections": {"items": [
            {"title": "Watchlist", "description": "Managed by JAVBeacon metadata plugin.", "library_id": "16"}]}}
        jav = jav if jav is not None else {"/api/health": {"status": "ok"}, "/api/v1/integrations/silo/stash-saved-filters?selection=Watchlist": {"items": [{"name": "Watchlist"}]}}

        def fake_silo(_settings, path, profile_id=None):
            if isinstance(silo.get(path), Exception):
                raise silo[path]
            return silo[path]

        def fake_backend(_settings, path, binary=False):
            if isinstance(jav.get(path), Exception):
                raise jav[path]
            return jav.get(path)

        with patch.object(plugin, "_stash_graphql", return_value=tag), patch.object(plugin, "_silo_get", side_effect=fake_silo), patch.object(plugin, "_backend", side_effect=fake_backend):
            return plugin._self_check({}, dict(self.SETTINGS if settings is None else settings))

    def test_all_good(self):
        result = self.run_check()
        self.assertEqual(result["status"], "ok")
        self.assertTrue(all(c["ok"] for c in result["checks"]))

    def test_reports_each_problem_without_secrets(self):
        settings = dict(self.SETTINGS, watchlist_tag_id="")
        result = self.run_check(settings=settings, silo={"/api/v2/system/info": RuntimeError("could not reach Silo"), "/api/v2/admin/collections": RuntimeError("Silo returned HTTP 401")},
                                jav={"/api/health": None, "/api/v1/integrations/silo/stash-saved-filters?selection=Watchlist": RuntimeError("JAVBeacon returned HTTP 403")})
        self.assertEqual(result["status"], "attention")
        failed = {c["name"] for c in result["checks"] if not c["ok"]}
        self.assertEqual(failed, {"watchlist_tag_id", "silo_reachable", "silo_admin_access", "javbeacon_reachable", "javbeacon_access"})
        text = json.dumps(result)
        for secret in ("secret-silo", "secret-jav", "secret-hook"):
            self.assertNotIn(secret, text)

    def test_missing_tag_in_stash_and_no_watchlist_collection(self):
        result = self.run_check(tag={"findTag": None}, silo={"/api/v2/system/info": {}, "/api/v2/admin/collections": {"items": []}})
        failed = {c["name"] for c in result["checks"] if not c["ok"]}
        self.assertEqual(failed, {"watchlist_tag_exists", "silo_watchlist_collections"})
