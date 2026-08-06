# Connection paths

| Path | TLS | Authentication | Status |
|---|---|---|---|
| Local process | No | Bearer enabled by default | Useful for local tests |
| Tunnel-only development | Provider-dependent | `MCP_AUTH_DISABLED=1` is permitted only here | External smoke unverified |
| Public deployment | Required | Bearer required | Production boundary |

The server does not implement a tunnel, TLS termination, OAuth, JWT, or connector provisioning. Use the official [MCP transport documentation](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports) and your selected tunnel provider's current documentation. Never expose the bypass mode on a public address.

Health is `GET /health`; the MCP Streamable HTTP endpoint is `/mcp`. There are no REST or GraphQL routes.
