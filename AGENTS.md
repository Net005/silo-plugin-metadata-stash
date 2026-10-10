# Working in this repository

This file applies to the entire repository. Follow more specific instructions in a nested `AGENTS.md` when present. Direct user instructions take precedence.

## Privacy comes first

This is a public repository. User messages, screenshots, browser sessions, deployment settings and logs may contain private information; they are context for the task, not material to publish.

- Never put real credentials, API keys, tokens, cookies, private server addresses, LAN IPs, usernames, private filesystem paths or personal library data in tracked files, commit messages, PRs, issues or release notes.
- Use neutral examples such as `https://silo.example.com`, `https://stash.example.com`, `/media/example.mp4` and `YOUR_API_KEY`. Public upstream project URLs and this repository's public URLs are fine.
- Keep connection details in the application's secret settings, environment variables or the user's local userscript settings. Keep the distributed userscript's `@match` generic; private domains belong only in the installed local copy.
- Do not copy screenshots, production responses, database dumps or raw logs into the repository. Build synthetic fixtures that reproduce the relevant behavior without preserving identifying data.
- Read only the settings needed for the task. Do not print configuration files or environment variables wholesale. Redact sensitive fields before any output; encoding a secret does not make it safe.
- Avoid secrets in shell command text, command-line arguments, URLs, tracing and generated scripts. Prefer an existing authenticated session or credential store. If a temporary secret-bearing file is necessary, keep it outside the repository, restrict permissions and remove it promptly.
- Do not include private connection details in progress messages or final summaries. Refer to the service or deployment by a generic name unless the address is necessary for the user's requested result.
- Before committing or publishing, inspect the exact staged diff, including new files, for sensitive data. Check fixtures, comments, documentation, userscript metadata and generated output as well as source code. A successful test run is not a privacy check.
- If private information is found, remove it from the pending changes and check related files. If it has already been published, tell the user what kind of information was exposed without repeating it. Recommend revoking exposed credentials; coordinate history rewriting or other destructive cleanup with the user.

Do not record this user's credentials, infrastructure inventory or deployment access in this file or any other tracked document.

## Fast project map

| Location | Purpose |
| --- | --- |
| `README.md` | Feature overview, installation and common troubleshooting. |
| `docs/INTEGRATION.md` | Detailed sync, recovery, artwork and migration behavior. |
| `RECOMMENDATIONS.md` | Recommendation configuration, reports and spending controls. |
| Root Go sources | Silo metadata, artwork, playback, collections and background workers. |
| `manifest.json` | Silo plugin manifest and release version. |
| `contrib/stashapp/` | Stash companion Python backend, JavaScript UI, CSS and tests. |
| `contrib/stashapp/stash-silo-companion.yml` | Companion identity, version, settings and tasks. |
| `contrib/tampermonkey/` | Silo browser enhancements, userscript version, tests and setup guide. |
| `scripts/update-stash-source.py` | Stash plugin source generation for releases. |
| `.github/workflows/` | Build, test and release automation. |

## Efficient workflow

1. Confirm the repository, branch and working-tree status before editing. Work on `main` unless the user requests another branch. Preserve unrelated work; never reset, discard or overwrite it to simplify the task.
2. Read the relevant code and nearby tests first. Use `rg` for targeted searches and batch independent reads. Prefer a small change over an unrelated refactor.
3. Check current installed versions and deployed code when diagnosing production behavior. Distinguish source changes, committed changes, published artifacts, installed server code and browser-loaded code.
4. Reuse native Silo or Stash components, icons, classes and interactions. Do not add custom visual styling when existing controls provide the requested behavior.
5. Run checks appropriate to the changed component. Add meaningful regression coverage for data mapping, ordering, retries or mutations. Documentation-only changes need link and diff checks rather than the full application suite.
6. Review the final diff and privacy checks. Report what changed, what was verified and what remains unverified. State explicitly whether changes are local, pushed or deployed.

Keep routine updates short. Ask only for missing information or authorization that actually blocks the next step. Do not repeat permission requests for actions the user already authorized.

## Sync and data integrity

- Match scenes and performers through exact provider identity or verified exact paths. Reject ambiguous matches; names alone must not silently select a person or scene.
- Scope collection and file checks to the selected library. Shared items in other libraries must not alter the target library's result.
- Watchlist changes must preserve unrelated tags, retain event ordering and survive background failures. Re-adding an item must move it to the correct newest position.
- Keep interactive tag saves independent of slow Silo scans. Queue backend work and maintain durable retry state. A queued job acknowledgement is not proof of completed synchronization.
- Watchlist operations, previews, metadata repairs and completed plays must never increment O counts. Only an explicit O action may do that.
- Do not automatically retry a non-idempotent O mutation after an uncertain response. Require a fresh confirmed count before another attempt.
- Preserve existing metadata, user artwork, collection IDs and library files unless changing them is part of the user's request. Migration fills empty fields and skips ambiguous rows.
- If working on downloads, only modify downloads verified as created by the user's qBittorrent instance. Do not treat nearby files or folders as owned downloads.

## Validation

Run from the repository root as relevant:

```sh
# Go plugin
go test ./...
go vet ./...

# Stash companion
python3 -m unittest discover -s contrib/stashapp -q
node --test contrib/stashapp/test_stash_silo_subtitles.js contrib/stashapp/test_stash_silo_scrubber.js

# Browser userscript
node --check contrib/tampermonkey/silo-backdrop-hover.user.js
node --test contrib/tampermonkey/test_silo_backdrop_hover.js

# All changes
git diff --check
git diff --cached --check
```

Use focused tests while iterating; run the relevant broader checks after the final code change. Do not claim live behavior was verified from unit tests or a successful build alone.

## Git, releases and deployment

- Stage explicit paths after reviewing their contents; avoid broad staging that can include private or unrelated files.
- When asked to commit and push, commit the authorized changes and push `main`. Confirm the push succeeded and report the commit ID. Do not infer deployment from a push.
- For a requested immediate deployment, build the tested revision locally and transfer it directly to the authorized instances without waiting for GitHub builds. Keep persistent stack images on their existing `latest` tags and preserve registry pull policies. Use a temporary local-build override for that deployment; never pin the stack to a local image unless explicitly requested. Preserve settings, back up affected artifacts and verify running checksums, versions and health. For Silo plugins update the persistent plugin archive too. Keep private connection details out of the repository.
- Update the version of the component being released, and keep packaged metadata consistent with it. Check the release workflow before inventing packaging commands or asset names.
- Do not assume a local Docker connection targets the user's remote server. Verify the target before any mutation, using private connection details only at runtime.
- Deployment requires user authorization, which may already exist in the conversation. Back up affected installation artifacts and preserve configured settings. Use the supported upgrade mechanism; a disk-only binary replacement may be restored from Silo's stored plugin archive.
- Verify the actual running artifact/version, service health and the behavior relevant to the fix. Keep temporary deployment scripts, production dumps and credentials out of Git and remove temporary sensitive files afterward.
- Stash UI and userscript changes may require a browser reload; Silo scheduled-task registration may require a server restart. Explain this when relevant rather than claiming that replacing server files updates an already-open browser.
