# obgo-sync stack

A Docker Compose stack that pairs **obgo-sync** (Obsidian vault sync) with **[QMD](https://github.com/tobi/qmd)** (a local semantic search / MCP server), giving your AI agent live read access to your Obsidian vault.

## Services

| Service | Image | Role |
|---------|-------|------|
| `obgo` | `jookos.org/obgo-sync:latest` | Continuously syncs your Obsidian vault from CouchDB to a local volume |
| `qmd` | `jookos.org/qmd:latest` | Indexes the vault and exposes an MCP server on port 8181 |

The two services share a Docker volume (`obsidian_data` by default). `obgo` writes to it; `qmd` mounts it read-only. QMD's vector database is persisted separately in a `qmd_cache` volume.

## Files

```
stack/
├── docker-compose.yml      # Defines the two-service stack
├── Dockerfile.qmd-node20   # Builds the QMD image (Node 20 + Bun + @tobilu/qmd)
├── start-qmd.sh            # Container entrypoint: bridges port, adds collection, starts MCP
├── Makefile                # Convenience targets
└── .env.example            # Configuration template → copy to env.local
```

## Setup

### 1. Configure environment

```bash
cp .env.example env.local
```

Edit `env.local`:

```dotenv
# Mandatory — your CouchDB connection string
COUCHDB_URL=http://user:pass@host:port/vault-name

# Optional — defaults shown
VAULT_USER=1000:1000        # UID:GID obgo runs as (match your vault files)
VAULT_BIND=./data/vault     # Host path for the vault (or omit to use a Docker volume)
QMD_CACHE_BIND=./data/qmd-cache  # Host path for the QMD vector DB cache
MCP_PORT=8181               # Host port for the QMD MCP server
```

### 2. Build the QMD image

The QMD image is not on a public registry, so build it locally first. This compiles `node-llama-cpp` and takes a few minutes.

```bash
make qmd-image
```

The obgo image is built from the repo root:

```bash
# from the repo root
make build-image   # or however your CI produces jookos.org/obgo-sync:latest
```

### 3. Start the stack

```bash
make up
# equivalent to: docker compose --env-file env.local up
```

On first start QMD will embed the entire vault (this may take a while depending on vault size). Subsequent restarts are fast because the cache is persisted.

## How QMD exposes the MCP server

`start-qmd.sh` does three things when the container starts:

1. Registers the vault as a QMD collection named `obsidian`
2. Runs the initial embedding pass (`qmd embed`)
3. Starts the MCP server in HTTP mode on `127.0.0.1:8180`, then bridges it to `0.0.0.0:8181` via `socat` so it is reachable from outside the container

The MCP endpoint is available at:

```
http://<host>:8181
```

## Connecting your agent to QMD

See the [QMD GitHub README](https://github.com/tobi/qmd) for full documentation on:

- Available MCP tools (`search`, `read`, etc.)
- Configuring QMD as an MCP server in Claude Code, Claude Desktop, Cursor, or any MCP-compatible client
- HTTP vs stdio transport modes

### Claude Code (this setup)

Add the server to your Claude Code MCP config (usually `~/.claude/settings.json` or via `claude mcp add`):

```json
{
  "mcpServers": {
    "obsidian": {
      "type": "http",
      "url": "http://localhost:8181"
    }
  }
}
```

### Agent skill

QMD publishes a ready-made agent skill at [agentskills.so/skills/tobi-qmd-qmd](https://agentskills.so/skills/tobi-qmd-qmd) that teaches your agent how to drive the MCP tools effectively. Install it via `claude skill add` or follow the instructions on that page.

---

Sources:
- [tobi/qmd on GitHub](https://github.com/tobi/qmd)
- [agentskills.so — QMD skill](https://agentskills.so/skills/tobi-qmd-qmd)
- [@tobilu/qmd on npm](https://www.npmjs.com/package/@tobilu/qmd)
