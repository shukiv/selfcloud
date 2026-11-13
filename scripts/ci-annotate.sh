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

# annotate_tests prints the test job's annotations from a failed go test log: each failed test with the indented lines
# after it (at most 20 per test and 60 in all), the failed packages, and a panic, a timeout or a data race with the
# next 40 lines.
annotate_tests() {
  local log=$1
  awk 'BEGIN { per = 0; total = 0 }
    /^--- FAIL:/ {
      per = 0
      if (total < 60) {
        print
        total++
        per = 1
      }
      next
    }
    per > 0 && (substr($0, 1, 1) == " " || substr($0, 1, 1) == "\t") {
      if (per < 20 && total < 60) {
        print
        per++
        total++
      }
      next
    }
    { per = 0 }' "$log" | annotate_note "failed tests"
  awk 'index($0, "FAIL\t") == 1' "$log" | annotate_note "failed packages"
  awk 'shown { if (n < 40) { print; n++ } next }
    /panic:|test timed out|WARNING: DATA RACE/ { shown = 1; n = 1; print; next }' "$log" | annotate_note "panic"
  return 0
}

# annotate_e2e prints the e2e job's annotations from a failed run's log: the last passes, the failure with the 3 lines
# after each match (at most 40 lines), the last lines, and at most 5 of the *-failure.txt files the diagnostics wrote in
# out, at most 40 lines each.
annotate_e2e() {
  local log=$1 out=$2 f notes=0
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
    if [ "$notes" -ge 5 ]; then
      break
    fi
    head -n 40 "$f" | annotate_note "$(basename "$f")"
    notes=$((notes + 1))
  done
  return 0
}
