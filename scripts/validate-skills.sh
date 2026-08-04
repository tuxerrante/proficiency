#!/usr/bin/env bash
set -euo pipefail

skills_root="${1:-.github/skills}"
found=0

for skill_file in "$skills_root"/*/SKILL.md; do
  if [[ ! -f "$skill_file" ]]; then
    continue
  fi
  found=1
  skill_dir="$(basename "$(dirname "$skill_file")")"
  name="$(awk '
    NR == 1 && $0 != "---" { exit 1 }
    NR > 1 && /^name:[[:space:]]*/ {
      sub(/^name:[[:space:]]*/, "")
      print
      exit
    }
  ' "$skill_file")"
  description="$(awk '
    NR > 1 && /^description:[[:space:]]*/ {
      sub(/^description:[[:space:]]*/, "")
      print
      exit
    }
  ' "$skill_file")"

  if [[ "$name" != "$skill_dir" ]]; then
    echo "$skill_file: name must match directory $skill_dir" >&2
    exit 1
  fi
  if [[ ! "$name" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]]; then
    echo "$skill_file: invalid skill name $name" >&2
    exit 1
  fi
  if [[ -z "$description" ]]; then
    echo "$skill_file: description is required" >&2
    exit 1
  fi
  if [[ "$(grep -c '^---$' "$skill_file")" -lt 2 ]]; then
    echo "$skill_file: incomplete YAML frontmatter" >&2
    exit 1
  fi
done

if [[ "$found" -eq 0 ]]; then
  echo "No skills found under $skills_root" >&2
  exit 1
fi

echo "Repository skills are structurally valid"
