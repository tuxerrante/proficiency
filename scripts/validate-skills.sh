#!/usr/bin/env bash
set -euo pipefail

skills_root="${1:-.github/skills}"
found=0

if [[ ! -d "$skills_root" ]]; then
  echo "Skills directory does not exist: $skills_root" >&2
  exit 1
fi

shopt -s nullglob
for skill_file in "$skills_root"/*/SKILL.md; do
  found=1
  skill_dir="$(basename "$(dirname "$skill_file")")"
  if ! frontmatter="$(awk '
    {
      sub(/\r$/, "")
    }
    NR == 1 {
      if ($0 != "---") {
        exit 2
      }
      next
    }
    $0 == "---" {
      closed = 1
      exit
    }
    {
      print
    }
    END {
      if (!closed) {
        exit 3
      }
    }
  ' "$skill_file")"; then
    echo "$skill_file: missing or incomplete YAML frontmatter" >&2
    exit 1
  fi

  while IFS= read -r line; do
    if [[ -n "$line" && ! "$line" =~ ^[A-Za-z0-9_-]+:[[:space:]]*.*$ ]]; then
      echo "$skill_file: unsupported frontmatter line: $line" >&2
      exit 1
    fi
  done <<<"$frontmatter"

  name="$(sed -n 's/^name:[[:space:]]*//p' <<<"$frontmatter")"
  description="$(sed -n 's/^description:[[:space:]]*//p' <<<"$frontmatter")"

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
done

if [[ "$found" -eq 0 ]]; then
  echo "No skills found under $skills_root" >&2
  exit 1
fi

echo "Repository skills are structurally valid"
