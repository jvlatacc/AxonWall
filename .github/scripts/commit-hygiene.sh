#!/usr/bin/env bash
# Conventional-commit hygiene gate (spec wave-1 acceptance criterion 8):
# validates the PR title and every commit subject on a PR against the
# Conventional Commits grammar. The CI workflow pipes commit subjects (one
# "<sha> <subject>" line per commit) to stdin and passes the PR title via --title.
set -euo pipefail

readonly TYPE_RE='^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([a-zA-Z0-9._/-]+\))?!?: .+'

title=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--title)
		title="$2"
		shift 2
		;;
	*)
		echo "usage: commit-hygiene.sh [--title <pr-title>] < commit subjects" >&2
		exit 2
		;;
	esac
done

failures=0

check_subject() {
	local label="$1" subject="$2"
	# Merge commits are git plumbing, not author intent; "Revert <type>:" commits
	# carry their own grammar — both pass through.
	if [[ "$subject" == Merge\ * || "$subject" == Revert\ * ]]; then
		return 0
	fi
	if [[ ! "$subject" =~ $TYPE_RE ]]; then
		echo "ERROR: ${label} does not follow Conventional Commits: \"${subject}\"" >&2
		echo "       expected: <type>(optional-scope)!: <subject>" >&2
		echo "       types: build, chore, ci, docs, feat, fix, perf, refactor, revert, style, test" >&2
		failures=$((failures + 1))
	fi
}

if [[ -z "$title" ]]; then
	echo "ERROR: PR title is empty — expected <type>(scope): <subject>" >&2
	failures=$((failures + 1))
else
	check_subject "PR title" "$title"
fi

while IFS= read -r line; do
	[[ -z "$line" ]] && continue
	sha="${line%% *}"
	subject="${line#* }"
	check_subject "commit ${sha}" "$subject"
done

if [ "$failures" -gt 0 ]; then
	echo "commit hygiene: ${failures} violation(s)" >&2
	exit 1
fi
echo "commit hygiene: PR title and all commit subjects follow Conventional Commits"
