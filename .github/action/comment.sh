#!/usr/bin/env bash
# Creates or updates DeployIQ's single comment on the pull request. One comment
# per PR, edited on every push, so the conversation does not fill with copies.
set -euo pipefail

marker='<!-- deployiq-optimizer -->'

existing=$(gh api "repos/$REPO/issues/$PR_NUMBER/comments" --paginate \
  --jq ".[] | select(.body | startswith(\"$marker\")) | .id" | head -n 1)

if [ -n "$existing" ]; then
  gh api -X PATCH "repos/$REPO/issues/comments/$existing" -F "body=@$BODY_FILE" >/dev/null
  echo "Updated comment $existing"
else
  gh api -X POST "repos/$REPO/issues/$PR_NUMBER/comments" -F "body=@$BODY_FILE" >/dev/null
  echo "Posted a new comment"
fi
