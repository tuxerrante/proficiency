# Proficiency 🚀

[![CI](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml/badge.svg)](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/tuxerrante/c40d872af91b3f8cae7757a85dc2f581/raw/coverage.json)](https://github.com/tuxerrante/proficiency)

_Automated API performance profiling from your Swagger spec_

---

## 🌟 What is Proficiency?

Proficiency is a Go-powered tool and GitHub Action that takes your **OpenAPI/Swagger spec**, automatically:

- Generates realistic **load** against your API
- Collects **pprof** profiles (CPU, memory, goroutines…)
- Analyzes them to find the **top inefficiencies**
- Produces a **machine-readable report** (JSON)
- Optionally sends anonymized stats to a **central telemetry service** for trend analysis

The goal: **turn performance profiling from an art into a repeatable CI step** that runs on every pull request.

---

## 🤕 Problem It Solves

Manual performance tuning in Go usually looks like this:

- Remember to enable `net/http/pprof`
- Manually run `go tool pprof`, click around graphs
- Guess which endpoints to hit, from which machines
- Forget to re-run after each PR
- Never aggregate insights across projects

This leads to:

- Hidden CPU bottlenecks in production
- Memory leaks discovered too late
- No systematic knowledge about _common_ inefficiencies across codebases

**Proficiency** solves this by:

- Reading your **Swagger/OpenAPI file**
- Generating load for all defined endpoints in a controlled way
- Collecting profiles automatically during that load
- Analyzing and ranking **top N “offending” functions**
- Producing consistent reports that CI/GitHub can consume
- Sending anonymized insights to a central service (opt-in) to learn what patterns hurt Go code in the wild

---

## ⚙️ Usage

### 1. CLI (local dev)

```bash
# Install
go install github.com/tuxerrante/proficiency/cmd/proficiency@latest

# Run against a local service
proficiency \
  --swagger ./api.yaml \
  --target http://localhost:6060 \
  --duration 30s \
  --concurrency 10 \
  --rps 100 \
  --report ./profiles/report.json
```

This will:

- Parse `api.yaml` for endpoints
- Hit `http://localhost:6060` according to your load config
- Collect CPU (and optionally heap) profiles from `/debug/pprof/…`
- Generate `profiles/report.json`, e.g.:

```json
{
  "timestamp": "2026-02-01T10:30:00Z",
  "repo": "github.com/you/your-service",
  "commit": "abc123",
  "inefficiencies": [
    {
      "function": "main.slowEndpoint",
      "flat_cost": 10234567,
      "cumulative_cost": 52345678,
      "flat_percent": 20.3,
      "cumulative_percent": 48.1
    }
  ]
}
```

### 2. GitHub Action (CI / PRs)

In your repo:

```yaml
# .github/workflows/proficiency.yml
name: Proficiency Profiling

on:
  pull_request:

jobs:
  profile:
    runs-on: ubuntu-latest
    services:
      api:
        image: ghcr.io/you/your-api:latest
        ports:
          - 6060:6060
    steps:
      - uses: actions/checkout@v4

      - name: Run Proficiency
        uses: tuxerrante/proficiency-action@v1
        with:
          swagger-path: ./api.yaml
          target-url: http://localhost:6060
          duration: 30s
          concurrency: 10
          rps: 100
          # Optional: Pro token for higher limits
          proficiency-token: ${{ secrets.PROFICIENCY_TOKEN }}

      - name: Attach report to PR
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const report = JSON.parse(fs.readFileSync('./profiles/report.json'));
            const top = report.inefficiencies.slice(0, 3);
            const body = [
              '## 📊 Proficiency Performance Report',
              '',
              '**Top inefficiencies (CPU):**',
              ...top.map(fn => `- \`${fn.function}\`: ${fn.cumulative_percent.toFixed(1)}% cumulative CPU`),
              '',
              '_Generated automatically by Proficiency_'
            ].join('\n');
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body
            });
```

---

## 🤝 Contributing

Contributions are welcome:

- Bug reports & feature requests via GitHub Issues
- PRs improving:
  - Profiling heuristics
  - Report formats
  - Documentation & examples
- Real-world profiling stories and anonymized reports to refine heuristics

---

## 💬 Questions / Feedback

- Open an issue in the repo
- Start a discussion in `Discussions` tab
- Ping on Reddit (r/golang) or wherever the project is announced

Happy profiling! 🔍🔥
