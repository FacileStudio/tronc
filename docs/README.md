# tronc — Documentation

| Page | What's in it |
|---|---|
| [Architecture](architecture.md) | Middleware order, request lifecycle, the defects this replaces |
| [Configuration](configuration.md) | Every environment variable the code reads, and its traps |
| [Development](development.md) | Local setup, the quality gate, CI, versioning |
| [API](api.md) | Every exported symbol, package by package |

`tronc` is a library. It ships no service and has no deployment of its own — it is deployed
by whatever app imports it, which is why there is no `deployment.md` here.

Release history lives in [CHANGELOG.md](../CHANGELOG.md), at the repo root.

Back to the [README](../README.md).
