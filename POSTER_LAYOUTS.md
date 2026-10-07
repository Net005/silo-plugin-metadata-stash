# Contextual non-JAV scene covers

Set **Non-JAV poster layout** to `smart`, enable poster repair, and configure JAVBeacon v1.0.286 or later plus the existing Stash/Silo connections and recommendation owner profile. Silo runs the plugin as a Go binary; JAVBeacon bundles Python, OpenCV and the MIT-licensed YuNet model, so no additional service is required.

The renderer uses original Stash artwork, never the old padded/cropped Silo poster. It detects faces in four orientations and produces a sharp 1200×1800 (2:3) crop using the maximum source height/width that fits. Fitting face groups are retained; otherwise it chooses the largest confident face that leaves at least one third of the frame outside the face's vertical extent. A smaller face with surrounding detail wins over an oversized close-up. If every detected face is already too tight, the existing cover remains unchanged. Images with no detected faces use a centre crop. No generative fill or blurred padding is used in smart mode.

The authenticated JAVBeacon renderer limits input to 16 MiB / 30 megapixels, runs at most two processes, and times out after 45 seconds. Failures are reported and retried rather than silently reverting to a legacy layout. New metadata image choices use the same smart endpoint for non-JAV scenes without linked release artwork.

The repair worker operates in bounded batches, persists per-item progress, and resumes after interruption. This release resets the scan revision and layout fingerprint to refresh old non-JAV covers. Automatic repair excludes JAV codes, linked JAV release artwork, ambiguous matches and manual artwork locks. Existing `contain` and legacy `face` modes remain available as explicit alternatives.

Queue renewal using the existing **Repair Stash poster layouts** task or the recommendation admin's poster renewal control. Monitor `/recommendations/posters/status` through Silo's plugin content route. A completed scan can include unchanged close-ups; inspect the error map for failures.
