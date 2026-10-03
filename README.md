# mcpit

Make any website usable by an agent as an MCP server, with no change to the site.

- The first visit to a site is slow: mcpit explores it and saves a **sitepack** (forms, search and API endpoints; no user data).
- Later visits use the sitepack and are fast.
- A shared **registry** controls sitepack versions, validates submits and decides which version is best.

Status: planning. See [PLAN.md](PLAN.md) (plan v3) and [docs/plan.html](docs/plan.html).

The first Chrome extension prototype (MCPfier 0.1) is parked in [legacy/extension](legacy/extension).
