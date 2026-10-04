import unittest
from unittest.mock import patch
import stash_metadata as plugin

class SiloImportTests(unittest.TestCase):
    def test_source_ignores_missing_entity_names(self):
        source = plugin._silo_source({"title": "Scene", "people": [{"name": "Actor"}, {"id": "missing-name"}], "genres": [{"name": "Drama"}, {}]})
        self.assertEqual(source["performers"], ["Actor"])
        self.assertEqual(source["tags"], ["Drama"])

    def test_artwork_id_is_exact(self):
        self.assertEqual(plugin._silo_scene_id({"poster_url":"https://jav.example/api/v1/integrations/silo/stash/scenes/42936/cover?api_key=hidden"}),"42936")
        self.assertIsNone(plugin._silo_scene_id({"poster_url":"https://jav.example/other/42936"}))
    @patch.object(plugin,"_stash_graphql")
    def test_title_match_requires_single_exact_scene(self,graphql):
        graphql.return_value={"findScenes":{"scenes":[{"id":"42","code":"ATID705","title":"Other","files":[]},{"id":"43","code":"ATID7050","title":"Other","files":[]}]}}
        self.assertEqual(plugin._silo_unique_scene({}, {"title":"ATID-705"}),"42")
        graphql.return_value={"findScenes":{"scenes":[{"id":"42","code":"ATID705","title":"Other","files":[]},{"id":"44","code":"ATID-705","title":"Other","files":[]}]}}
        self.assertIsNone(plugin._silo_unique_scene({}, {"title":"ATID-705"}))

class SiloMigrationFlowTests(unittest.TestCase):
    @patch.object(plugin, "_enrich_scene", return_value={"scene_id":"42","state":"preview","fields":["details"]})
    @patch.object(plugin, "_silo_unique_scene", return_value="42")
    @patch.object(plugin, "_silo_get")
    def test_preview_reads_catalog_without_refresh(self, silo_get, unique, enrich):
        silo_get.side_effect = [
            {"items":[{"id":"profile"}]},
            {"items":[{"content_id":"movie:one","title":"ATID-705","overview":"Saved Silo description"}],"page":{"has_more":False}},
            {"content_id":"movie:one","title":"ATID-705","overview":"Saved Silo description"},
        ]
        result=plugin._import_silo({}, {"silo_library_id":"16"}, dry_run=True)
        self.assertEqual(result["counts"],{"preview":1})
        self.assertEqual(enrich.call_args.kwargs["source"]["details"],"Saved Silo description")
        self.assertTrue(enrich.call_args.kwargs["dry_run"])
        self.assertTrue(all("refresh-metadata" not in str(call) for call in silo_get.call_args_list))

class TargetedRefreshTests(unittest.TestCase):
    @patch.object(plugin, "_silo_post", return_value={"id":"job-1"})
    @patch.object(plugin, "_silo_get")
    @patch.object(plugin, "_scene")
    def test_scene_edit_refreshes_one_exact_silo_item(self, scene, silo_get, silo_post):
        scene.return_value={"id":"42","code":"ATID-705","title":"", "files":[]}
        silo_get.side_effect=[{"items":[{"id":"profile"}]},{"items":[{"content_id":"movie:one","title":"ATID705"},{"content_id":"movie:two","title":"ATID7050"}]}]
        settings={"silo_url":"http://silo", "silo_api_key":"key", "silo_library_id":"16"}
        result=plugin._refresh_silo_scene({},settings,"42")
        self.assertEqual(result["state"],"queued")
        self.assertIn("movie%3Aone",silo_post.call_args.args[1])
    @patch.object(plugin, "_silo_post")
    @patch.object(plugin, "_silo_get")
    @patch.object(plugin, "_scene")
    def test_ambiguous_scene_does_not_refresh(self, scene, silo_get, silo_post):
        scene.return_value={"id":"42","code":"ATID-705","title":"", "files":[]}
        silo_get.side_effect=[{"items":[{"id":"profile"}]},{"items":[{"content_id":"movie:one","title":"ATID705"},{"content_id":"movie:two","title":"ATID-705"}]}]
        settings={"silo_url":"http://silo", "silo_api_key":"key", "silo_library_id":"16"}
        self.assertEqual(plugin._refresh_silo_scene({},settings,"42")["state"],"ambiguous")
        silo_post.assert_not_called()

class RealtimeCollectionTests(unittest.TestCase):
    settings = {"silo_url": "https://silo.example", "silo_api_key": "key", "silo_library_id": "16", "watchlist_tag_id": "99"}
    hook = {"id": "42", "type": "Scene.Update.Post", "inputFields": ["id", "tag_ids"]}

    def _run(self, desired, current, linked_scene="42"):
        from unittest.mock import Mock
        scene = {"id": "42", "code": "ATID-705", "title": "", "files": [], "tags": [{"id": "99"}] if desired else []}
        def silo_get(_settings, path, profile_id=None):
            if path == "/api/v2/admin/collections":
                return {"items": [{"id": "7", "title": "WatchList", "library_id": "16", "collection_type": "manual"}]}
            if path == "/api/v2/profiles":
                return {"items": [{"id": "profile"}]}
            if path.startswith("/api/v2/catalog?"):
                self.assertEqual(profile_id, "profile")
                return {"items": [{"content_id": "movie:one", "title": "ATID705"}]}
            if path.startswith("/api/v2/catalog/items/"):
                return {"provider_ids": {"stash": linked_scene}}
            if path.startswith("/api/v2/admin/collections/7/items?"):
                return {"items": [{"media_item_id": "movie:one"}] if current else [], "page": {"has_more": False}}
            raise AssertionError(path)
        writes = []
        def urlopen(request, timeout):
            writes.append((request.get_method(), request.full_url, request.data))
            response = Mock(status=204)
            response.__enter__ = Mock(return_value=response)
            response.__exit__ = Mock(return_value=False)
            return response
        with patch.object(plugin, "_scene", return_value=scene), patch.object(plugin, "_silo_get", side_effect=silo_get), patch.object(plugin.urllib.request, "urlopen", side_effect=urlopen):
            result = plugin._sync_silo_watchlist_collection({}, self.settings, self.hook)
        return result, writes

    def test_unique_collection_infers_library_id(self):
        settings = dict(self.settings)
        settings.pop("silo_library_id")
        with patch.object(plugin, "_scene", return_value={"id": "42", "code": "ATID-705", "title": "", "files": [], "tags": [{"id": "99"}]}), \
             patch.object(plugin, "_silo_get") as silo_get, \
             patch.object(plugin.urllib.request, "urlopen") as urlopen:
            silo_get.side_effect = [
                {"items": [
                    {"id": "7", "title": "WatchList", "library_id": "16", "collection_type": "manual", "slug": "javbeacon-stash-preset-filter-library-16"},
                    {"id": "8", "title": "Watchlist", "library_id": "16", "collection_type": "manual", "slug": "javbeacon-watchlist-library-16"},
                ]},
                {"items": [{"id": "profile"}]},
                {"items": [{"content_id": "movie:one", "title": "ATID705"}]},
                {"provider_ids": {"stash": "42"}},
                {"items": [{"media_item_id": "movie:one"}], "page": {"has_more": False}},
            ]
            result = plugin._sync_silo_watchlist_collection({}, settings, self.hook)
        self.assertEqual(result["state"], "unchanged")
        self.assertIn("library_id=16", silo_get.call_args_list[2].args[1])
        urlopen.assert_not_called()

    def test_add_and_remove_update_existing_collection(self):
        added, writes = self._run(True, False)
        self.assertEqual(added["state"], "added")
        self.assertEqual(writes, [("PUT", "https://silo.example/api/v2/admin/collections/7/items/movie%3Aone", b'{"position":0}')])
        removed, writes = self._run(False, True)
        self.assertEqual(removed["state"], "removed")
        self.assertEqual(writes[0][0], "DELETE")

    def test_repeat_and_unrelated_hook_do_not_write(self):
        self.assertEqual(self._run(True, True)[1], [])
        with patch.object(plugin, "_scene") as scene:
            result = plugin._sync_silo_watchlist_collection({}, self.settings, {"id": "42", "type": "Scene.Update.Post", "inputFields": ["play_count"]})
        self.assertEqual(result["state"], "not_tag_update")
        scene.assert_not_called()

    def test_different_linked_scene_is_not_changed(self):
        result, writes = self._run(True, False, linked_scene="43")
        self.assertEqual(result["state"], "different_scene")
        self.assertEqual(writes, [])
