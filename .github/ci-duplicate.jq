# A referenced local workflow records the immutable merge commit tested by CI.
# The run's pull_requests array can be empty after the branch moves.
.workflow_runs as $runs
| ($runs[] | select(.id == $current)) as $now
| [$runs[]
   | select(.id != $current and .conclusion == "success" and .run_attempt == 1)
   | select(.head_repository.full_name == $repo and .head_sha == $now.head_sha)
   | select(((($now.created_at | fromdateiso8601) - (.created_at | fromdateiso8601)) | fabs) <= 120)
   | select(any(.referenced_workflows[]?;
       .ref == ("refs/pull/" + ($pr | tostring) + "/merge") and
       (.path | startswith($repo + "/.github/workflows/")) and .sha == $merge))]
| first // empty | .id
