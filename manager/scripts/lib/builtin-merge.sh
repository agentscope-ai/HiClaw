#!/bin/bash
# builtin-merge.sh - Shared logic for merging builtin sections in .md files
#
# Sourced by upgrade-builtins.sh and tests.
# Provides: BUILTIN_START, BUILTIN_END, BUILTIN_HEADER, update_builtin_section()

BUILTIN_START="<!-- hiclaw-builtin-start -->"
BUILTIN_END="<!-- hiclaw-builtin-end -->"
BUILTIN_HEADER='<!-- hiclaw-builtin-start -->
> ⚠️ **DO NOT EDIT** this section. It is managed by HiClaw and will be automatically
> replaced on upgrade. To customize, add your content **after** the
> `<!-- hiclaw-builtin-end -->` marker below.
'

# update_builtin_section <target_file> <source_file>
#
# Merges the builtin section from source into target:
#   - If target doesn't exist: write marker-wrapped source content
#   - If target has markers: replace builtin section, preserve user content
#   - If target has no markers (old install): overwrite with new builtin + markers
update_builtin_section() {
    local target="$1"
    local source="$2"

    if [ ! -f "${source}" ]; then
        log "  WARNING: source not found: ${source}, skipping"
        return 0
    fi

    mkdir -p "$(dirname "${target}")"

    if [ ! -f "${target}" ]; then
        log "  Creating: ${target}"
        printf '%s\n' "${BUILTIN_HEADER}" > "${target}"
        cat "${source}" >> "${target}"
        printf '\n%s\n' "${BUILTIN_END}" >> "${target}"
        return 0
    fi

    if grep -q 'hiclaw-builtin-start' "${target}" 2>/dev/null; then
        # Detect corrupted file: markers count must be exactly start=1, end=1, and line count <= 500,
        # and the builtin heading (first non-empty line of source) must appear exactly once.
        local start_count end_count heading heading_count
        start_count=$(awk '$0 == "<!-- hiclaw-builtin-start -->" {c++} END {print c+0}' "${target}" 2>/dev/null || echo 0)
        end_count=$(awk '$0 == "<!-- hiclaw-builtin-end -->" {c++} END {print c+0}' "${target}" 2>/dev/null || echo 0)
        heading=$(grep -m1 '^#' "${source}" 2>/dev/null || true)
        if [ -n "${heading}" ]; then
            heading_count=$(awk -v h="${heading}" '$0 == h {c++} END {print c+0}' "${target}" 2>/dev/null || echo 0)
        else
            heading_count=1  # no heading in source, skip this check
        fi
        if [ "${start_count}" -ne 1 ] || [ "${end_count}" -ne 1 ] || [ "${heading_count}" -gt 1 ]; then
            log "  Corrupted (start=${start_count}, end=${end_count}, heading_count=${heading_count}): ${target} — force rewriting"
            local user_content=""
            # Only attempt to preserve user content if there's at least one end marker to anchor on.
            # Also strip any lines matching the builtin heading to avoid duplicates.
            if [ "${end_count}" -ge 1 ]; then
                user_content=$(awk '{lines[NR]=$0} END{for(i=NR;i>=1;i--) print lines[i]}' "${target}" \
                    | awk '$0 == "<!-- hiclaw-builtin-end -->" {exit} {print}' \
                    | awk '{lines[NR]=$0} END{for(i=NR;i>=1;i--) print lines[i]}' \
                    | grep -v 'hiclaw-builtin' || true)
                # Strip duplicate heading lines (same as the first heading in source)
                if [ -n "${heading}" ] && [ -n "${user_content}" ]; then
                    user_content=$(printf '%s\n' "${user_content}" | grep -vF "${heading}" || true)
                fi
            fi
            {
                printf '%s\n' "${BUILTIN_HEADER}"
                cat "${source}"
                printf '\n%s\n' "${BUILTIN_END}"
                [ -n "${user_content}" ] && printf '\n%s\n' "${user_content}"
            } > "${target}.tmp"
            mv "${target}.tmp" "${target}"
            log "  Rewrote corrupted file: ${target}"
            return 0
        fi

        # Has markers: check if builtin content actually changed
        local current_builtin new_builtin
        current_builtin=$(awk '
            $0 == "<!-- hiclaw-builtin-start -->" { found=1; skip=1; next }
            $0 == "<!-- hiclaw-builtin-end -->"   { found=0; skip=0; next }
            !found { next }
            skip && /^[[:space:]]*$/ { next }
            skip && /^>/ { next }
            { skip=0; print }
        ' "${target}")
        new_builtin=$(cat "${source}")
        if [ "${current_builtin}" = "${new_builtin}" ]; then
            log "  Up to date: ${target}"
            return 0
        fi

        # Extract user content after the end marker
        local user_content
        user_content=$(awk '$0 == "<!-- hiclaw-builtin-end -->" {found=1; next} found{print}' "${target}" | grep -v 'hiclaw-builtin')
        {
            printf '%s\n' "${BUILTIN_HEADER}"
            cat "${source}"
            printf '\n%s\n' "${BUILTIN_END}"
            [ -n "${user_content}" ] && printf '\n%s\n' "${user_content}"
        } > "${target}.tmp"
        mv "${target}.tmp" "${target}"
        log "  Updated builtin section: ${target}"
    else
        # Old install without markers: discard old content, write new builtin with markers
        log "  Adding markers to legacy file (discarding duplicate builtin content): ${target}"
        {
            printf '%s\n' "${BUILTIN_HEADER}"
            cat "${source}"
            printf '\n%s\n' "${BUILTIN_END}"
        } > "${target}.tmp"
        mv "${target}.tmp" "${target}"
    fi
}
