import unittest
from unittest.mock import patch
import stash_silo_companion as plugin

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
        silo_get.side_effect=[{"items":[{"id":"profile"}]},{"items":[{"content_id":"movie:one","title":"ATID705"},{"content_id":"movie:two","title":"ATID7050"}]},{"provider_ids":{"stash":"42"}}]
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
        scene = {"id": "42", "code": "ATID-705", "title": "", "files": [{"path": "/collections/ATID-705.mp4"}], "tags": [{"id": "99"}] if desired else []}
        def silo_get(_settings, path, profile_id=None):
            if path == "/api/v2/admin/plugins/installations":
                return {"items": [{"plugin_id": "stash.metadata", "global_configs": {"connection": {"stash_saved_filter_prefix": "Stash | "}}}]}
            if path == "/api/v2/admin/collections":
                return {"items": [{"id": "7", "title": "Stash | Watchlist", "slug": "javbeacon-stash-preset-3-library-test", "library_id": "16", "collection_type": "manual"}]}
            if path == "/api/v2/profiles":
                return {"items": [{"id": "profile"}]}
            if path.startswith("/api/v2/catalog?"):
                self.assertEqual(profile_id, "profile")
                return {"items": [{"content_id": "movie:one", "title": "ATID705"}]}
            if path.startswith("/api/v2/catalog/items/"):
                return {"provider_ids": {"stash": linked_scene}}
            if path.startswith("/api/v2/admin/items/"):
                return {"items": [{"file_path": "/collections/ATID-705.mp4"}], "page": {"has_more": False}}
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
                {"items": [{"id": "16", "type": "movies", "enabled": True}]},
                {"items": [
                    {"id": "7", "title": "Stash | Watchlist", "slug": "javbeacon-stash-preset-3-library-test", "library_id": "16", "collection_type": "manual", "slug": "javbeacon-stash-preset-filter-library-16"},
                    {"id": "8", "title": "Watchlist", "library_id": "16", "collection_type": "manual", "slug": "javbeacon-watchlist-library-16"},
                ]},
                {"items": [{"plugin_id": "stash.metadata", "global_configs": {"connection": {"stash_saved_filter_prefix": "Stash | "}}}]},
                {"items": [{"id": "profile"}]},
                {"items": [{"content_id": "movie:one", "title": "ATID705"}]},
                {"provider_ids": {"stash": "42"}},
                {"items": [{"media_item_id": "movie:one"}], "page": {"has_more": False}},
            ]
            result = plugin._sync_silo_watchlist_collection({}, settings, self.hook)
        self.assertEqual(result["state"], "unchanged")
        self.assertIn("library_id=16", silo_get.call_args_list[4].args[1])
        urlopen.assert_not_called()

    def test_cached_artwork_uses_exact_native_file_identity(self):
        result, writes = self._run(True, False, linked_scene=None)
        self.assertEqual(result["state"], "added")
        self.assertEqual(len(writes), 1)

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

class MultipleLibraryTests(unittest.TestCase):
    @patch.object(plugin, "_silo_get")
    def test_blank_selection_discovers_enabled_movie_libraries(self, silo_get):
        silo_get.return_value = {"items": [
            {"id": "16", "type": "movies", "enabled": True},
            {"id": "19", "type": "movies", "enabled": True},
            {"id": "3", "type": "shows", "enabled": True},
            {"id": "21", "type": "mixed", "enabled": True},
            {"id": "20", "type": "movies", "enabled": False},
        ]}
        self.assertEqual(plugin._silo_movie_libraries({}), ["16", "19", "21"])

    @patch.object(plugin, "_enrich_scene", return_value={"state": "preview", "scene_id": "42"})
    @patch.object(plugin, "_silo_get")
    def test_migration_resumes_in_second_library(self, silo_get, enrich):
        def get(_settings, path, profile_id=None):
            if path == "/api/v2/libraries":
                return {"items": [{"id": "16", "type": "movies"}, {"id": "19", "type": "movies"}]}
            if path == "/api/v2/profiles":
                return {"items": [{"id": "profile"}]}
            if "library_id=16" in path:
                return {"items": [{"content_id": "one", "provider_ids": {"stash": "42"}}], "page": {"has_more": False}}
            if "library_id=19" in path:
                return {"items": [{"content_id": "two", "provider_ids": {"stash": "42"}}], "page": {"has_more": False}}
            raise AssertionError(path)
        silo_get.side_effect = get
        settings = {"max_scenes_per_run": 1}
        first = plugin._import_silo({}, settings, dry_run=True)
        self.assertEqual((first["next_library_id"], first["next_index"]), ("19", 0))
        second = plugin._import_silo({}, settings, dry_run=True, start_library_id=first["next_library_id"], cursor=first["next_cursor"], start_index=first["next_index"])
        self.assertEqual(second["next_library_id"], None)
        self.assertEqual(enrich.call_count, 2)

    @patch.object(plugin, "_silo_post", return_value={"id": "job"})
    @patch.object(plugin, "_silo_get")
    @patch.object(plugin, "_scene", return_value={"id": "42", "code": "ATID-705", "files": []})
    def test_targeted_refresh_visits_each_selected_library(self, scene, silo_get, silo_post):
        def get(_settings, path, profile_id=None):
            if path == "/api/v2/profiles":
                return {"items": [{"id": "profile"}]}
            if "library_id=16" in path:
                return {"items": [{"content_id": "one", "code": "ATID705"}]}
            if "library_id=19" in path:
                return {"items": [{"content_id": "two", "code": "ATID705"}]}
            if path.startswith("/api/v2/catalog/items/"):
                return {"provider_ids": {"stash": "42"}}
            raise AssertionError(path)
        silo_get.side_effect = get
        result = plugin._refresh_silo_scene({}, {"silo_url": "http://silo", "silo_api_key": "key", "silo_library_id": "16,19"}, "42")
        self.assertEqual(result["state"], "queued")
        self.assertEqual({item["library_id"] for item in result["items"]}, {"16", "19"})
        self.assertEqual(silo_post.call_count, 2)

class MultiLibraryWatchListTests(unittest.TestCase):
    @patch.object(plugin, "_sync_silo_watchlist_collection_one")
    @patch.object(plugin, "_silo_get")
    @patch.object(plugin, "_scene")
    def test_stash_tag_updates_existing_collections_in_two_libraries(self, scene, silo_get, sync_one):
        scene.return_value = {"id": "42", "tags": [{"id": "1355"}]}
        collections = {"items": [
            {"id": "7", "title": "Stash | Watchlist", "slug": "javbeacon-stash-preset-3-library-test", "library_id": "16", "collection_type": "manual"},
            {"id": "8", "title": "Stash | Watchlist", "slug": "javbeacon-stash-preset-3-library-test", "library_id": "19", "collection_type": "manual"},
        ]}
        silo_get.side_effect = lambda settings, path, profile_id=None: ({"items": [{"plugin_id": "stash.metadata", "global_configs": {"connection": {"stash_saved_filter_prefix": "Stash | "}}}]} if path.endswith("installations") else collections)
        sync_one.side_effect = lambda settings, row, scene_id, desired, collection: {"state": "added", "collection_id": collection["id"]}
        settings = {"silo_url": "http://silo", "silo_api_key": "key", "watchlist_tag_id": "1355", "silo_library_id": "16,19"}
        hook = {"id": "42", "type": "Scene.Update.Post", "inputFields": ["tag_ids"]}
        result = plugin._sync_silo_watchlist_collection({}, settings, hook)
        self.assertEqual(result["state"], "multiple")
        self.assertEqual({entry["collection_id"] for entry in result["results"]}, {"7", "8"})
        self.assertEqual(sync_one.call_count, 2)

class ProtectedWatchListHookTests(unittest.TestCase):
    @patch.object(plugin, "_sync_silo_watchlist_collection_one")
    @patch.object(plugin, "_silo_post", return_value={"status": "queued"})
    @patch.object(plugin, "_silo_get")
    @patch.object(plugin, "_scene", return_value={"id": "42", "tags": [{"id": "1355"}]})
    def test_journaled_collection_uses_protected_go_reconciler(self, scene, silo_get, silo_post, sync_one):
        installs = {"items": [{"id": "15", "plugin_id": "stash.metadata", "global_configs": {"connection": {"stash_saved_filter_prefix": "Stash | "}}}]}
        collections = {"items": [{"id": "watch", "title": "Stash | Watchlist", "slug": "javbeacon-stash-preset-7-library-16", "library_id": "16", "source_config": {"stash_watchlist_outbox": {"version": 1}}}]}
        silo_get.side_effect = lambda settings, path, profile_id=None: installs if path.endswith("installations") else collections
        settings = {"silo_url": "http://silo", "silo_api_key": "key", "watchlist_tag_id": "1355", "silo_library_id": "16"}
        result = plugin._sync_silo_watchlist_collection({}, settings, {"id": "42", "type": "Scene.Update.Post", "inputFields": ["tag_ids"]})
        self.assertEqual(result["state"], "protected_reconcile")
        sync_one.assert_not_called()
        self.assertEqual(silo_post.call_args.args[1], "/api/v2/plugin-content/plugins/15/recommendations/watchlist/reconcile")
