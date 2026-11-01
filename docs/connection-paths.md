# Connection paths

| Path | TLS | Authentication | Status |
|---|---|---|---|
| Local process | No | Bearer enabled by default | Useful for local tests |
| Tunnel-only development | Provider-dependent | `MCP_AUTH_DISABLED=1` is permitted only here | External smoke unverified |
| Public deployment | Required | Bearer required, OAuth access tokens optional | Production boundary |

The server does not implement a tunnel, TLS termination, an OAuth authorization server, or connector provisioning. Use the official [MCP transport documentation](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports) and your selected tunnel provider's current documentation. Never expose the bypass mode on a public address.

A connection that reaches the server over a loopback address must carry a loopback `Host`, or `/mcp` answers `403 Forbidden: invalid Host header`. This is the MCP SDK's DNS rebinding protection. A tunnel agent or proxy on the same machine must therefore rewrite `Host` to `localhost`. A reverse proxy reaching the server over a container network is not affected.

To accept OAuth access tokens on `/mcp`, set `MCP_OAUTH_ISSUER`, `MCP_OAUTH_RESOURCE` (this server's public `/mcp` URL) and `MCP_OAUTH_SUBJECT` (the owner, who holds the data from before accounts existed) together. The server only verifies tokens: login and issuance belong to the provider, which must issue RS256 JWTs whose `aud` is the resource. The static bearer token keeps working alongside them, and the web UI stays behind it. Each token acts as its own user: the static token, the web UI and `MCP_AUTH_DISABLED=1` act as the owner. Any other subject is refused unless it already has an account or `MCP_OAUTH_PROVISION=1` is set, which gives every subject the provider vouches for an account of its own, so only set it once the provider admits invited people alone. Leaving all three empty leaves OAuth off, and combining them with `MCP_AUTH_DISABLED=1` stops the server from starting.

Health is `GET /health`; the MCP Streamable HTTP endpoint is `/mcp`. There are no REST or GraphQL routes.
