#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later

# A failed CI run's log can be read only by a signed-in user with admin rights, but the run's annotations can be read by
# anyone. scripts/ci-e2e.sh and scripts/ci-test.sh source this file and print a failure as ::error workflow commands.
#
# The escaping is GitHub's: in a message a percent becomes %25, a carriage return %0D and a line feed %0A; in a title a
# colon becomes %3A and a comma %2C as well. Each line is cut to 300 characters, and an empty message reads "(none)".

# annotate_escape prints one line, cut and escaped for a message (the default) or for a title.
annotate_escape() {
  local line=${1:0:300}
  line=${line//%/%25}
  line=${line//$'\r'/%0D}
  if [ "$2" = title ]; then
    line=${line//:/%3A}
    line=${line//,/%2C}
  fi
  printf '%s' "$line"
}

# annotate_note prints one annotation: the title, then every line of the file (or standard input) joined with %0A.
annotate_note() {
  local title=$1
  local file=${2:-/dev/stdin}
  local message="" line first=1
  while IFS= read -r line || [ -n "$line" ]; do
    line=$(annotate_escape "$line" message)
    if [ "$first" -eq 1 ]; then
      first=0
    else
      message+="%0A"
    fi
    message+="$line"
  done < "$file"
  if [ -z "$message" ]; then
    message="(none)"
  fi
  printf '::error title=%s::%s\n' "$(annotate_escape "$title" title)" "$message"
  return 0
}

# annotate_tests prints the test job's annotations from a failed go test log: each failed test with the 15 lines before
# it (at most 60 lines), the failed packages, and a panic or a timeout with the 40 lines after it.
annotate_tests() {
  local log=$1
  awk 'BEGIN { size = 16; total = 0; last = 0 }
    {
      buf[NR % size] = $0
      if ($0 ~ /^--- FAIL:/ && total < 60) {
        start = NR - 15
        if (start < 1) start = 1
        if (start <= last) start = last + 1
        for (i = start; i <= NR && total < 60; i++) {
          print buf[i % size]
          total++
          last = i
        }
      }
    }' "$log" | annotate_note "failed tests"
  awk 'index($0, "FAIL\t") == 1' "$log" | annotate_note "failed packages"
  awk 'shown { if (n < 40) { print; n++ } next }
    /panic:|test timed out/ { shown = 1; n = 1; print; next }' "$log" | annotate_note "panic"
  return 0
}

# annotate_e2e prints the e2e job's annotations from a failed run's log: the last passes, the failure with the 3 lines
# after each match (at most 40 lines), the last lines, and each *-failure.txt file the diagnostics wrote in out.
annotate_e2e() {
  local log=$1 out=$2 f
  awk '/^PASS / { count++; pass[count % 12] = $0 }
    END {
      n = count < 12 ? count : 12
      for (i = count - n + 1; i <= count; i++) print pass[i % 12]
    }' "$log" | annotate_note "last passes"
  awk '{ if ($0 ~ /^FAIL |check failed|Error|imeout/) left = 4
         if (left > 0 && total < 40) { print; total++; left-- } }' "$log" | annotate_note "failure"
  tail -n 25 "$log" | annotate_note "last lines"
  for f in "$out"/*-failure.txt; do
    [ -f "$f" ] || continue
    head -n 40 "$f" | annotate_note "$(basename "$f")"
  done
  return 0
}