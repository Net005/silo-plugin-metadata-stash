import unittest
from unittest.mock import patch
import stash_metadata as plugin

class SiloImportTests(unittest.TestCase):
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
