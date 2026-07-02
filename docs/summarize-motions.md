# Summarize Council Motions

Generate plain-language summaries and short labels for council motions using Claude Haiku, then republish the signed muni bundle.

## Prerequisites

- Postgres running with `thundercitizen` database seeded
- `ANTHROPIC_API_KEY` set (Haiku run costs ~$1.50 for all motions)

## Process

### 1. Run the summarizer

```bash
go run ./cmd/summarize          # only motions missing llm_summary
go run ./cmd/summarize -force   # re-summarize everything
```

This writes `llm_summary` and `llm_label` to `council_motions`, and a meeting-level `llm_summary` to `council_meetings`.

### 2. Republish the muni bundle

```bash
make muni-publish     # extract + sign + zip + upload in one step
```

The app re-fetches the bundle on next boot and applies new datasets automatically.

### 3. Commit

```bash
git add static/councillors/summaries.json
git commit -m "Refresh council motion summaries"
```
