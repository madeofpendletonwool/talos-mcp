# Talos Linux Documentation MCP Server

A Model Context Protocol (MCP) server that provides AI models with real-time access to the latest Talos Linux documentation from the [Sidero Labs documentation repository](https://github.com/siderolabs/docs).

## Overview

This MCP server enables AI assistants to search and retrieve up-to-date Talos Linux documentation, including:
- Installation guides for various platforms (AWS, Azure, bare-metal, etc.)
- Configuration and networking documentation
- Security best practices
- Upgrade procedures
- Troubleshooting guides
- Version-specific documentation (v1.6 - v1.11+)

## Features

- **Full-Text Search**: Bleve-powered search across all Talos documentation
- **Version Filtering**: Search within specific Talos versions
- **Platform-Specific Docs**: Filter by cloud provider or hardware platform
- **Real-Time Updates**: Git-based synchronization with the upstream repository
- **Atomic Index Updates**: Zero-downtime documentation updates
- **5 MCP Tools**: Specialized tools for different documentation queries

## Installation

### Prerequisites

- Go 1.25.1 or later
- Git (for documentation synchronization)

### Build from Source

```bash
git clone <repository-url>
cd talos-mcp
go mod download
go build -o talos-mcp
```

## Usage

### Running the Server

The server communicates via stdio (standard input/output) as per the MCP specification:

```bash
./talos-mcp
```

### Configuration via Environment Variables

```bash
# Server configuration
export TALOS_MCP_NAME="talos-docs-mcp"
export TALOS_MCP_VERSION="1.0.0"

# Repository settings
export TALOS_MCP_REPO_URL="https://github.com/siderolabs/docs"
export TALOS_MCP_BRANCH="main"

# Search index location
export TALOS_MCP_INDEX_PATH="./data/search_index"

# Webhook secret (if using webhooks)
export TALOS_MCP_WEBHOOK_SECRET="your-secret-here"

# Logging
export TALOS_MCP_LOG_LEVEL="debug"  # Options: debug, info, warn, error

# Run the server
./talos-mcp
```

### Integrating with Claude Desktop

Add to your Claude Desktop configuration file:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`
**Linux**: `~/.config/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "talos-docs": {
      "command": "/path/to/talos-mcp",
      "env": {
        "TALOS_MCP_LOG_LEVEL": "info",
        "TALOS_MCP_INDEX_PATH": "/path/to/index"
      }
    }
  }
}
```

## Available MCP Tools

### 1. `search_talos_docs`

Search across all Talos Linux documentation.

**Parameters:**
- `query` (string, required): Search query
- `version` (string, optional): Talos version filter (e.g., "v1.8", "v1.11")
- `section` (string, optional): Section filter (e.g., "getting-started", "networking", "security")
- `platform` (string, optional): Platform filter (e.g., "aws", "azure", "bare-metal")
- `limit` (number, optional): Maximum results (default: 20)

**Example:**
```json
{
  "query": "kubernetes upgrade",
  "version": "v1.8",
  "limit": 10
}
```

### 2. `get_talos_guide`

Retrieve a complete guide for a specific Talos topic.

**Parameters:**
- `topic` (string, required): Guide topic (e.g., "quickstart", "networking", "upgrading")
- `version` (string, optional): Talos version
- `platform` (string, optional): Platform-specific variant

**Example:**
```json
{
  "topic": "quickstart",
  "platform": "aws"
}
```

### 3. `compare_talos_versions`

Compare documentation across different Talos versions to identify changes.

**Parameters:**
- `topic` (string, required): Topic to compare
- `from_version` (string, required): Starting version
- `to_version` (string, required): Target version

**Example:**
```json
{
  "topic": "networking",
  "from_version": "v1.7",
  "to_version": "v1.8"
}
```

### 4. `get_platform_specific_docs`

Get platform-specific installation and configuration guides.

**Parameters:**
- `platform` (string, required): Cloud platform or hardware (e.g., "aws", "azure", "raspberry-pi")
- `version` (string, optional): Talos version

**Example:**
```json
{
  "platform": "aws",
  "version": "v1.8"
}
```

### 5. `get_latest_release_notes`

Get the latest Talos release information and what's new.

**Parameters:** None

## Architecture

### Components

1. **Documentation Fetcher** (`fetcher.go`)
   - Clones/updates Sidero Labs docs repository
   - Parses MDX/MD files and navigation structure
   - Extracts metadata (version, platform, tags)
   - Background sync with exponential backoff

2. **Search Engine** (`search.go`)
   - Bleve full-text search indexing
   - Atomic index swapping for zero-downtime updates
   - Content taxonomy (versions, sections, platforms, tags)
   - Snippet and context extraction

3. **MCP Server** (`server.go`)
   - Implements 5 specialized documentation tools
   - Handles MCP protocol communication via stdio
   - Request validation and error handling

4. **Data Models** (`models.go`)
   - Document structure with metadata
   - Search request/response types
   - Configuration schema

### Directory Structure

```
talos-mcp/
├── main.go           # Entry point and configuration loading
├── server.go         # MCP server implementation
├── fetcher.go        # Documentation fetching and parsing
├── search.go         # Search engine with Bleve
├── models.go         # Data structures
├── go.mod            # Go module dependencies
├── go.sum            # Dependency checksums
└── data/
    └── search_index/
        ├── active/   # Active search index
        ├── staging/  # Staging index for updates
        └── backup/   # Backup of previous index
```

## Data Flow

1. **Initialization**:
   - Clone Talos docs repository to temp directory
   - Parse `docs.json` navigation structure
   - Extract and index all MDX/MD documents
   - Build search index with Bleve

2. **Query Handling**:
   - Receive MCP tool request via stdio
   - Parse and validate parameters
   - Execute search with filters
   - Format and return JSON results

3. **Background Updates**:
   - Poll git repository for changes (5-30 min intervals)
   - On changes detected: pull latest, re-index documents
   - Atomic swap: staging → active (zero downtime)
   - Old index moved to backup

## Development

### Project Structure

```bash
go build ./...              # Build all packages
go test ./...               # Run all tests
go fmt ./...                # Format code
go vet ./...                # Static analysis
go mod tidy                 # Clean dependencies
```

### Running in Development

```bash
# Enable debug logging
export TALOS_MCP_LOG_LEVEL=debug

# Use custom index path
export TALOS_MCP_INDEX_PATH=./dev_index

# Run server
go run .
```

### Testing with MCP Inspector

Use the [MCP Inspector](https://github.com/modelcontextprotocol/inspector) to test the server:

```bash
npx @modelcontextprotocol/inspector go run .
```

## Configuration Reference

### Default Configuration

```yaml
server:
  name: "talos-docs-mcp"
  version: "1.0.0"

repository:
  url: "https://github.com/siderolabs/docs"
  branch: "main"

sync:
  mode: "hybrid"  # polling, webhook, or hybrid
  polling:
    interval: "5m"
    backoff_max: "30m"
  health_check:
    stale_threshold: "1h"

search:
  index_path: "./data/search_index"
  max_results: 20
  snippet_length: 300

cache:
  ttl: "24h"
  max_size: "1GB"

logging:
  level: "info"
  format: "json"
```

## Known Limitations

1. **Version Sorting**: Uses simple string comparison (not semantic versioning)
2. **Search Filtering**: Manual post-search filtering (not optimal for large result sets)
3. **No Webhook Handler**: Polling-only for now (webhook support planned)
4. **Single Repository**: Only supports Sidero Labs docs repository
5. **No Persistent Cache**: Documents re-indexed on every restart

## Roadmap

- [ ] Implement GitHub webhook handler for instant updates
- [ ] Add semantic version comparison
- [ ] Optimize search with Bleve query composition
- [ ] Add persistent document cache
- [ ] Support multiple documentation repositories
- [ ] Implement usage metrics and analytics
- [ ] Add health check endpoint
- [ ] Kubernetes deployment manifests

## Troubleshooting

### Server won't start

**Issue**: `failed to clone repository`
**Solution**: Check network connectivity and repository URL

**Issue**: `failed to create search index`
**Solution**: Ensure index path is writable: `chmod -R 755 ./data`

### Search returns no results

**Issue**: Index may be empty or corrupted
**Solution**: Delete index and restart:
```bash
rm -rf ./data/search_index
./talos-mcp
```

### High memory usage

**Issue**: Large documentation set in memory
**Solution**: Reduce `max_results` or implement pagination

## Contributing

Contributions welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Write tests for new functionality
4. Submit a pull request

## License

[Add your license here]

## Acknowledgments

- [Sidero Labs](https://www.siderolabs.com/) for Talos Linux and documentation
- [Anthropic](https://www.anthropic.com/) for the Model Context Protocol
- [Bleve](https://blevesearch.com/) for full-text search capabilities
