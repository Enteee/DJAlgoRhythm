# DJAlgoRhythm 🎵

> **Live Chat → Spotify DJ with AI disambiguation**

DJAlgoRhythm is a production-grade Go service that listens to chat messages (Telegram/WhatsApp) in real time and automatically adds requested tracks to a Spotify playlist. It features AI-powered song disambiguation, user reaction confirmations, and comprehensive duplicate detection.

## Features

- 🎵 **Automatic Track Detection** - Recognizes Spotify links, music platform links, and free text
- 💬 **Multi-Platform Support** - Primary Telegram support with optional WhatsApp integration
- 🤖 **AI Disambiguation** - Uses OpenAI, Anthropic, or Ollama to identify songs from casual text
- 👍 **User Confirmations** - React with 👍/👎 or use inline buttons to confirm/reject song suggestions
- 🚫 **Duplicate Prevention** - Bloom filter + LRU cache prevents duplicate additions
- 📊 **Observability** - Prometheus metrics, health checks, and structured logging
- 🔄 **Resilient** - Automatic retries, graceful shutdown, and error handling
- 🐳 **Production Ready** - Docker support, environment configuration, and CI/CD ready

## Quick Start

### Prerequisites

- Go 1.24+
- **Telegram**: Bot token and group (recommended)
- **WhatsApp**: Account and group (optional, disabled by default)
- Spotify Premium account and app credentials
- (Optional) OpenAI/Anthropic API key for AI features

### Installation

#### Using Nix (Recommended)

```bash
# Clone and enter dev environment
git clone <repo-url>
cd djalgorhythm
nix develop --impure

# Or if you have direnv
direnv allow
```

#### Manual Setup

```bash
git clone <repo-url>
cd djalgorhythm
go mod download
```

### Configuration

1. **Copy environment template:**

   ```bash
   cp .env.example .env
   ```

2. **Configure Spotify:**
   - Create app at [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
   - Add `http://127.0.0.1:8080/callback` to redirect URIs
   - Update `.env` with your credentials

3. **Configure Telegram (Default):**
   - Create bot with [@BotFather](https://t.me/botfather)
   - Add bot to your group and make it admin
   - Update `.env` with your bot token (group will be selected automatically):

   ```bash
   DJALGORHYTHM_TELEGRAM_ENABLED=true
   DJALGORHYTHM_TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
   # DJALGORHYTHM_TELEGRAM_GROUP_ID=-100xxxxxxxxxx  # Auto-detected on first run
   DJALGORHYTHM_WHATSAPP_ENABLED=false
   ```

   **Note**: If no group ID is configured, the application will automatically scan for available groups and let you select one interactively on first startup.

4. **Configure WhatsApp (Optional):**
   ⚠️ **Warning**: WhatsApp bot usage may violate their Terms of Service. Enable at your own risk.

   ```bash
   DJALGORHYTHM_WHATSAPP_ENABLED=true
   DJALGORHYTHM_WHATSAPP_GROUP_JID=120363123456789@g.us
   DJALGORHYTHM_TELEGRAM_ENABLED=false
   ```

5. **Configure LLM (Optional):**

   ```bash
   # For OpenAI
   DJALGORHYTHM_LLM_PROVIDER=openai
   DJALGORHYTHM_LLM_API_KEY=sk-...

   # For Anthropic
   DJALGORHYTHM_LLM_PROVIDER=anthropic
   DJALGORHYTHM_LLM_API_KEY=sk-ant-...

   # For local Ollama
   DJALGORHYTHM_LLM_PROVIDER=ollama
   DJALGORHYTHM_LLM_BASE_URL=http://localhost:11434
   ```

### Running

```bash
# Build and run
make build
./bin/djalgorhythm

# Or run directly
go run ./cmd/djalgorhythm

# With custom config
./bin/djalgorhythm --config myconfig.env --log-level debug
```

## Chat Platform Setup

### Telegram Setup (Recommended)

1. **Create Bot:**
   - Message [@BotFather](https://t.me/botfather)
   - Use `/newbot` and follow instructions
   - Copy the bot token

2. **Setup Group:**
   - Create a group or use existing one
   - Add your bot to the group
   - Make the bot an admin (required for message access)

3. **Get Group ID:**
   - **Automatic Detection** (Recommended): Leave `DJALGORHYTHM_TELEGRAM_GROUP_ID` unset in your `.env` file
   - The application will scan for available groups and let you select one interactively on first startup
   - **Manual Setup**: If you know your group ID, set it directly in `.env`

4. **Configure Bot:**
   - Enable inline mode (optional): `/setinline` with @BotFather
   - Set commands (optional): `/setcommands` with @BotFather

### WhatsApp Setup (Optional)

⚠️ **Warning**: WhatsApp bot usage may violate their Terms of Service. This feature is disabled by default and should only be used for personal/testing purposes.

1. **Get Group JID:**

   ```bash
   # Run with debug logging to see group JIDs
   DJALGORHYTHM_WHATSAPP_ENABLED=true DJALGORHYTHM_LOG_LEVEL=debug ./bin/djalgorhythm
   ```

2. **QR Code Login:**
   - Scan QR code with WhatsApp on your phone
   - Session will be saved for future use

## Usage

### Message Types

DJAlgoRhythm recognizes three types of messages:

#### 1. Spotify Links

```
https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC
https://spotify.link/ie2dPfjkzXb
spotify:track:4uLU6hMCjMI75M1A2tKUQC
```

→ **Immediate addition** (if not duplicate)

Supports both full Spotify URLs and shortened `spotify.link` URLs.

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

### User Interactions

#### Telegram

- **Inline Buttons**: Click "👍 Confirm" or "👎 Not this"
- **Reactions** (if supported): React with 👍 or 👎 emojis

#### WhatsApp

- **Reactions**: React with 👍 or 👎 emojis

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
| **Chat Platforms** | | | |
| `DJALGORHYTHM_TELEGRAM_ENABLED` | Enable Telegram integration | `true` | ❌ |
| `DJALGORHYTHM_TELEGRAM_BOT_TOKEN` | Telegram bot token | - | ✅ (if enabled) |
| `DJALGORHYTHM_TELEGRAM_GROUP_ID` | Telegram group ID (auto-detected if unset) | - | ❌ |
| `DJALGORHYTHM_ADMIN_APPROVAL` | Require admin approval for songs | `false` | ❌ |
| `DJALGORHYTHM_ADMIN_NEEDS_APPROVAL` | Require approval even for admins (testing) | `false` | ❌ |
| `DJALGORHYTHM_WHATSAPP_ENABLED` | Enable WhatsApp integration | `false` | ❌ |
| `DJALGORHYTHM_WHATSAPP_GROUP_JID` | WhatsApp group JID | - | ✅ (if enabled) |
| **Spotify** | | | |
| `DJALGORHYTHM_SPOTIFY_CLIENT_ID` | Spotify app client ID | - | ✅ |
| `DJALGORHYTHM_SPOTIFY_CLIENT_SECRET` | Spotify app secret | - | ✅ |
| `DJALGORHYTHM_SPOTIFY_PLAYLIST_ID` | Target playlist ID | - | ✅ |
| **LLM** | | | |
| `DJALGORHYTHM_LLM_PROVIDER` | AI provider (openai/anthropic/ollama/none) | `none` | ❌ |
| `DJALGORHYTHM_LLM_API_KEY` | LLM API key | - | ❌ |
| `DJALGORHYTHM_LLM_MODEL` | Model name | Provider default | ❌ |
| **General** | | | |
| `DJALGORHYTHM_CONFIRM_TIMEOUT_SECS` | Reaction timeout (seconds) | `120` | ❌ |
| `DJALGORHYTHM_CONFIRM_ADMIN_TIMEOUT_SECS` | Reaction timeout for admins (seconds) | `3600` | ❌ |
| `DJALGORHYTHM_QUEUE_AHEAD_DURATION_SECS` | Target queue duration (seconds) | `90` | ❌ |
| `DJALGORHYTHM_QUEUE_CHECK_INTERVAL_SECS` | Queue check interval (seconds) | `45` | ❌ |
| `DJALGORHYTHM_SERVER_PORT` | HTTP server port | `8080` | ❌ |
| `DJALGORHYTHM_LOG_LEVEL` | Logging level | `info` | ❌ |

### CLI Flags

```bash
djalgorhythm --help

Flags:
      --config string                  config file (default is .env)
      --log-level string              log level (debug, info, warn, error) (default "info")
      --telegram-enabled              enable Telegram integration (default true)
      --telegram-bot-token string     Telegram bot token
      --telegram-group-id int         Telegram group ID
      --whatsapp-enabled              enable WhatsApp integration
      --whatsapp-group-jid string     WhatsApp group JID
      --spotify-client-id string      Spotify client ID
      --spotify-playlist-id string    Spotify playlist ID
      --llm-provider string           LLM provider (openai, anthropic, ollama, none) (default "none")
      --server-port int               HTTP server port (default 8080)
      --confirm-timeout int           Confirmation timeout in seconds (default 120)
```

### Example .env File

```bash
# Chat Platform (choose one)
DJALGORHYTHM_TELEGRAM_ENABLED=true
DJALGORHYTHM_TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
# DJALGORHYTHM_TELEGRAM_GROUP_ID=-100xxxxxxxxxx  # Auto-detected on first run

# WhatsApp (disabled by default)
DJALGORHYTHM_WHATSAPP_ENABLED=false
DJALGORHYTHM_WHATSAPP_GROUP_JID=120363123456789@g.us

# Spotify (required)
DJALGORHYTHM_SPOTIFY_CLIENT_ID=your_spotify_client_id
DJALGORHYTHM_SPOTIFY_CLIENT_SECRET=your_spotify_client_secret
DJALGORHYTHM_SPOTIFY_PLAYLIST_ID=37i9dQZF1DX0XUsuxWHRQd

# LLM (optional)
DJALGORHYTHM_LLM_PROVIDER=openai
DJALGORHYTHM_LLM_API_KEY=sk-...
DJALGORHYTHM_LLM_MODEL=gpt-4o-mini

# General
DJALGORHYTHM_CONFIRM_TIMEOUT_SECS=120
DJALGORHYTHM_SERVER_PORT=8080
DJALGORHYTHM_LOG_LEVEL=info
```

## Development

### Project Structure

```
cmd/djalgorhythm/           # Main application
internal/
  ├── chat/           # Unified chat frontend interface
  │   ├── telegram/   # Telegram Bot API client
  │   └── whatsapp/   # WhatsApp client (whatsmeow)
  ├── core/           # Domain types and message dispatcher
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

# Test specific package
go test ./internal/chat/telegram/
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

- `djalgorhythm_messages_total` - Messages processed by type/status
- `djalgorhythm_adds_total` - Tracks added by source
- `djalgorhythm_duplicates_total` - Duplicate tracks rejected
- `djalgorhythm_llm_calls_total` - LLM API calls by provider/status
- `djalgorhythm_errors_total` - Errors by component/type
- `djalgorhythm_processing_duration_seconds` - Processing time histogram
- `djalgorhythm_playlist_size` - Current playlist track count
- `djalgorhythm_active_sessions` - Active message processing sessions

## Deployment

### Docker

```bash
# Build image
docker build -t djalgorhythm:latest .

# Run with environment file
docker run --env-file .env -p 8080:8080 djalgorhythm:latest

# Or with docker-compose
docker-compose up -d
```

### Production Considerations

- **Secrets**: Use proper secret management (not .env files)
- **Monitoring**: Set up Prometheus + Grafana dashboards
- **Logs**: Forward structured logs to your logging system
- **Backup**: Chat frontend sessions and Spotify tokens
- **Scaling**: Single instance recommended (chat sessions are stateful)
- **Compliance**: Be aware of chat platform ToS, especially for WhatsApp

## Troubleshooting

### Common Issues

**Telegram Bot Setup:**

```bash
# Check bot token is valid
curl "https://api.telegram.org/bot<TOKEN>/getMe"

# Verify bot is admin in group
# Check group ID is correct (negative number)
```

**WhatsApp QR Code Login:**

```bash
# Check logs for QR code output
./bin/djalgorhythm --log-level debug

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

### Debug Mode

```bash
# Enable debug logging
DJALGORHYTHM_LOG_LEVEL=debug ./bin/djalgorhythm

# Or with flag
./bin/djalgorhythm --log-level debug
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

- [go-telegram/bot](https://github.com/go-telegram/bot) - Telegram Bot API client
- [whatsmeow](https://github.com/tulir/whatsmeow) - WhatsApp Web multi-device client
- [zmb3/spotify](https://github.com/zmb3/spotify) - Spotify Web API wrapper
- [OpenAI](https://openai.com/) / [Anthropic](https://anthropic.com/) - AI disambiguation
- [Prometheus](https://prometheus.io/) - Monitoring and alerting

---

Made with ❤️ and 🎵 by the DJAlgoRhythm team
