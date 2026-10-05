import unittest
from unittest.mock import patch
import stash_silo_companion as companion

class RecommendationNotifications(unittest.TestCase):
    def test_notification_never_runs_paid_ranking(self):
        settings={"silo_url":"https://silo.example","silo_api_key":"secret"}
        with patch.object(companion,"_silo_get",return_value={"items":[{"id":"15","plugin_id":"stash.metadata","enabled":True}]}), patch.object(companion,"_silo_post",return_value={"status":"updated"}) as post:
            companion._notify_silo_recommendations({},settings,{"id":"42"})
            self.assertEqual(post.call_args.args[1],"/api/v2/plugin-content/plugins/15/recommendations/dirty")
            self.assertNotIn("run",post.call_args.args[1])
    def test_old_plugin_is_nonblocking(self):
        settings={"silo_url":"https://silo.example","silo_api_key":"secret"}
        with patch.object(companion,"_silo_get",return_value={"items":[{"id":"15","plugin_id":"stash.metadata"}]}), patch.object(companion,"_silo_post",side_effect=RuntimeError("404")):
            result=companion._notify_silo_recommendations({},settings,{})
            self.assertEqual(result["state"],"deferred_to_weekly_snapshot")
    def test_performer_hook_does_not_write_or_enrich_scene(self):
        import io, json
        payload={"args":{"mode":"hook","hookContext":{"id":"42","type":"Performer.Update.Post"}}}
        with patch.object(companion.sys,"stdin",io.StringIO(json.dumps(payload))), patch.object(companion,"_settings",return_value={"silo_url":"https://silo.example","silo_api_key":"secret"}), patch.object(companion,"_notify_silo_recommendations") as notify, patch.object(companion,"_enrich_scene") as enrich, patch.object(companion,"_sync_silo_watchlist_collection") as watchlist:
            result=companion.main()
            notify.assert_called_once()
            enrich.assert_not_called()
            watchlist.assert_not_called()
            self.assertIn("recommendations",result["output"])
