#!/bin/bash
# merge-openclaw-config.sh - Merge remote (MinIO) and local (Worker) openclaw.json
#
# Design principle:
#   Remote (MinIO/Manager) is the authoritative base.
#   Only plugins and channels are merged (Worker may add its own).
#   Everything else (models, agents.defaults, etc.) uses remote as-is.
#   Merge rules:
#     - plugins: deep merge entries, union load.paths
#     - channels: deep merge (remote wins shared types, local-only types preserved)
#     - channels.matrix.accessToken: local wins (Worker re-login)
#
# Usage (as sourced function):
#   source /opt/hiclaw/scripts/lib/merge-openclaw-config.sh
#   merge_openclaw_config <remote_path> <local_path> [<output_path>]
#
# If output_path is omitted, writes merged result to local_path.

merge_openclaw_config() {
    local remote_path="$1"
    local local_path="$2"
    local output_path="${3:-$local_path}"

    if [ ! -f "${remote_path}" ]; then
        # No remote version, keep local as-is
        return 0
    fi

    if [ ! -f "${local_path}" ]; then
        # No local version, use remote directly
        mv "${remote_path}" "${output_path}"
        return 0
    fi

    local merged
    merged=$(jq -n --argfile remote "${remote_path}" --argfile local "${local_path}" '
        $remote
        # ── plugins: only touch fields that exist in at least one side ──
        | if ($remote.plugins.entries // null) != null or ($local.plugins.entries // null) != null then
            .plugins.entries = (($local.plugins.entries // {}) * (.plugins.entries // {}))
          else . end
        | if ($remote.plugins.load.paths // null) != null or ($local.plugins.load.paths // null) != null then
            .plugins.load.paths = ([(.plugins.load.paths // [])[], ($local.plugins.load.paths // [])[]] | unique)
          else . end
        # ── channels: deep merge only when present ──
        | if ($remote.channels // null) != null or ($local.channels // null) != null then
            .channels = (($local.channels // {}) * (.channels // {}))
          else . end
        | if ($local.channels.matrix.accessToken // null) != null then
            .channels.matrix.accessToken = $local.channels.matrix.accessToken
          else . end
    ' 2>/dev/null)

    if [ $? -eq 0 ] && [ -n "${merged}" ]; then
        echo "${merged}" > "${output_path}"
    else
        # jq merge failed, fall back to remote version
        mv "${remote_path}" "${output_path}"
    fi
}
