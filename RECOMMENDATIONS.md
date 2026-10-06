# Weekly recommendations

Stash Metadata v0.3.23 builds up to **500 verified items per collection** across all
movie libraries using this plugin. The original nine kinds are `for-you`, `top-rated`,
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
{"count":500,"high_rating":80,"low_rating":40,"cooldown_days":14,
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

## Additional collections

The default selection now includes sixteen kinds, each with up to 500 eligible
items. The five additional kinds add no OpenAI requests. Four reuse existing validated
Luna priorities where available, while new releases retain date ordering:

- `monthly-spotlight` and `yearly-spotlight`: supported cast/studio/series themes
  selected with the calendar month or year as their rotation seed. They are
  refreshed by the existing weekly job as availability and feedback change;
  these are theme periods, not claims that all feedback happened in that period.
- `cast-spotlight`: several performers with repeated positive feedback and
  available unwatched scenes. It avoids performers with net negative evidence.
- `general-spotlight`: a broad mixture of unwatched scenes matching supported
  cast/studio relationships and favourite performers.
- `new-releases`: unwatched scenes released within `recent_days`, matching
  supported preferences or favourite cast. Sorted by release date descending;
  future releases and unknown release dates are excluded. It uses release date,
  not import date, and generic genre tags do not establish eligibility.

Existing custom `collections` arrays remain explicit selections: append the new
kind IDs to enable them. Existing rating, exclusion, diversity and overlap rules
still apply, so a collection can contain fewer than 500 items.

### Reusing Luna rankings

Monthly/yearly spotlight, cast spotlight and general spotlight reuse priorities
from relevant existing collections in the same library. Eligibility, positive
cast evidence, exclusions, diversity and the 500-item limit remain local.
New releases keep descending release-date order and do not reuse AI ordering.

Validated numeric priorities are retained with selected scenes in durable reports.
Current priorities take precedence over the previous report's priorities; saved
priorities expire for reuse after six weeks (report retention remains six months).
Older reports without numeric priorities are not guessed to be AI rankings.
Only the exact scene and Silo item mapping can reuse a priority.

Source priorities are converted to within-source percentiles, so unrelated model
score scales are not compared directly. Covered candidates blend 75% local rank
with 25% reused priority; saved evidence has half weight when combined with
current evidence. Only covered slots are reordered: uncovered scenes retain
their local positions. Final local diversity rules still apply. The report page
and JSON show coverage, saved coverage and source collection kinds. A priority
can support several collections without another API call or spending reservation.
These are bounded ranking improvements, not a claim of measured accuracy gains.


## Personalised Watchlist periods

The selected **current Stash Watchlist saved filter** is the only membership source
for these three collections. Archived JAVBeacon watch/O history informs preferences
but never introduces missing releases or entries outside the current Watchlist.
Candidates must have a verified item in the corresponding Silo library. Prefixes,
weekly scheduling, six-month report retention and rating/exclusion rules are retained.

| Collection kind | Title | Dated activity window |
| --- | --- | --- |
| `watchlist` | Watchlist This Week | 7 days |
| `monthly-watchlist` | Watchlist This Month | 30 days |
| `yearly-watchlist` | Watchlist This Year | 365 days |

All three refresh weekly. Windows describe preference evidence, not release dates
or refresh schedules. The weekly collection favours recent interests, the monthly
collection mixes recent and established preferences, and the yearly collection puts
more weight on long-term taste. Undated ratings and aggregate play/O counts remain
long-term signals; they are never assigned invented event dates. Sparse recent
history reduces the recent weighting and is disclosed in each pick's explanation.

Content fit uses performers, studios/labels, series, supported performer pairs and
short/medium/long durations. Pair features require history of the same combination;
individual favourite performers alone do not establish a pair preference. Common
tags receive less weight in proportion to their prevalence; duration and tags have
less weight than cast/studio/series. Cross-library feedback, when enabled, is a weaker
prior and excludes scenes already counted in the local library. Low ratings, scene
and entity exclusions and dismissals still prevent selection.

The default target is **up to 500**, subject to available Watchlist matches and
selection constraints. The previous shipped `count:250` defaults and per-library overrides now migrate
to 500 automatically. Set `keep_legacy_250_count:true` alongside `count:250` to
retain that limit intentionally. Other custom lower limits remain respected.
Report `target_items_per_collection` records the effective limit for each library. Three periods share a separate overlap budget: unselected candidates
are preferred, and a scene can occur in at most two periods when the pool is large
enough. Small pools permit more overlap instead of producing empty collections.
Per-studio/performer diversity limits still apply. An exact 500 or fully disjoint
membership is not guaranteed for a small Watchlist.

Existing saved configurations selecting `watchlist` automatically include the two
new periods. Set `watchlist_periods:false` to stop this automatic expansion, and
use an explicit `collections` list to choose the desired periods. Libraries which
exclude `watchlist` do not have it enabled automatically.

These three kinds make **no dedicated OpenAI requests**. They reuse verified
current/saved candidate priorities from existing For You, favourites, recent,
overlooked or diversity rankings where IDs and local media matches agree. Local
preferences retain most ranking weight; uncovered entries retain their local
order. Report `ranking_reuse` fields show actual coverage. The existing OpenAI
monthly cap and scheduled requests for other collections are unchanged; the old
weekly Watchlist no longer consumes its own model request.


Version 0.3.23 fingerprints the ranking revision and effective configuration.
Updating from an old build or changing recommendation settings makes this week's
result eligible for a rebuild, even when the scheduled task previously completed.
Active durable leases still block duplicate workers; interrupted workers become
eligible when their leases expire. A failed build with unchanged configuration
is not retried every minute (use Run Now for an explicit retry). The task status
still acknowledges admission only; the durable report contains final success,
failure, effective limits and actual published counts.
