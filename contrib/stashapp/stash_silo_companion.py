#!/usr/bin/env python3
"""Fill missing Stash scene fields from exactly linked JAVBeacon releases."""

from datetime import datetime, timezone
import base64
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

import stash_silo_features as features


PLUGIN_ID = "stash-silo-companion"
SCENE_QUERY = """
query StashSiloScene($id: ID!) {
  findScene(id: $id) {
    id title code details director date urls
    studio { id name }
    performers { id name }
    tags { id name }
    paths { screenshot }
    files { path }
  }
}
"""
SCENES_QUERY = """
query StashSiloScenes($filter: FindFilterType) {
  findScenes(filter: $filter) { count scenes { id } }
}
"""
UPDATE_QUERY = """
mutation StashSiloUpdate($input: SceneUpdateInput!) {
  sceneUpdate(input: $input) { id }
}
"""


def _log(message):
    print("[Stash.Silo Companion] " + message, file=sys.stderr, flush=True)


def _json_response(response):
    raw = response.read()
    return json.loads(raw) if raw else {}


def _stash_endpoint(payload):
    connection = payload.get("server_connection") or {}
    scheme = str(connection.get("Scheme") or "http")
    host = str(connection.get("Host") or "127.0.0.1")
    if host in ("0.0.0.0", "::"):
        host = "127.0.0.1"
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"
    port = int(connection.get("Port") or 9999)
    headers = {"Content-Type": "application/json"}
    cookie = connection.get("SessionCookie") or {}
    if cookie.get("Name") and cookie.get("Value"):
        headers["Cookie"] = f"{cookie['Name']}={cookie['Value']}"
    return f"{scheme}://{host}:{port}/graphql", headers


def _stash_graphql(payload, query, variables=None):
    endpoint, headers = _stash_endpoint(payload)
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    request = urllib.request.Request(endpoint, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = _json_response(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Stash GraphQL returned HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"could not reach Stash: {error.reason}") from error
    if result.get("errors"):
        message = "; ".join(str(x.get("message") or x) for x in result["errors"])
        raise RuntimeError("Stash GraphQL: " + message)
    return result.get("data") or {}


def _settings(payload):
    query = 'query { configuration { plugins(include: ["stash-silo-companion"]) } }'
    configs = (_stash_graphql(payload, query).get("configuration") or {}).get("plugins") or {}
    return configs.get(PLUGIN_ID) or {}


def _bool(value, default=False):
    if value is None or value == "":
        return default
    if isinstance(value, str):
        if value.strip().lower() in ("true", "yes", "1", "on"):
            return True
        if value.strip().lower() in ("false", "no", "0", "off"):
            return False
        raise RuntimeError("invalid Boolean setting")
    return bool(value)


def _backend(settings, path, *, binary=False):
    base = str(settings.get("javbeacon_url") or "").strip().rstrip("/")
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password:
        raise RuntimeError("configure a valid JAVBeacon URL in Settings > Plugins")
    key = str(settings.get("javbeacon_api_key") or "").strip()
    if not key:
        raise RuntimeError("configure a JAVBeacon API key in Settings > Plugins")
    if not path.startswith("/") or path.startswith("//"):
        raise RuntimeError("invalid JAVBeacon resource path")
    timeout = max(1, int(settings.get("timeout_seconds") or 15))
    request = urllib.request.Request(base + path, headers={"Authorization": "Bearer " + key})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            if binary:
                content_type = response.headers.get("Content-Type", "").split(";", 1)[0]
                if content_type not in ("image/jpeg", "image/png", "image/webp"):
                    raise RuntimeError("JAVBeacon poster is not a supported image")
                image = response.read(8 * 1024 * 1024 + 1)
                if len(image) > 8 * 1024 * 1024:
                    raise RuntimeError("JAVBeacon poster exceeds 8 MiB")
                return content_type, image
            return _json_response(response)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None
        raise RuntimeError(f"JAVBeacon returned HTTP {error.code} for {path}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"could not reach JAVBeacon: {error.reason}") from error


def _enrichment(settings, scene_id):
    path = "/api/v1/integrations/stash/enrichment/" + urllib.parse.quote(str(scene_id), safe="")
    data = _backend(settings, path)
    if data and str(data.get("scene_id")) != str(scene_id):
        raise RuntimeError("JAVBeacon returned a different Stash scene ID")
    return data


def _first_exact(rows, name):
    matches = [item for item in rows if str(item.get("name") or "").strip().casefold() == name.casefold()]
    return matches[0]["id"] if len(matches) == 1 else None


def _entity_id(payload, kind, name, create):
    name = str(name or "").strip()
    if not name:
        return None
    spec = {
        "studio": ("findStudios", "studios", "studioCreate", "StudioCreateInput"),
        "performer": ("findPerformers", "performers", "performerCreate", "PerformerCreateInput"),
        "tag": ("findTags", "tags", "tagCreate", "TagCreateInput"),
    }[kind]
    field, collection, mutation, input_type = spec
    query = f"query($filter: FindFilterType) {{ {field}(filter: $filter) {{ {collection} {{ id name }} }} }}"
    data = _stash_graphql(payload, query, {"filter": {"q": name, "per_page": 50}})
    rows = (data.get(field) or {}).get(collection) or []
    found = _first_exact(rows, name)
    if found or not create:
        return found
    mutation_query = f"mutation($input: {input_type}!) {{ {mutation}(input: $input) {{ id }} }}"
    try:
        created = _stash_graphql(payload, mutation_query, {"input": {"name": name}})
    except RuntimeError:
        # Another plugin may have created the same entity concurrently.
        data = _stash_graphql(payload, query, {"filter": {"q": name, "per_page": 50}})
        rows = (data.get(field) or {}).get(collection) or []
        found = _first_exact(rows, name)
        if found:
            return found
        raise
    return (created.get(mutation) or {}).get("id")


def _blank(value):
    return value is None or isinstance(value, str) and not value.strip()


def _plan(scene, source, *, cover_mode="off"):
    """Return only fields absent from Stash. This pure step is safe to preview."""
    update = {"id": str(scene["id"])}
    for key in ("title", "code", "details", "director", "date"):
        value = source.get(key)
        if _blank(scene.get(key)) and isinstance(value, str) and value.strip():
            update[key] = value.strip()
    source_url = str(source.get("source_url") or "").strip()
    if source_url and not (scene.get("urls") or []):
        update["urls"] = [source_url]
    if not scene.get("studio") and source.get("studio"):
        update["_studio_name"] = str(source["studio"]).strip()
    if not (scene.get("performers") or []) and source.get("performers"):
        update["_performer_names"] = [str(x).strip() for x in source["performers"] if str(x).strip()]
    if not (scene.get("tags") or []) and source.get("tags"):
        update["_tag_names"] = [str(x).strip() for x in source["tags"] if str(x).strip()]
    if cover_mode == "missing" and not (scene.get("paths") or {}).get("screenshot"):
        if source.get("poster_path"):
            update["_poster_path"] = source["poster_path"]
    return update


def _resolve_plan(payload, settings, plan, *, dry_run=False):
    result = dict(plan)
    create = _bool(settings.get("create_entities"), False) and not dry_run
    if "_studio_name" in result:
        name = result.pop("_studio_name")
        if not dry_run:
            found = _entity_id(payload, "studio", name, create)
            if found:
                result["studio_id"] = found
        else:
            result["studio_name"] = name
    for field, kind, target in (("_performer_names", "performer", "performer_ids"), ("_tag_names", "tag", "tag_ids")):
        names = result.pop(field, [])
        if dry_run and names:
            result[kind + "_names"] = names
        elif names:
            ids = [_entity_id(payload, kind, name, create) for name in names]
            ids = list(dict.fromkeys(str(x) for x in ids if x))
            if ids:
                result[target] = ids
    poster_path = result.pop("_poster_path", None)
    if poster_path:
        if dry_run:
            result["poster_path"] = poster_path
        else:
            poster = _backend(settings, poster_path, binary=True)
            if poster is None:
                raise RuntimeError("JAVBeacon poster was not found")
            content_type, image = poster
            result["cover_image"] = "data:" + content_type + ";base64," + base64.b64encode(image).decode("ascii")
    return result


def _scene(payload, scene_id):
    return _stash_graphql(payload, SCENE_QUERY, {"id": str(scene_id)}).get("findScene")


def _enrich_scene(payload, settings, scene_id, *, dry_run, allow_cover=True, source=None):
    scene = _scene(payload, scene_id)
    if not scene:
        return {"scene_id": str(scene_id), "state": "missing_scene"}
    if source is None:
        source = _enrichment(settings, scene_id)
    if source is None:
        return {"scene_id": str(scene_id), "state": "unlinked"}
    cover_mode = str(settings.get("cover_mode") or "off").strip().lower()
    if cover_mode not in ("off", "missing"):
        raise RuntimeError("cover_mode must be off or missing")
    plan = _plan(scene, source, cover_mode=cover_mode if allow_cover else "off")
    if len(plan) == 1:
        return {"scene_id": str(scene_id), "state": "unchanged"}
    resolved = _resolve_plan(payload, settings, plan, dry_run=dry_run)
    if len(resolved) == 1:
        return {"scene_id": str(scene_id), "state": "unresolved_entities"}
    fields = [key for key in resolved if key not in ("id", "cover_image")]
    if "cover_image" in resolved:
        fields.append("cover_image")
    if not dry_run:
        # Older Stash releases cleared entity relations when omitted from a
        # sceneUpdate mutation. Include the currently assigned IDs as guards.
        if "studio_id" not in resolved and scene.get("studio"):
            resolved["studio_id"] = scene["studio"]["id"]
        for collection, field in (("performers", "performer_ids"), ("tags", "tag_ids")):
            if field not in resolved:
                resolved[field] = [item["id"] for item in scene.get(collection) or []]
        updated = _stash_graphql(payload, UPDATE_QUERY, {"input": resolved}).get("sceneUpdate")
        if not updated or str(updated.get("id")) != str(scene_id):
            raise RuntimeError("Stash did not confirm scene update")
    return {"scene_id": str(scene_id), "state": "preview" if dry_run else "updated", "fields": fields}


def _scan(payload, settings, *, dry_run, start_page=1, start_index=0):
    limit = int(settings.get("max_scenes_per_run") if settings.get("max_scenes_per_run") not in (None, "") else 200)
    if limit < 0:
        raise RuntimeError("max_scenes_per_run must be nonnegative")
    page = max(1, int(start_page))
    index = max(0, int(start_index))
    if index >= 100:
        raise RuntimeError("start_index must be less than 100")
    checked = 0
    counts = {}
    examples = []
    while True:
        data = _stash_graphql(payload, SCENES_QUERY, {"filter": {"page": page, "per_page": 100, "sort": "created_at", "direction": "DESC"}})
        scenes = (data.get("findScenes") or {}).get("scenes") or []
        if not scenes:
            break
        for offset, item in enumerate(scenes[index:], start=index):
            if limit and checked >= limit:
                return {"mode": "preview" if dry_run else "enrich", "checked": checked, "counts": counts, "examples": examples, "next_page": page, "next_index": offset}
            result = _enrich_scene(payload, settings, item["id"], dry_run=dry_run)
            checked += 1
            state = result["state"]
            counts[state] = counts.get(state, 0) + 1
            if state in ("preview", "updated") and len(examples) < 20:
                examples.append(result)
        if len(scenes) < 100:
            break
        page += 1
        index = 0
    return {"mode": "preview" if dry_run else "enrich", "checked": checked, "counts": counts, "examples": examples, "next_page": None, "next_index": None}



def _silo_get(settings, path, profile_id=None):
    base = str(settings.get("silo_url") or "").strip().rstrip("/")
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password:
        raise RuntimeError("configure a valid Silo URL")
    key = str(settings.get("silo_api_key") or "").strip()
    if not key:
        raise RuntimeError("configure a Silo API key")
    headers = {"Accept": "application/json", "Authorization": "Bearer " + key}
    if profile_id:
        headers["X-Profile-Id"] = profile_id
    request = urllib.request.Request(base + path, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return _json_response(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Silo returned HTTP {error.code}") from error


def _silo_scene_id(item):
    for field in ("provider_ids", "external_ids"):
        value = item.get(field) or {}
        if isinstance(value, dict):
            raw = str(value.get("stash") or "").strip().removeprefix("stash:")
            if raw.isdigit():
                return raw
    for field in ("poster_url", "backdrop_url"):
        path = urllib.parse.urlparse(str(item.get(field) or "")).path
        matched = re.fullmatch(r"/api/v1/integrations/silo/stash/scenes/([0-9]+)/(?:cover|poster)", path)
        if matched:
            return matched.group(1)
    return None


def _silo_unique_scene(payload, item):
    title = str(item.get("code") or item.get("title") or "").strip()
    key = re.sub(r"[^a-z0-9]", "", title.casefold())
    if not key:
        return None
    query = "query($filter:FindFilterType) { findScenes(filter:$filter) { scenes { id code title files { path } } } }"
    data = _stash_graphql(payload, query, {"filter": {"q": title, "per_page": 50}})
    rows = (data.get("findScenes") or {}).get("scenes") or []
    matches = []
    for scene in rows:
        values = [scene.get("code"), scene.get("title")]
        values.extend(os.path.splitext(os.path.basename(file.get("path") or ""))[0] for file in scene.get("files") or [])
        if any(re.sub(r"[^a-z0-9]", "", str(value or "").casefold()) == key for value in values):
            matches.append(str(scene["id"]))
    return matches[0] if len(set(matches)) == 1 else None


def _silo_source(item):
    def names(values):
        result = []
        for value in values or []:
            name = value.get("name") if isinstance(value, dict) else value
            if name is not None and str(name).strip():
                result.append(str(name).strip())
        return result
    studios = names(item.get("studios"))
    studio = item.get("studio") or (studios[0] if studios else "")
    cast = names(item.get("cast") or item.get("people") or item.get("performers"))
    return {"title": item.get("title") or "", "code": item.get("code") or "",
            "details": item.get("overview") or item.get("details") or "",
            "date": item.get("release_date") or item.get("date") or "",
            "studio": studio if isinstance(studio, str) else studio.get("name", ""),
            "performers": cast, "tags": names(item.get("genres") or item.get("tags"))}


def _silo_movie_libraries(settings):
    """Resolve one or more configured IDs, or discover every enabled movie library."""
    raw = str(settings.get("silo_library_id") or "").strip()
    requested = [value.strip() for value in re.split(r"[,;\s]+", raw) if value.strip()]
    if requested:
        return list(dict.fromkeys(requested))
    libraries = _silo_get(settings, "/api/v2/libraries").get("items") or []
    available = {str(row.get("id")): row for row in libraries
                 if str(row.get("type") or "").lower() in ("movies", "mixed") and row.get("enabled", True)}
    if not available:
        raise RuntimeError("Silo returned no enabled movie libraries")
    return list(available)


def _import_silo(payload, settings, *, dry_run, cursor="", start_index=0, start_library_id=""):
    libraries = _silo_movie_libraries(settings)
    if start_library_id:
        if start_library_id not in libraries:
            raise RuntimeError("start_library_id is not an enabled selected movie library")
        libraries = libraries[libraries.index(start_library_id):]
    profiles = _silo_get(settings, "/api/v2/profiles").get("items") or []
    if not profiles:
        raise RuntimeError("Silo returned no profiles")
    profile_id = str(profiles[0]["id"])
    limit = int(settings.get("max_scenes_per_run") if settings.get("max_scenes_per_run") not in (None, "") else 200)
    if limit < 0:
        raise RuntimeError("max_scenes_per_run must be nonnegative")
    counts = {}
    examples = []
    checked = 0
    index = max(0, int(start_index))
    if index >= 200:
        raise RuntimeError("start_index must be less than 200")
    for library_id in libraries:
        while True:
            params = {"library_id": library_id, "limit": "200", "skip_total": "true", "status": "matched", "sort": "-added_at"}
            if cursor:
                params["cursor"] = cursor
            page = _silo_get(settings, "/api/v2/catalog?" + urllib.parse.urlencode(params), profile_id)
            rows = page.get("items") or []
            for offset, row in enumerate(rows[index:], start=index):
                if limit and checked >= limit:
                    return {"mode": "silo_preview" if dry_run else "silo_import", "checked": checked, "counts": counts, "examples": examples, "next_library_id": library_id, "next_cursor": cursor, "next_index": offset}
                checked += 1
                scene_id = _silo_scene_id(row)
                detail = None
                if not scene_id:
                    content_id = str(row.get("content_id") or "")
                    if content_id:
                        detail = _silo_get(settings, "/api/v2/catalog/items/" + urllib.parse.quote(content_id, safe=""), profile_id)
                        if str(detail.get("content_id")) == content_id:
                            scene_id = _silo_scene_id(detail)
                if not scene_id:
                    scene_id = _silo_unique_scene(payload, {**row, **(detail or {})})
                if not scene_id:
                    state = "no_unique_scene_match"
                    result = None
                else:
                    source = _silo_source({**row, **(detail or {})})
                    result = _enrich_scene(payload, settings, scene_id, dry_run=dry_run, allow_cover=False, source=source)
                    state = result["state"]
                counts[state] = counts.get(state, 0) + 1
                if result and state in ("preview", "updated") and len(examples) < 20:
                    examples.append({"library_id": library_id, **result})
            next_cursor = str((page.get("page") or {}).get("next_cursor") or "")
            if not (page.get("page") or {}).get("has_more") or not next_cursor or next_cursor == cursor:
                break
            cursor = next_cursor
            index = 0
        cursor = ""
        index = 0
    return {"mode": "silo_preview" if dry_run else "silo_import", "checked": checked, "counts": counts, "examples": examples, "next_library_id": None, "next_cursor": None, "next_index": None}


def _silo_post(settings, path, body):
    base = str(settings.get("silo_url") or "").strip().rstrip("/")
    key = str(settings.get("silo_api_key") or "").strip()
    data = json.dumps(body).encode()
    request = urllib.request.Request(base + path, data=data,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            if response.status != 202:
                raise RuntimeError(f"Silo refresh returned HTTP {response.status}")
            return _json_response(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Silo refresh returned HTTP {error.code}") from error


def _notify_silo_recommendations(payload, settings, hook):
    """Mark recommendations stale; never trigger paid ranking from an edit hook."""
    if not settings.get("silo_url") or not settings.get("silo_api_key"):
        return {"state": "not_configured"}
    installs = _silo_get(settings, "/api/v2/admin/plugins/installations").get("items", [])
    matches = [x for x in installs if x.get("plugin_id") == "stash.metadata" and x.get("enabled", True)]
    if len(matches) != 1:
        return {"state": "ambiguous_or_missing_plugin"}
    try:
        return _silo_post(settings, "/api/v2/plugin-content/plugins/" + str(matches[0]["id"]) + "/recommendations/dirty", {"scene_id": str(hook.get("id") or "")})
    except Exception:
        # Older Silo plugin versions have no route. The weekly full snapshot
        # remains authoritative, so notification failures cannot break hooks.
        return {"state": "deferred_to_weekly_snapshot"}


def _sync_silo_watchlist_collection(payload, settings, hook):
    # Stash includes the full input on every update, but inputFields records
    # which fields were actually edited. Playback and enrichment hooks are ignored.
    if hook.get("type") != "Scene.Update.Post" or "tag_ids" not in (hook.get("inputFields") or []):
        return {"state": "not_tag_update"}
    if not all(settings.get(key) for key in ("silo_url", "silo_api_key", "watchlist_tag_id")):
        return {"state": "disabled"}
    scene_id = str(hook.get("id") or (hook.get("input") or {}).get("id") or "")
    scene = _scene(payload, scene_id)
    if not scene:
        return {"state": "missing_scene"}
    desired = hook.get("desired", any(str(tag.get("id")) == str(settings["watchlist_tag_id"]) for tag in scene.get("tags") or []))
    changed_at = hook.get("changed_at") or datetime.now(timezone.utc).isoformat()
    allowed = set(_silo_movie_libraries(settings))
    collections = _silo_get(settings, "/api/v2/admin/collections").get("items") or []
    # Use the same prefix as the selected Stash saved-filter importer. Never
    # maintain a separate unprefixed legacy WatchList collection.
    installations = _silo_get(settings, "/api/v2/admin/plugins/installations").get("items") or []
    installation = next((row for row in installations if row.get("plugin_id") == "stash.metadata"), None)
    if not installation:
        return {"state": "missing_stash_plugin"}
    configs = installation.get("global_configs") or {}
    if isinstance(configs, list):
        configs = {row["key"]: row.get("value") for row in configs}
    connection = configs.get("connection") or {}
    if isinstance(connection, str):
        connection = json.loads(connection)
    title = str(connection.get("stash_saved_filter_prefix") or "") + "Watchlist"
    candidates = [row for row in collections if str(row.get("library_id")) in allowed
                  and str(row.get("title") or "").casefold() == title.casefold()
                  and str(row.get("slug") or "").startswith("javbeacon-stash-preset-")]
    selected = []
    for library_id in allowed:
        matches = [row for row in candidates if str(row.get("library_id")) == library_id]
        if len(matches) > 1:
            return {"state": "ambiguous_collection", "library_id": library_id}
        selected.extend(matches)
    if not selected:
        return {"state": "missing_collection"}
    # The Go plugin owns the durable outbound journal. Let its protected
    # reconciler apply incoming tag changes so a late hook cannot overwrite a
    # user's concurrent local choice or create an add/remove feedback loop.
    if any((row.get("source_config") or {}).get("stash_watchlist_outbox") for row in selected):
        installs = _silo_get(settings, "/api/v2/admin/plugins/installations").get("items", [])
        plugins = [row for row in installs if row.get("plugin_id") == "stash.metadata" and row.get("enabled", True)]
        if len(plugins) != 1:
            return {"state": "watchlist_reconciler_unavailable"}
        result = _silo_post(settings, "/api/v2/plugin-content/plugins/" + str(plugins[0]["id"]) + "/recommendations/watchlist/reconcile", {"scene_id": scene_id, "desired": desired, "changed_at": changed_at, "paths": [f["path"] for f in scene.get("files") or [] if f.get("path")]})
        return {"state": "protected_reconcile", "result": result}
    results = [_sync_silo_watchlist_collection_one(settings, scene, scene_id, desired, collection) for collection in selected]
    return results[0] if len(results) == 1 else {"state": "multiple", "results": results}


def _sync_silo_watchlist_collection_one(settings, scene, scene_id, desired, collection):
    library_id = str(collection.get("library_id") or "")
    if not library_id:
        return {"state": "collection_without_library"}
    if str(collection.get("collection_type") or "manual") != "manual":
        return {"state": "nonmanual_collection"}
    terms = [scene.get("code"), scene.get("title")]
    terms.extend(os.path.splitext(os.path.basename(file.get("path") or ""))[0] for file in scene.get("files") or [])
    terms = [str(term).strip() for term in terms if str(term or "").strip()]
    if not terms:
        return {"state": "no_identity"}
    profiles = _silo_get(settings, "/api/v2/profiles").get("items") or []
    if not profiles:
        raise RuntimeError("Silo returned no profiles")
    profile_id = str(profiles[0]["id"])
    content_ids = set()
    for term in dict.fromkeys(terms[:3]):
        params = {"library_id": library_id, "q": term, "limit": "50", "status": "matched"}
        page = _silo_get(settings, "/api/v2/catalog?" + urllib.parse.urlencode(params), profile_id)
        key = re.sub(r"[^a-z0-9]", "", term.casefold())
        for item in page.get("items") or []:
            values = [item.get("title"), item.get("code")]
            if key and any(re.sub(r"[^a-z0-9]", "", str(value or "").casefold()) == key for value in values) and item.get("content_id"):
                content_ids.add(str(item["content_id"]))
    if len(content_ids) != 1:
        return {"state": "ambiguous_item" if content_ids else "unmatched_item"}
    content_id = next(iter(content_ids))
    detail = _silo_get(settings, "/api/v2/catalog/items/" + urllib.parse.quote(content_id, safe=""), profile_id)
    linked_scene = _silo_scene_id(detail)
    if linked_scene and linked_scene != scene_id:
        return {"state": "different_scene"}
    if not linked_scene:
        # Cached Silo artwork no longer carries the Stash scene ID. Verify all
        # native files instead of using a title as the final identity proof.
        stash_paths = {str(row.get("path") or "") for row in scene.get("files") or []}
        native_paths = set()
        cursor = ""
        while True:
            path = "/api/v2/admin/items/" + urllib.parse.quote(content_id, safe="") + "/files?limit=200"
            if cursor:
                path += "&cursor=" + urllib.parse.quote(cursor, safe="")
            page = _silo_get(settings, path)
            native_paths.update(str(row.get("file_path") or "") for row in page.get("items") or [])
            if not (page.get("page") or {}).get("has_more"):
                break
            next_cursor = str((page.get("page") or {}).get("next_cursor") or "")
            if not next_cursor or next_cursor == cursor:
                return {"state": "unverified_scene"}
            cursor = next_cursor
        if not native_paths or "" in native_paths or not native_paths.issubset(stash_paths):
            return {"state": "unverified_scene"}
    collection_id = str(collection["id"])
    prefix = "/api/v2/admin/collections/" + urllib.parse.quote(collection_id, safe="") + "/items"
    cursor = ""
    for _ in range(100):
        path = prefix + "?limit=200" + ("&cursor=" + urllib.parse.quote(cursor, safe="") if cursor else "")
        page = _silo_get(settings, path)
        if any(str(row.get("media_item_id")) == content_id for row in page.get("items") or []):
            current = True
            break
        next_cursor = str((page.get("page") or {}).get("next_cursor") or "")
        if not (page.get("page") or {}).get("has_more"):
            current = False
            break
        if not next_cursor or next_cursor == cursor:
            raise RuntimeError("Silo collection pagination did not advance")
        cursor = next_cursor
    else:
        raise RuntimeError("Silo collection has too many membership pages")
    if current == desired:
        return {"state": "unchanged", "content_id": content_id, "collection_id": collection_id}
    path = prefix + "/" + urllib.parse.quote(content_id, safe="")
    base = str(settings["silo_url"]).strip().rstrip("/")
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password:
        raise RuntimeError("configure a valid Silo URL")
    headers = {"Authorization": "Bearer " + str(settings["silo_api_key"]).strip()}
    if desired:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=b'{"position":0}' if desired else None,
                                     headers=headers, method="PUT" if desired else "DELETE")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            if response.status != 204:
                raise RuntimeError(f"Silo collection update returned HTTP {response.status}")
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Silo collection update returned HTTP {error.code}") from error
    return {"state": "added" if desired else "removed", "content_id": content_id, "collection_id": collection_id}


def _refresh_silo_scene(payload, settings, scene_id):
    if not all(settings.get(key) for key in ("silo_url", "silo_api_key")):
        return {"state": "disabled"}
    scene = _scene(payload, scene_id)
    if not scene:
        return {"state": "missing_scene"}
    terms = [scene.get("code"), scene.get("title")]
    terms.extend(os.path.splitext(os.path.basename(file.get("path") or ""))[0] for file in scene.get("files") or [])
    terms = [str(x).strip() for x in terms if str(x or "").strip()]
    if not terms:
        return {"state": "no_identity"}
    libraries = _silo_movie_libraries(settings)
    profiles = _silo_get(settings, "/api/v2/profiles").get("items") or []
    if not profiles:
        raise RuntimeError("Silo returned no profiles")
    profile_id = str(profiles[0]["id"])
    queued = []
    ambiguous = []
    for library_id in libraries:
        content_ids = set()
        for term in dict.fromkeys(terms[:3]):
            params = {"library_id": library_id, "q": term, "limit": "50", "status": "matched"}
            page = _silo_get(settings, "/api/v2/catalog?" + urllib.parse.urlencode(params), profile_id)
            key = re.sub(r"[^a-z0-9]", "", term.casefold())
            for item in page.get("items") or []:
                values = [item.get("title"), item.get("code")]
                if key and any(re.sub(r"[^a-z0-9]", "", str(value or "").casefold()) == key for value in values) and item.get("content_id"):
                    content_ids.add(str(item["content_id"]))
        if len(content_ids) > 1:
            ambiguous.append(library_id)
            continue
        if not content_ids:
            continue
        content_id = next(iter(content_ids))
        detail = _silo_get(settings, "/api/v2/catalog/items/" + urllib.parse.quote(content_id, safe=""), profile_id)
        linked_scene = _silo_scene_id(detail)
        if linked_scene and linked_scene != str(scene_id):
            continue
        job = _silo_post(settings, "/api/v2/admin/items/" + urllib.parse.quote(content_id, safe="") + "/refresh-metadata", {"mode": "complete"})
        queued.append({"library_id": library_id, "content_id": content_id, "job_id": job.get("id")})
    if queued:
        return {"state": "queued", "items": queued, "ambiguous_libraries": ambiguous}
    return {"state": "ambiguous" if ambiguous else "unmatched", "ambiguous_libraries": ambiguous}


def main():
    payload = json.load(sys.stdin)
    args = payload.get("args") or {}
    mode = str(args.get("mode") or "hook").lower()
    if mode == "silo_import":
        result = _import_silo(payload, _settings(payload), dry_run=_bool(args.get("dry_run"), True), cursor=str(args.get("start_cursor") or ""), start_index=args.get("start_index") or 0, start_library_id=str(args.get("start_library_id") or ""))
    elif mode == "watchlist_sync":
        scene_id = str(args.get("scene_id") or "")
        result = _sync_silo_watchlist_collection(payload, _settings(payload), {"id": scene_id, "type": "Scene.Update.Post", "inputFields": ["tag_ids"], **{key: args[key] for key in ("desired", "changed_at") if key in args}})
    elif mode == "scan":
        result = _scan(payload, _settings(payload), dry_run=_bool(args.get("dry_run"), True), start_page=args.get("start_page") or 1, start_index=args.get("start_index") or 0)
    elif mode == "subtitles":
        result = features.request_subtitles(payload, args)
    elif mode == "subtitle_status":
        result = features.subtitle_status(payload, args)
    elif mode in ("test", "history"):
        result = features.request_realtime_sync(payload, args)
    elif mode == "hook":
        settings = _settings(payload)
        hook = args.get("hookContext") or {}
        edited = set(hook.get("inputFields") or []) - {"id", "ids", "clientMutationId"}
        if hook.get("type") == "Scene.Update.Post" and edited == {"tag_ids"}:
            scene_id = str(hook.get("id") or (hook.get("input") or {}).get("id") or "")
            scene = _scene(payload, scene_id)
            desired = any(str(tag.get("id")) == str(settings.get("watchlist_tag_id")) for tag in (scene or {}).get("tags") or [])
            queued = _stash_graphql(payload, 'mutation($plugin:ID!,$args:Map!){runPluginTask(plugin_id:$plugin,description:"Sync Watchlist change to Silo",args_map:$args)}', {"plugin": PLUGIN_ID, "args": {"mode": "watchlist_sync", "scene_id": scene_id, "desired": desired, "changed_at": datetime.now(timezone.utc).isoformat()}})
            return {"output": {"watchlist_job": queued.get("runPluginTask")}}
        if settings.get("silo_url") and settings.get("silo_api_key"):
            try:
                _notify_silo_recommendations(payload, settings, hook)
            except Exception:
                pass
        if str(hook.get("type") or "").startswith("Performer."):
            # Performer favourites affect the weekly snapshot; never treat a
            # performer ID as a scene ID for enrichment or activity hooks.
            return {"output": {"recommendations": "deferred_to_weekly_snapshot"}}
        scene_id = hook.get("id") or (hook.get("input") or {}).get("id")
        if not scene_id:
            raise RuntimeError("scene hook did not include an ID")
        try:
            silo_collection = _sync_silo_watchlist_collection(payload, settings, hook)
        except Exception as error:
            silo_collection = {"state": "error", "error": str(error)}
            _log("Silo WatchList collection update failed: " + str(error))
        if settings.get("javbeacon_url") and settings.get("webhook_secret"):
            try:
                realtime = features.request_realtime_sync(payload, args)
            except Exception as error:
                realtime = {"state": "error", "error": str(error)}
                _log("realtime webhook failed: " + str(error))
        else:
            realtime = {"state": "disabled"}
        if hook.get("type") == "Scene.Destroy.Post" or not _bool(settings.get("auto_enrich"), True):
            enrichment = {"state": "disabled" if hook.get("type") != "Scene.Destroy.Post" else "destroyed"}
        else:
            enrichment = _enrich_scene(payload, settings, scene_id, dry_run=False, allow_cover=False)
        if hook.get("type") == "Scene.Destroy.Post" or not _bool(settings.get("refresh_silo_on_scene_update"), True):
            silo_refresh = {"state": "disabled"}
        else:
            try:
                silo_refresh = _refresh_silo_scene(payload, settings, scene_id)
            except Exception as error:
                silo_refresh = {"state": "error", "error": str(error)}
                _log("Silo targeted refresh failed: " + str(error))
        result = {"realtime": realtime, "enrichment": enrichment, "silo_refresh": silo_refresh, "silo_collection": silo_collection}
    else:
        raise RuntimeError("unknown task mode")
    if mode != "hook":
        _log(json.dumps(result, ensure_ascii=False))
    return {"output": result}


if __name__ == "__main__":
    try:
        print(json.dumps(main()))
    except Exception as error:
        print(json.dumps({"error": str(error)}))
        sys.exit(1)
