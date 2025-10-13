# WhatDj v2 🎵

> **Live WhatsApp → Spotify DJ with AI disambiguation**

WhatDj v2 is a production-grade Go service that listens to WhatsApp group messages in real time and automatically adds requested tracks to a Spotify playlist. It features AI-powered song disambiguation, user reaction confirmations, and comprehensive duplicate detection.

## Features

- 🎵 **Automatic Track Detection** - Recognizes Spotify links, music platform links, and free text
- 🤖 **AI Disambiguation** - Uses OpenAI, Anthropic, or Ollama to identify songs from casual text
- 👍 **User Confirmations** - React with 👍/👎 to confirm or reject song suggestions
- 🚫 **Duplicate Prevention** - Bloom filter + LRU cache prevents duplicate additions
- 📊 **Observability** - Prometheus metrics, health checks, and structured logging
- 🔄 **Resilient** - Automatic retries, graceful shutdown, and error handling
- 🐳 **Production Ready** - Docker support, environment configuration, and CI/CD ready

## Quick Start

### Prerequisites

- Go 1.24+
- WhatsApp account and group
- Spotify Premium account and app credentials
- (Optional) OpenAI/Anthropic API key for AI features

### Installation

#### Using Nix (Recommended)

```bash
# Clone and enter dev environment
git clone <repo-url>
cd whatdj
nix develop --impure

# Or if you have direnv
direnv allow
```

#### Manual Setup

```bash
git clone <repo-url>
cd whatdj
go mod download
```

### Configuration

1. **Copy environment template:**
   ```bash
   cp .env.example .env
   ```

2. **Configure Spotify:**
   - Create app at [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
   - Add `http://localhost:8080/callback` to redirect URIs
   - Update `.env` with your credentials

3. **Configure WhatsApp:**
   - Add your group JID to `.env`
   - Run the app to get QR code for login

4. **Configure LLM (Optional):**
   ```bash
   # For OpenAI
   WHATDJ_LLM_PROVIDER=openai
   WHATDJ_LLM_API_KEY=sk-...

   # For Anthropic
   WHATDJ_LLM_PROVIDER=anthropic
   WHATDJ_LLM_API_KEY=sk-ant-...

   # For local Ollama
   WHATDJ_LLM_PROVIDER=ollama
   WHATDJ_LLM_BASE_URL=http://localhost:11434
   ```

### Running

```bash
# Build and run
make build
./bin/whatdj

# Or run directly
go run ./cmd/whatdj

# With custom config
./bin/whatdj --config myconfig.env --log-level debug
```

## Usage

### Message Types

WhatDj recognizes three types of messages:

#### 1. Spotify Links
```
https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC
```
→ **Immediate addition** (if not duplicate)

#### 2. Music Platform Links
```
https://www.youtube.com/watch?v=dQw4w9WgXcQ
```
→ **Asks for clarification:** "Which song do you mean by that?"

#### 3. Free Text
```
never gonna give you up rick astley
```
→ **AI disambiguation:** "Did you mean Rick Astley - Never Gonna Give You Up (1987)? React 👍 to confirm."

### Reactions

- **👍** - Confirm and add to playlist
- **👎** - Mark as duplicate or reject suggestion

### State Machine

```
[MESSAGE] → [DISPATCH]
  ├── Spotify Link → Add (if not duplicate) → 👍 + "Added: Artist - Title"
  ├── Other Link → "Which song?" → [WAIT_REPLY] → LLM → Confirm
  └── Free Text → LLM → High confidence → 👍 Confirm → Add
                       Low confidence → Clarify → 👍 Confirm → Add
```

## Configuration

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `WHATDJ_WHATSAPP_GROUP_JID` | WhatsApp group JID | - | ✅ |
| `WHATDJ_SPOTIFY_CLIENT_ID` | Spotify app client ID | - | ✅ |
| `WHATDJ_SPOTIFY_CLIENT_SECRET` | Spotify app secret | - | ✅ |
| `WHATDJ_SPOTIFY_PLAYLIST_ID` | Target playlist ID | - | ✅ |
| `WHATDJ_LLM_PROVIDER` | AI provider (openai/anthropic/ollama/none) | `none` | ❌ |
| `WHATDJ_LLM_API_KEY` | LLM API key | - | ❌ |
| `WHATDJ_LLM_MODEL` | Model name | Provider default | ❌ |
| `WHATDJ_LLM_THRESHOLD` | Confidence threshold (0-1) | `0.65` | ❌ |
| `WHATDJ_CONFIRM_TIMEOUT` | Reaction timeout (seconds) | `120` | ❌ |
| `WHATDJ_SERVER_PORT` | HTTP server port | `8080` | ❌ |
| `WHATDJ_LOG_LEVEL` | Logging level | `info` | ❌ |

### CLI Flags

```bash
whatdj --help

Flags:
      --config string                  config file (default is .env)
      --log-level string              log level (debug, info, warn, error) (default "info")
      --whatsapp-group-jid string     WhatsApp group JID
      --spotify-client-id string      Spotify client ID
      --spotify-playlist-id string    Spotify playlist ID
      --llm-provider string           LLM provider (openai, anthropic, ollama, none) (default "none")
      --server-port int               HTTP server port (default 8080)
      --confirm-timeout int           Confirmation timeout in seconds (default 120)
```

## Development

### Project Structure

```
cmd/whatdj/           # Main application
internal/
  ├── core/           # Domain types and orchestrator
  ├── whatsapp/       # WhatsApp client (whatsmeow)
  ├── spotify/        # Spotify client (zmb3/spotify)
  ├── llm/            # LLM providers (OpenAI, Anthropic, Ollama)
  ├── store/          # Dedup store (Bloom + LRU)
  └── http/           # HTTP server and metrics
pkg/
  ├── text/           # Message parsing and URL detection
  └── fuzzy/          # String similarity and normalization
```

### Development Environment

The project uses **devenv** (Nix) for reproducible development:

```bash
# Enter development shell
nix develop --impure

# Available tools
devenv-help

# VS Code with extensions
code .
```

Included tools:
- Go toolchain with delve debugger
- golangci-lint for linting
- Git LFS for large files
- Claude Code extension for AI assistance

### Building

```bash
# Build binary
make build

# Run tests
make test

# Run linting
make lint

# Clean build artifacts
make clean
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detection
go test -race ./...
```

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /` | Service information and status |
| `GET /healthz` | Health check (liveness probe) |
| `GET /readyz` | Readiness check |
| `GET /metrics` | Prometheus metrics |

### Metrics

Key metrics exposed at `/metrics`:

- `whatdj_messages_total` - Messages processed by type/status
- `whatdj_adds_total` - Tracks added by source
- `whatdj_duplicates_total` - Duplicate tracks rejected
- `whatdj_llm_calls_total` - LLM API calls by provider/status
- `whatdj_errors_total` - Errors by component/type
- `whatdj_processing_duration_seconds` - Processing time histogram
- `whatdj_playlist_size` - Current playlist track count
- `whatdj_active_sessions` - Active message processing sessions

## Deployment

### Docker

```bash
# Build image
docker build -t whatdj:latest .

# Run with environment file
docker run --env-file .env -p 8080:8080 whatdj:latest

# Or with docker-compose
docker-compose up -d
```

### Production Considerations

- **Secrets**: Use proper secret management (not .env files)
- **Monitoring**: Set up Prometheus + Grafana dashboards
- **Logs**: Forward structured logs to your logging system
- **Backup**: WhatsApp session and Spotify tokens
- **Scaling**: Single instance recommended (WhatsApp sessions are stateful)

## Troubleshooting

### Common Issues

**WhatsApp QR Code Login:**
```bash
# Check logs for QR code output
./bin/whatdj --log-level debug

# Ensure phone is connected to internet
# Scan QR code within 30 seconds
```

**Spotify Authentication:**
```bash
# Verify redirect URI matches Spotify app settings
# Check client ID and secret are correct
# Ensure playlist ID is valid and accessible
```

**LLM Errors:**
```bash
# Check API key is valid
# Verify model name is correct
# Monitor rate limits in logs
```

**Duplicate Detection:**
```bash
# Check playlist snapshot loading in logs
# Verify track IDs are being stored correctly
# Monitor dedup store size metrics
```

### Debug Mode

```bash
# Enable debug logging
WHATDJ_LOG_LEVEL=debug ./bin/whatdj

# Or with flag
./bin/whatdj --log-level debug
```

## Contributing

1. **Fork** the repository
2. **Create** a feature branch (`git checkout -b feature/amazing-feature`)
3. **Commit** your changes (`git commit -m 'Add amazing feature'`)
4. **Push** to the branch (`git push origin feature/amazing-feature`)
5. **Open** a Pull Request

### Development Guidelines

- Follow Go conventions and idioms
- Add tests for new functionality
- Update documentation for user-facing changes
- Run `make lint` before committing
- Use conventional commit messages

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [whatsmeow](https://github.com/tulir/whatsmeow) - WhatsApp Web multi-device client
- [zmb3/spotify](https://github.com/zmb3/spotify) - Spotify Web API wrapper
- [OpenAI](https://openai.com/) / [Anthropic](https://anthropic.com/) - AI disambiguation
- [Prometheus](https://prometheus.io/) - Monitoring and alerting

---

Made with ❤️ and 🎵 by the WhatDj team