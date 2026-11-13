# Loch.sh Panel API: documentation

Start with the page matching what you want to do.

## Run the panel

| Page | Read it to |
|---|---|
| [Production examples](../../example/README.md) | install the panel on a server: Docker Compose stacks with Traefik, SFTPGo and deploy-server |
| [Configuration](configuration.md) | set the environment variables (authentication, storage, plugins) |
| [Web hosting](hosting.md) | let admins create PHP / static sites from the panel |
| [SFTP users](sftp.md) | give site owners SFTP access through SFTPGo |
| [Managed services](services.md) | offer databases, caches and message brokers to the sites |
| [Private networks](networks.md) | decide which sites reach which instances, with native internal Docker networks |
| [Functions](functions.md) | run handlers written for AWS Lambda™ (work in progress) |
| [Audit trail](audit.md) | know what is recorded and how to consult it |

## Understand the security

| Page | Content |
|---|---|
| [Security](security.md) | authentication, authorization, CSRF, HTTP hardening, storage |
| [Managed services security](services-security.md) | tenant isolation, CI threat model, networks and exposure, hardening of every engine |
| [Functions security](functions-security.md) | code upload validation, runtime binary trust, container hardening and verification (work in progress) |

## Use or extend the API

| Page | Content |
|---|---|
| [API reference](api.md) | REST routes, WebSocket messages, API key scopes |
| [Development](development.md) | build, tests, project structure |
| [Services design decisions](design/services-decisions.md) | history of the decisions behind the services module |

The web interface and the `loch` CLI have their own README in their repositories.

## Vocabulary

| Term | Meaning |
|---|---|
| **Stack** | A Docker Compose project, as shown in the container list |
| **Hosted site** | A web site created by the hosting plugin: a stack named `hosted-<name>` |
| **Site directory** | A directory of files under the sites directory, shared by SFTP users and used by a hosted site |
| **Resource** | What a managed service hands to a site: a database, a RabbitMQ virtual host, or a dedicated instance |
| **Shared instance** | One container per engine version, holding the resources of every site (databases, RabbitMQ) |
| **Dedicated instance** | One container per resource, sized when it is created (Redis, Valkey, NATS) |
| **Private network** | A named internal Docker network created by the panel; sites and instances on a common network can reach each other |
| **System network** | The network `services` (`SERVICES_NETWORK`): shared with the services module, cannot be deleted |
| **Exposure** | The host address and port an instance is published on, chosen in the panel within the limits set in the environment |
| **Restricted user** | A user who only sees the containers an admin granted to them |
