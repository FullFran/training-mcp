# ChatGPT setup

## Public connector

1. Run the service behind a public HTTPS endpoint.
2. Set a long random `MCP_BEARER_TOKEN` outside source control.
3. Configure the connector URL as `https://your-host.example/mcp`.
4. Configure the connector's bearer credential with the same token.
5. Exercise `start_session`, `add_set`, and `get_session` before using corrections or deletion.

ChatGPT integration details change. Follow the current [OpenAI MCP documentation](https://platform.openai.com/docs/mcp) and [MCP specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports).

## Local development

Local use requires an HTTPS-capable MCP tunnel or a local MCP host. The tunnel command is intentionally not reproduced here because tunnel products and command syntax are external and versioned. See `connection-paths.md`.
