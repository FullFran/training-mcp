# Connection paths

| Path | TLS | Authentication | Status |
|---|---|---|---|
| Local process | No | Bearer enabled by default | Useful for local tests |
| Tunnel-only development | Provider-dependent | `MCP_AUTH_DISABLED=1` is permitted only here | External smoke unverified |
| Public deployment | Required | Bearer required | Production boundary |

The server does not implement a tunnel, TLS termination, OAuth, JWT, or connector provisioning. Use the official [MCP transport documentation](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports) and your selected tunnel provider's current documentation. Never expose the bypass mode on a public address.

A connection that reaches the server over a loopback address must carry a loopback `Host`, or `/mcp` answers `403 Forbidden: invalid Host header`. This is the MCP SDK's DNS rebinding protection. A tunnel agent or proxy on the same machine must therefore rewrite `Host` to `localhost`. A reverse proxy reaching the server over a container network is not affected.

To accept OAuth access tokens on `/mcp`, set `MCP_OAUTH_ISSUER`, `MCP_OAUTH_RESOURCE` (this server's public `/mcp` URL) and `MCP_OAUTH_SUBJECT` (the one account allowed in) together. The server only verifies tokens: login and issuance belong to the provider, which must issue RS256 JWTs whose `aud` is the resource. The static bearer token keeps working alongside them, and the web UI stays behind it. Leaving all three empty leaves OAuth off.

Health is `GET /health`; the MCP Streamable HTTP endpoint is `/mcp`. There are no REST or GraphQL routes.
