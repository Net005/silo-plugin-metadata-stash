# Weekly recommendations

Stash Metadata v0.3.8 builds up to **250 verified items per collection** across all
movie libraries using this plugin. The nine kinds are `for-you`, `top-rated`,
`revisit`, `favourites`, `watchlist`, `overlooked`, `different`, `recent`, `spotlight`.
Names use the existing Stash prefix exactly, including trailing spaces.

## Setup

Enable Weekly Recommendations, enter the owner Silo profile ID and save the OpenAI
key in its secret field. The fixed model is `gpt-6-luna`, reasoning `none` by default.
Keep Preview only enabled initially. Open this **administrator-only** report data page:

`/api/v2/plugin-content/plugins/<installation ID>/recommendations`

Preview generates a report without creating visible recommendation collections.
Inspect evidence, warnings and usage, then turn Preview only off and build collections.
Task `started` acknowledges worker admission; the report contains the final outcome.
Collections are featured; the Silo frontend controls their placement. Library collections
are shared with profiles allowed to access the library. Stash activity is account-wide.

## Schedule

Default Sunday **03:30 Europe/Amsterdam**. The resident scheduler honours timezone,
DST and missed runs, once per weekly period. The native recommendation-sync task can
also use a weekly binding; new native tasks register after a Silo server restart.
An explicit Build collections button allows a rebuild within the same week.
Hourly local pruning removes watched discoveries, unavailable items and dismissals
without another AI call. Existing realtime saved-filter Watchlist sync stays independent.

## Library choices

Defaults JSON supports:

```json
{"count":250,"high_rating":80,"low_rating":40,"cooldown_days":14,
 "recent_days":90,"retain_fraction":0.7,"max_entity_fraction":0.3,
 "max_discovery_overlap":2,"cross_library":true}
```

Per-library JSON inherits those settings:

```json
{"18":{"collections":["for-you","top-rated","revisit","spotlight"]},
 "19":{"cross_library":false},"20":{"enabled":false}}
```

Set `excluded_scenes` to Stash IDs, and `excluded_performers`, `excluded_studios`,
`excluded_tags` to IDs or exact names. Disabling a library hides only its owned
recommendation collections. Sparse libraries get confidence warnings. Lists remain
shorter when evidence/candidates are insufficient, rather than being padded.
The entity cap adapts to candidate population so a single-studio library can still
have fifty picks; Spotlight can concentrate on its supported theme.

## Feedback and safety

Ratings are deliberate feedback. Rating 0/null is unset, absent O is neutral, and
partial playback is not a completed play. Explicit O events and repeat plays have
diminishing weight. Recent and lifetime patterns are calculated separately.

The engine makes only GraphQL **queries** to Stash. It never changes counters, history,
ratings, favourites or tags. Candidates require exact native file-path identity and
catalog membership; title-only and ambiguous matches are excluded. Non-native IDs
without a verified native mapping are reported as unmatched.
When enabled, JAVBeacon archives union timestamps using unique exact paths, unique
release codes, or unchanged IDs with agreeing title/code. Counters use the maximum
of snapshots and deduplicated events, never a sum of overlapping copies. Removed
releases with available JAVBeacon metadata contribute history-only feedback. Exact
unique Stash studio/tag names and performer names/aliases link their preferences;
ambiguous identities are skipped. A unique archived path assigns library-specific
feedback; otherwise feedback contributes only where cross-library learning is enabled.
History-only rows have no media ID and cannot be collection candidates. The report
shows archive counts, removed releases used, and unresolved active snapshots. Archive
read failures preserve previous collections. This enrichment is in memory, not a
Stash history restore.
Source/activity drops over 20% stop publication/pruning when a sufficient prior baseline
exists. All weekly source reads finish before visible collection writes. Collection
membership changes are incremental, order changes use ETags, and partial failures
are reported. Unrelated and saved-filter collections cannot be written by this engine.

## Persistence and costs

A **hidden admin collection**, Stash recommendation state, stores the versioned report,
weekly selections, exposures, dismissals, safety baseline, lease and spending ledger
in Silo's database. It survives restarts/upgrades. Do not delete it to reset API spending.

Luna receives anonymised entity IDs, scores, ratings, confidence and factual evidence.
No titles, narratives, images, paths or credentials are sent. Structured output must
assign priorities to exactly the local candidate IDs through required schema properties; unknown/duplicate/omitted IDs fall back to
local ordering. Explanations stay grounded in the local evidence. API requests use
`store:false`, no tools, bounded input and bounded output, including reasoning.

The default **$2/calendar-month cap** is enforced by reserving a conservative request
cost durably before sending. Unknown request outcomes retain reservations, so retries
cannot bypass the cap. Preview calls consume budget too. Reported token usage, estimated costs and unsettled reservations are shown in the report.
These are not verified invoice charges. OpenAI’s Costs dashboard is authoritative.
Standard short-context rates per million tokens: input $0.10, cached input $0.01,
cache writes $0.125, output $0.50. Requests explicitly use the standard service tier.
Cache details are applied when returned; old reports lack cache breakdowns.
Each collection is ranked separately with at most 500 candidates, avoiding oversized
schemas and long-context pricing. Reservations include the full request and schema. No automatic
retries, Batch, paid tools or generation. Revisit rates when API pricing changes.

## Validation

A retrospective diagnostic holds out explicit O events from the last 90 days and
compares retrieval against an earlier-history studio baseline. Current ratings and
favourites have no edit timestamps and are excluded to prevent leakage. Sparse data
reports insufficient feedback. Results are activity retrieval, not calibrated enjoyment
probabilities. The engine's tests cover rating eligibility, fifty-item limits, diversity,
AI ID validation, collection ownership, durable budget state, timezone/DST and companion
notifications that never trigger paid ranking from an edit hook.

The report page can also be opened from the plugin detail page’s More actions → Stash recommendations. Its public HTML shell contains no library or history data; report reads and actions remain administrator-only. Direct links establish Silo’s short-lived plugin cookie from the existing same-origin Silo login. An expired or locked profile requires signing in/unlocking in Silo first.
