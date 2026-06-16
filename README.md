# Recall

## Setup

Download the Apple Silicon macOS release binary:

```sh
curl -L -o recall_Darwin_arm64.tar.gz \
  https://github.com/MarcBrede/recall/releases/download/v0.1.0/recall_Darwin_arm64.tar.gz
```

Install it somewhere on your `PATH`:

```sh
tar -xzf recall_Darwin_arm64.tar.gz
sudo mv recall /usr/local/bin/recall
sudo chmod +x /usr/local/bin/recall
```

Run an initial ingest:

```sh
recall ingest --last 5
```

Install the Recall skill for either Codex or Claude:

```sh
# Codex
mkdir -p "$HOME/.codex/skills/recall"
curl -fsSL \
  https://raw.githubusercontent.com/MarcBrede/recall/v0.1.0/skills/recall/SKILL.md \
  -o "$HOME/.codex/skills/recall/SKILL.md"

# Claude
mkdir -p "$HOME/.claude/skills/recall"
curl -fsSL \
  https://raw.githubusercontent.com/MarcBrede/recall/v0.1.0/skills/recall/SKILL.md \
  -o "$HOME/.claude/skills/recall/SKILL.md"
```
