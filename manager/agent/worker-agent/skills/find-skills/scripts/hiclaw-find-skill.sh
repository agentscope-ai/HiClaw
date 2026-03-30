#!/bin/sh
# hiclaw-find-skill.sh - Unified skill discovery wrapper for Workers
# Backends:
#   - skills_sh: delegate to `skills find`
#   - nacos: query local/default Nacos CLI profile and render skills-style output

set -eu

BACKEND="${HICLAW_FIND_SKILL_BACKEND:-nacos}"
MAX_RESULTS="${HICLAW_FIND_SKILL_MAX_RESULTS:-6}"
PAGE_SIZE="${HICLAW_FIND_SKILL_NACOS_PAGE_SIZE:-100}"

RESET='[0m'
DIM='[38;5;102m'
TEXT='[38;5;145m'
GRAY_0='[38;5;250m'
GRAY_1='[38;5;248m'
GRAY_2='[38;5;245m'
GRAY_3='[38;5;243m'
GRAY_4='[38;5;240m'
GRAY_5='[38;5;238m'

show_logo() {
    printf '\n'
    printf '%s%s%s\n' "${GRAY_0}" '███████╗██╗  ██╗██╗██╗     ██╗     ███████╗' "${RESET}"
    printf '%s%s%s\n' "${GRAY_1}" '██╔════╝██║ ██╔╝██║██║     ██║     ██╔════╝' "${RESET}"
    printf '%s%s%s\n' "${GRAY_2}" '███████╗█████╔╝ ██║██║     ██║     ███████╗' "${RESET}"
    printf '%s%s%s\n' "${GRAY_3}" '╚════██║██╔═██╗ ██║██║     ██║     ╚════██║' "${RESET}"
    printf '%s%s%s\n' "${GRAY_4}" '███████║██║  ██╗██║███████╗███████╗███████║' "${RESET}"
    printf '%s%s%s\n' "${GRAY_5}" '╚══════╝╚═╝  ╚═╝╚═╝╚══════╝╚══════╝╚══════╝' "${RESET}"
    printf '\n'
}

usage() {
    cat <<'EOF'
Usage:
  hiclaw-find-skill find <query>
  hiclaw-find-skill install <skill>

Environment:
  HICLAW_FIND_SKILL_BACKEND=nacos|skills_sh   Default: nacos
  SKILLS_API_URL                              Used by skills.sh backend when configured
EOF
}

run_skills_find() {
    exec skills find "$@"
}

run_skills_install() {
    if [ $# -lt 1 ]; then
        echo "error: skill name is required for install" >&2
        exit 1
    fi
    exec skills add "$1" -g -y
}

run_nacos_install() {
    if [ $# -lt 1 ]; then
        echo "error: skill name is required for install" >&2
        exit 1
    fi
    run_nacos_cli skill-get "$1"
}

derive_nacos_connection() {
    host="${HICLAW_NACOS_HOST:-}"
    port="${HICLAW_NACOS_PORT:-}"
    namespace="${HICLAW_NACOS_NAMESPACE:-}"
    username="${HICLAW_NACOS_USERNAME:-}"
    password="${HICLAW_NACOS_PASSWORD:-}"
    token="${HICLAW_NACOS_TOKEN:-}"

    printf '%s\n%s\n%s\n%s\n%s\n%s\n' \
        "${host}" "${port}" "${namespace}" "${username}" "${password}" "${token}"
}

run_nacos_cli() {
    if [ $# -lt 1 ]; then
        return 0
    fi

    connection="$(
        derive_nacos_connection
    )"
    host="$(printf '%s\n' "${connection}" | sed -n '1p')"
    port="$(printf '%s\n' "${connection}" | sed -n '2p')"
    namespace="$(printf '%s\n' "${connection}" | sed -n '3p')"
    username="$(printf '%s\n' "${connection}" | sed -n '4p')"
    password="$(printf '%s\n' "${connection}" | sed -n '5p')"
    token="$(printf '%s\n' "${connection}" | sed -n '6p')"

    set -- npx -y @nacos-group/cli "$@"
    [ -n "${host}" ] && set -- "$@" --host "${host}"
    [ -n "${port}" ] && set -- "$@" --port "${port}"
    [ -n "${namespace}" ] && set -- "$@" --namespace "${namespace}"
    [ -n "${username}" ] && set -- "$@" --username "${username}"
    [ -n "${password}" ] && set -- "$@" --password "${password}"
    [ -n "${token}" ] && set -- "$@" --token "${token}"
    "$@"
}

append_skill_lines() {
    output="$1"
    pattern_rank="$2"
    page="$3"
    out_file="$4"

    printf '%s\n' "${output}" | awk -v pattern_rank="${pattern_rank}" -v page="${page}" '
        /^[[:space:]]*[0-9]+\.[[:space:]]/ {
            line = $0
            sub(/^[[:space:]]*[0-9]+\.[[:space:]]+/, "", line)
            item_rank += 1
            printf "%s\t%s\t%s\t%s\n", pattern_rank, page, item_rank, line
        }
    ' >> "${out_file}"
}

fetch_nacos_pattern() {
    pattern="$1"
    pattern_rank="$2"
    out_file="$3"

    page=1
    while :; do
        page_output="$(run_nacos_cli skill-list --name "${pattern}" --page "${page}" --size "${PAGE_SIZE}" 2>&1)" || {
            printf '%s\n' "${page_output}" >&2
            exit 1
        }

        page_count="$(printf '%s\n' "${page_output}" | awk '/^[[:space:]]*[0-9]+\.[[:space:]]/ { c++ } END { print c + 0 }')"
        [ "${page_count}" -gt 0 ] || break

        append_skill_lines "${page_output}" "${pattern_rank}" "${page}" "${out_file}"

        [ "${page_count}" -lt "${PAGE_SIZE}" ] && break
        page=$((page + 1))
    done
}

build_nacos_patterns() {
    query="$1"
    out_file="$2"

    printf '%s\n' "${query}" | awk '
        function add(pattern) {
            if (pattern == "" || seen[pattern]++) return
            print pattern
        }
        {
            q = tolower($0)
            gsub(/[^[:alnum:]]+/, " ", q)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", q)
            gsub(/[[:space:]]+/, " ", q)

            token_count = split(q, tokens, /[[:space:]]+/)
            wildcard = ""
            for (i = 1; i <= token_count; i++) {
                token = tokens[i]
                if (token == "") continue
                if (wildcard == "") wildcard = token
                else wildcard = wildcard "*" token
            }

            if (wildcard != "") add("*" wildcard "*")
            for (i = 1; i <= token_count; i++) {
                token = tokens[i]
                if (length(token) < 2) continue
                add("*" token "*")
            }
        }
    ' > "${out_file}"
}

score_nacos_candidates() {
    query="$1"
    candidates_file="$2"
    scored_file="$3"

    awk -F '\t' -v query="${query}" '
        function trim(s) {
            sub(/^[[:space:]]+/, "", s)
            sub(/[[:space:]]+$/, "", s)
            return s
        }
        function normalize(s) {
            s = tolower(s)
            gsub(/[^[:alnum:]]+/, " ", s)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
            gsub(/[[:space:]]+/, " ", s)
            return s
        }
        function has_word(words, token) {
            return index(" " words " ", " " token " ") > 0
        }
        function has_prefix(words, token,    count, i, arr) {
            count = split(words, arr, /[[:space:]]+/)
            for (i = 1; i <= count; i++) {
                if (arr[i] == "") continue
                if (index(arr[i], token) == 1) return 1
            }
            return 0
        }
        BEGIN {
            q_raw = tolower(query)
            q_words = normalize(query)
            token_count = split(q_words, raw_tokens, /[[:space:]]+/)
            effective_tokens = 0
            for (i = 1; i <= token_count; i++) {
                token = raw_tokens[i]
                if (token == "" || seen_query_token[token]++) continue
                effective_tokens += 1
                tokens[effective_tokens] = token
            }
        }
        NF >= 4 {
            pattern_rank = ($1 + 0)
            page = ($2 + 0)
            item_rank = ($3 + 0)
            line = $4

            name = line
            desc = ""
            split_pos = index(line, " - ")
            if (split_pos > 0) {
                name = substr(line, 1, split_pos - 1)
                desc = substr(line, split_pos + 3)
            }

            name = trim(name)
            desc = trim(desc)
            lname = tolower(name)
            ldesc = tolower(desc)
            name_words = normalize(name)
            desc_words = normalize(desc)

            exact_name_phrase = (q_words != "" && has_word(name_words, q_words))
            exact_desc_phrase = (q_words != "" && has_word(desc_words, q_words))
            raw_name_substring = (q_raw != "" && index(lname, q_raw) > 0)
            raw_desc_substring = (q_raw != "" && index(ldesc, q_raw) > 0)

            score = 0
            coverage = 0
            missing = 0

            if (lname == q_raw) score += 30000
            if (name_words == q_words && q_words != "") score += 24000
            if (exact_name_phrase) score += 12000
            if (exact_desc_phrase) score += 3000
            if (raw_name_substring) score += 1500
            if (raw_desc_substring) score += 400

            for (i = 1; i <= effective_tokens; i++) {
                token = tokens[i]
                matched = 0

                if (has_word(name_words, token)) {
                    score += 1800
                    matched = 1
                } else if (length(token) >= 4 && has_prefix(name_words, token)) {
                    score += 900
                    matched = 1
                } else if (has_word(desc_words, token)) {
                    score += 600
                    matched = 1
                } else if (length(token) >= 4 && has_prefix(desc_words, token)) {
                    score += 250
                    matched = 1
                }

                if (matched) coverage += 1
                else missing += 1
            }

            if (coverage == 0 && !exact_name_phrase && !exact_desc_phrase &&
                !raw_name_substring && !raw_desc_substring) {
                next
            }

            score += coverage * coverage * 500
            score -= missing * 200

            backend_bonus = 200 - (pattern_rank * 20) - ((page - 1) * 5) - item_rank
            if (backend_bonus > 0) score += backend_bonus

            key = lname SUBSEP ldesc
            if (!(key in best_score) || score > best_score[key]) {
                best_score[key] = score
                best_name[key] = name
                best_desc[key] = desc
            }
        }
        END {
            for (key in best_score) {
                printf "%08d\t%s\t%s\n", best_score[key], best_name[key], best_desc[key]
            }
        }
    ' "${candidates_file}" > "${scored_file}"
}

run_nacos_find() {
    if [ $# -lt 1 ]; then
        show_logo
        printf '%sTip:%s search with %shiclaw-find-skill find <query>%s\n' "${DIM}" "${RESET}" "${TEXT}" "${RESET}"
        exit 0
    fi

    query="$*"
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "${tmp_dir}"' EXIT INT TERM
    patterns="${tmp_dir}/patterns.txt"
    candidates="${tmp_dir}/candidates.tsv"
    scored="${tmp_dir}/scored.txt"
    sorted="${tmp_dir}/sorted.txt"

    build_nacos_patterns "${query}" "${patterns}"
    : > "${candidates}"

    pattern_rank=0
    while IFS= read -r pattern; do
        [ -n "${pattern}" ] || continue
        pattern_rank=$((pattern_rank + 1))
        fetch_nacos_pattern "${pattern}" "${pattern_rank}" "${candidates}"
    done < "${patterns}"

    score_nacos_candidates "${query}" "${candidates}" "${scored}"
    sort -r "${scored}" > "${sorted}"

    show_logo

    if [ ! -s "${sorted}" ]; then
        printf '%sNo skills found for "%s"%s\n' "${DIM}" "${query}" "${RESET}"
        exit 0
    fi

    first_name="$(awk -F '\t' 'NR == 1 { print $2; exit }' "${sorted}")"

    printf '%sInstall with%s %shiclaw-find-skill install %s%s\n\n' \
        "${DIM}" "${RESET}" "${TEXT}" "${first_name}" "${RESET}"

    sed -n "1,${MAX_RESULTS}p" "${sorted}" | while IFS="$(printf '\t')" read -r score name desc; do
        if [ -z "${desc}" ]; then
            desc="Available from Nacos skill registry"
        fi

        printf '%s%s%s\n' "${TEXT}" "${name}" "${RESET}"
        printf '%s└ %s%s\n\n' "${DIM}" "${desc}" "${RESET}"
    done
}

command_name="${1:-find}"
if [ $# -gt 0 ]; then
    shift
fi

case "${command_name}" in
    find)
        case "${BACKEND}" in
            skills_sh) run_skills_find "$@" ;;
            nacos) run_nacos_find "$@" ;;
            *)
                echo "error: unsupported HICLAW_FIND_SKILL_BACKEND=${BACKEND}" >&2
                exit 1
                ;;
        esac
        ;;
    install|get)
        case "${BACKEND}" in
            skills_sh) run_skills_install "$@" ;;
            nacos) run_nacos_install "$@" ;;
            *)
                echo "error: unsupported HICLAW_FIND_SKILL_BACKEND=${BACKEND}" >&2
                exit 1
                ;;
        esac
        ;;
    help|-h|--help)
        usage
        ;;
    *)
        echo "error: unknown command: ${command_name}" >&2
        usage >&2
        exit 1
        ;;
esac
