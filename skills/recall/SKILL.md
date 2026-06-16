---
name: recall
description: Use Recall search and generated memory Markdown to answer questions about prior Codex or Claude coding-agent sessions.
---

# Recall

## What Recall Is For

Use Recall to answer questions about past coding-agent work: commands, files, decisions, bugs, PRs, artifacts, errors, and recent project activity. Recall searches summaries generated from Codex and Claude JSONL transcripts. Use summaries for retrieval; use linked raw JSONL lines for exact facts.

### Directory Model

Recall's default memory root is `~/.recall`.

```text
~/.recall/
  sessions/
    <timestamp>-<source>-<session-id>/
      session.md
      sections/
        S001.md
        S002.md
      segments/
        seg000/
          segment.md
          sections/
            S001.md
            S002.md
```

Use only the Markdown memory files: `session.md`, `segment.md`, and `sections/SNNN.md`. Ignore config, index, database, and metadata files unless the user asks about Recall internals.

In commands below, use `$HOME/.recall` as the memory root unless the user explicitly provides another Recall root.

- **Session**: one original Codex or Claude JSONL conversation. `session.md` gives the top-level summary and links downward.
- **Segment**: a compaction-split part of a long session. Many sessions have no `segments/` directory.
- **Section**: usually one user request plus the assistant/tool work for it.
- **Step**: a smaller unit inside a section, usually with source line ranges for looking up the original JSONL file.

## Goal

Goal: answer the user's question in the fastest, most token-efficient way possible. That means finding the right information from past agent sessions using Recall search and the Markdown memory files.

## How To Use Recall

There are three retrieval modes.

1. Semantic search with the binary:
   Use this first. Keep normal searches at `--limit 3`; read the best returned Markdown file before any rewrite. If the right `session_id` is known, scope search immediately.

   ```bash
   recall search --type section --json --limit 3 "<query>"
   recall search --type session,segment --json --limit 3 "<broad query>"
   recall search --type section --json --limit 3 --session <session_id> "<query>"
   ```

   Do at most one focused rewrite unless the result is clearly wrong. Do not raise limits above 5 unless needed for a broad comparison.

2. Agentic search over memory Markdown:
   Use this when semantic search is close but not enough, or when you need to follow links from `session.md` to `segment.md` to `sections/SNNN.md`. Read the YAML frontmatter between `---` first: it contains provenance, timestamps, source file/line ranges, and the main `summary`. Then use the Markdown body for linked segments/sections and step summaries. Search Markdown only within one relevant session/segment tree; never broad `rg .` or broad `rg` across all sessions.

3. Original raw JSONL:
   Use raw JSONL for exact verification, or directly when you already have a narrow file/session/unique term. Do not read broad JSONL history when Recall search or Markdown navigation can narrow the target first. Never print raw JSONL with plain `sed`, `nl`, or unbounded `rg`; JSONL lines can be huge.

## Retrieval Patterns

Choose the cheapest path that can answer the question. The goal is relevant evidence quickly, with minimal tokens.

- **Semantic section search, then Markdown drilldown**: Search sections, open the best `memory_path`, then follow links or step line ranges inside that Markdown.

  ```bash
  recall search --type section --json --limit 3 "<query>"
  sed -n '1,120p' "$HOME/.recall/<memory_path>"
  ```

- **Semantic session/segment search, then agentic Markdown**: use for broad questions or when you need to identify the right session first. Search sessions/segments semantically, then inspect that session's Markdown tree.

  ```bash
  recall search --type session,segment --json --limit 3 "<query>"
  recall search --type section --json --limit 3 --session <session_id> "<focused query>"
  sed -n '1,120p' "$HOME/.recall/sessions/<session>/session.md"
  rg -n "<term>" "$HOME/.recall/sessions/<session>" --glob '*.md'
  ```

- **Switch to raw JSONL when it is cheaper**: after finding a relevant session, decide whether Markdown navigation or scoped raw lookup is faster. For small sessions or exact strings, first find matching line numbers only, then inspect a tiny range with the truncating reader.

  ```bash
  python3 -c 'import sys
p,q=sys.argv[1],sys.argv[2]
for n,line in enumerate(open(p),1):
    if q in line: print(n)' <source_file> "<exact term>" | head

  python3 -c 'import json,sys; p,a,b=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]); M=1000
with open(p) as f:
  for n,line in enumerate(f,1):
    if a<=n<=b:
      try:
        o=json.loads(line); print(f"{n}\t"+json.dumps(o,ensure_ascii=False)[:M])
      except Exception: print(f"{n}\t"+line[:M].rstrip())
    if n>b: break' <source_file> <start_line> <end_line>
  ```
