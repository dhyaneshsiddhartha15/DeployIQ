# Incident notes

Short written notes after a backend incident (Phase 12.2, step 4). Useful for a
future team; deliberately not overhead for a solo maintainer.

One file per incident: `YYYY-MM-DD-short-slug.md`.

Keep it to four headings — what broke, what users saw, what fixed it, what
would have caught it sooner. A page at most. An elaborate template is how this
directory stops being written in.

The runbook itself is in Phase 12.2 of the design document, not here:

1. Alert fires → check the hosting platform's status and logs first.
2. Bad deploy → roll back (redeploy the previous image; Phase 11.3).
3. Dependency outage (MongoDB Atlas, GitHub OAuth) → post a status note and
   wait it out. No custom failover is justified at this scale.
4. Afterwards → a note here.

Applies from Phase 4 of the build plan. The v1 CLI has no incidents to respond
to: it runs on the user's machine, and GitHub Issues are its alerting channel
(Phase 12.1).
