# LLM-Consensus

Multi-agent debate orchestration behind an OpenAI-compatible chat endpoint.

LLM-Consensus fans a request out to multiple agents, runs a structured debate loop (draft, critique, synthesize, vote, revise), and returns one final response.

It aims to improve answer quality and reasoning via multi-agent critique and voting. It can reduce some hallucinations from single-pass generation, but it is complementary to RAG rather than a replacement for retrieval-grounded answers. 

## How It Works

```text
Client                        LLM-Consensus                        Providers
  |                                 |                                  |
  | POST /v1/chat/completions       |                                  |
  |-------------------------------->|                                  |
  |                                 | 1) Draft (all agents)            |
  |                                 |--------------------------------->|
  |                                 |<---------------------------------|
  |                                 | 2) Critique (all agents)         |
  |                                 |--------------------------------->|
  |                                 |<---------------------------------|
  |                                 | 3) Synthesize candidate          |
  |                                 |--------------------------------->|
  |                                 |<---------------------------------|
  |                                 | 4) Vote (JSON)                   |
  |                                 |--------------------------------->|
  |                                 |<---------------------------------|
  |                                 | 5) Consensus? revise/fallback    |
  |                                 |                                  |
  | Final response                  |                                  |
  |<--------------------------------|                                  |
```

## Quick Start

### Prerequisites

- Go 1.25+
- At least one provider API key

### Environment setup

Copy and edit env template:

```bash
cp .env.example .env
```

Example `.env` values:

```bash
OPENAI_API_KEY=your-openai-key
ANTHROPIC_API_KEY=your-anthropic-key
GROK_API_KEY=your-grok-key
```

### Start server

```bash
make start
```

Default address from config:

- `http://127.0.0.1:8080`

### Test request (non-streaming)

```bash
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llm-consensus-balanced",
    "messages": [
      {"role": "user", "content": "Give me a 5-step plan to learn Go."}
    ],
    "stream": false
  }'
```

### Test request (streaming)

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llm-consensus-balanced",
    "messages": [
      {"role": "user", "content": "Explain Go interfaces simply."}
    ],
    "stream": true
  }'
```

## Presets and Virtual Models

Virtual aliases configured in `config.yaml`:

- `llm-consensus-fast` -> `fast`
- `llm-consensus-balanced` -> `balanced`
- `llm-consensus-paranoid` -> `paranoid`
- `llm-consensus-debug` -> `debug`

Preset fields:

- `max_rounds`: max vote/revise loops
- `strict_unanimity`: unanimous or majority acceptance
- `output_mode`: `clean`, `debug`, or `audit`

## Output Modes

- `clean`: final answer text only
- `debug`: human-readable phase transcript summary
- `audit`: structured JSON transcript

`output.default_mode` in `config.yaml` controls default behavior unless a preset overrides it.

## API Reference

### GET /health

Basic liveness check.

Response:

```json
{"status":"ok"}
```

### GET /v1/models

Returns OpenAI-compatible model list exposed by handler code.

### POST /v1/chat/completions

OpenAI-compatible chat endpoint.

Request fields:

- `model` (required)
- `messages` (required)
- `stream` (optional)

Non-streaming returns one completion object.

Streaming returns SSE chunks and a terminal:

```text
data: [DONE]
```

## Configuration

All runtime config lives in `config.yaml`:

- `server`: host/port
- `agents`: debate participants (`name`, `role`, `model`, `provider`, `base_url`, `api_key`)
- `debate`: global defaults (`max_rounds`, `strict_unanimity`)
- `output`: default output mode
- `virtual_models`: alias mapping to presets
- `presets`: named behavior profiles

## Project Structure

```text
cmd/llm-consensus/
  main.go                    # server bootstrap

internal/
  config/config.go           # YAML + env loading, preset resolution
  handler/chat.go            # /health, /v1/models, /v1/chat/completions
  debate/orchestrator.go     # phase orchestration
  debate/prompts.go          # phase prompt templates
  debate/consensus.go        # vote parsing and consensus rules
  debate/transcript.go       # debug/audit transcript formatting
  provider/client.go         # provider factory
  provider/openai.go         # OpenAI-compatible provider
  provider/anthropic.go      # Anthropic provider
  types/types.go             # shared request/response interfaces
```

## Troubleshooting

### Model does not exist

- Verify model ID against provider account models API.
- Update agent `model` values in `config.yaml`.
- Restart the service.

### Missing API keys

- Ensure `.env` exists and has required keys.
- Ensure `api_key` placeholders in `config.yaml` match variable names.
- Start via `make start` so `.env` is loaded.

### Provider 400 during draft phase

- Validate model name/provider pairing in `config.yaml`.
- Check API key permissions and quota.
- Check server logs for provider-specific error payload.

## What This Is Not

- Not a database-backed system
- Not a web dashboard
- Not a tool-execution runtime
- Not long-term memory across requests

## License

MIT
