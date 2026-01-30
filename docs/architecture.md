## 🧩 High‑Level Architecture

At a high level, Proficiency consists of:

- **CLI / Go module**
  - `openapi.NewParser().ParseFile()` → endpoints
  - `load.Run()` → concurrent HTTP load
  - `profile.CollectCPU()` / `CollectHeap()` → pprof files
  - `profile.Analyze()` → top inefficiencies
  - `report.Generate()` → JSON report

- **GitHub Action**
  - Runs the CLI in CI
  - Posts a **comment on PRs** with a summary (“Top 3 hot functions”)
  - Takes an optional **Pro token** for higher monthly limits

- **Telemetry backend (optional)**
  - Receives aggregated reports
  - Stores **per-repo, per-commit** profiles in Postgres
  - Maintains global **inefficiency patterns** for analytics

- **Web dashboard (future)**
  - GitHub OAuth login
  - Per-repo charts (“top bottleneck over time”)
  - Insights like “this project regressed CPU by +20% this month”

Architecture details and diagrams live in:  
👉 [`docs/architecture.md`](./docs/architecture.md)

---
