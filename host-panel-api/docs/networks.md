# Private networks

Admin feature (no environment switch: always available) that lets hosted sites and managed service instances reach each other over named Docker networks, instead of one network shared by everything. A site only sees the instances on the networks it joins.

| Section | Content |
|---|---|
| [Concepts](#concepts) | what a network is, the `services` system network |
| [Creating and deleting](#creating-and-deleting-a-network) | name, subnet, limits |
| [Members](#members) | sites, instances, states, unmanaged containers |
| [Attaching and detaching](#attaching-and-detaching-a-member) | what happens to the container |
| [Booting without the panel](#booting-without-the-panel) | native Docker, no lock-in |
| [Address pools](#address-pools) | how subnets are allocated |
| [Limits](#limits-and-what-a-network-is-not) | what a network does not protect against |
| [Interfaces](#interfaces) | web, API, CLI, MCP |

## Concepts

- **Native Docker only.** A private network is a plain Docker `bridge` network created `internal` (no route to the outside, no Internet access) and labelled `sh.loch.managed=true`. Nothing else is needed to run it: the compose file of every member references it as `external`.
- **Names.** A network has a registry name (`[a-z]([a-z0-9-]{0,22}[a-z0-9])?`, at most 24 characters) and a Docker name, `loch-net-<name>`. The names `services`, `pub`, `default`, `host`, `none`, `bridge` and `traefik` are reserved.
- **The `services` system network.** The network shared with the managed services (`SERVICES_NETWORK`, `loch-services` by default) appears as the system network `services`: it is listed first, cannot be deleted (`NETWORK_SYSTEM`) and has no `sh.loch.network` label. It is the default network of new sites and of instances created without a network (see [Managed services](services.md#networks-of-an-instance)). It is registered when the services module starts; disabling the module afterwards keeps it (the sites and instances on it keep working).
- **Registry.** `DATA_DIR/networks.json` records, for each network, its name, Docker ID, subnet, gateway, description and creator. It is used by the panel to check that the live network is the one it created; the stacks never need it to run.
- **Membership lives with the member**, not in the registry:
  - a site lists its networks in the `x-loch.networks` section of its compose file ([Web hosting](hosting.md#networks));
  - an instance lists them in its private reference (`SERVICES_DATA_DIRECTORY/.loch/<project>/instance.json`, see [Managed services security](services-security.md#what-the-panel-trusts-on-disk)).

## Creating and deleting a network

| Field | Rule |
|---|---|
| `name` | see above |
| `description` | optional, 200 characters at most, no control characters; editable later |
| `subnet` | optional. Empty: Docker picks one in its [address pools](#address-pools). Otherwise an IPv4 network address in a private range (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`), prefix between `/16` and `/28` |

- A manual subnet that overlaps another Docker network is refused by Docker itself (`INVALID_SUBNET`). The subnet and the gateway of an existing network cannot be changed: create a new one and move the members.
- The panel never adopts an existing Docker network by name: if `loch-net-<name>` already exists, creation is refused (`NETWORK_NAME_TAKEN`).
- Names `config` and `docker` are reserved too (API paths).
- At most `NETWORKS_MAX` private networks (default 50, the system network does not count): `NETWORK_LIMIT_REACHED`.
- A network can be deleted only without members (`NETWORK_IN_USE`: detach them first, or [disconnect](#unmanaged-members) the unmanaged ones). Deleting a network that was replaced outside the panel only removes it from the registry: the Docker network is not ours and is left alone.

## Members

The detail of a network lists every container attached to it, plus the members declared on it that are not running.

| Kind | Meaning |
|---|---|
| `site` | a hosted site (name: the stack name) |
| `instance` | a managed service instance (name: its key, e.g. `postgres-17`, `elasticmq-jobs`) |
| `panel` | the API container itself, which joins a network temporarily for `loch proxy` |
| `unmanaged` | a container the panel does not manage, for example a stack deployed by CI |

A member is recognised by its **container name** (Docker keeps names unique: `svc-<…>` for an instance, `hosted-<name>-web-1` for a site), never by its labels: a container carrying the compose label of a site is shown as `unmanaged`, and can be disconnected.

Each member carries `expected` (declared in its compose file or reference) and `present` (connected to the Docker network now):

| expected | present | Reading |
|---|---|---|
| yes | yes | normal |
| yes | no | declared but not running on the network (stopped, or not deployed yet) |
| no | yes | connected without being declared: an unmanaged container, or a managed one attached by hand (`docker network connect`) |

### Network states

| State | Meaning |
|---|---|
| `ok` | the Docker network is the registered one |
| `missing` | it no longer exists in Docker |
| `compromised` | it exists but is not what the panel created |

A network is `compromised` when its Docker ID differs from the registry, it is not a local `bridge`, not `internal`, lacks the panel label or carries another network label, carries a compose project label (created by a stack), has IPv6 or a config-only/config-from setup, or no longer has the registered subnet. The reason is returned in `problem`. Attaching to such a network is refused (`NETWORK_COMPROMISED`), and the panel does not trust the containers of its members (see [verification](services-security.md#container-verification)).

At startup the panel reconciles the registry: a missing network is recreated with the same name, subnet and labels (the compose files of the members still apply; audit `network recreated`), and a replaced one is reported (audit `network compromised`, error in the logs).

### Unmanaged members

Any container able to talk to the Docker socket can join a network by name, so the panel shows what it does not manage instead of pretending it cannot happen. An admin can disconnect such a container (`POST /networks/{name}/disconnect`, `loch networks disconnect <network> <container>`). Refused for the panel container and for members declared on the network (`NETWORK_MEMBER_MANAGED`: detach them instead). A CI stack that joins the network again at its next deployment is connected again: remove the network from its compose file.

## Attaching and detaching a member

Both operations change the networks of the member and **recreate its container**: Compose recreates a container whose networks change. Only that container is affected, for a few seconds. A manual `docker network disconnect` is undone by the next `docker compose up`, which is why detaching goes through the panel.

| Member | Behaviour |
|---|---|
| Site | The compose file is rendered again and the site is redeployed like an update: asynchronous, the response carries the hosting `operation` id to follow on the stack. A site may have no network at all |
| Instance | Synchronous, may take up to a few minutes (health check). An instance keeps at least one network (`SERVICE_LAST_NETWORK`): without one, Compose would attach it to a default network with Internet access |

- **Detach cuts first.** A removed network is disconnected from the container before the recreation, so a failure cannot leave it connected. If the instance then does not restart without it, it is **stopped**, never reconnected (audit `service instance networks update failed`, outcome `stopped`). When only networks were added, a failure restores the previous files and restarts the instance with them (outcome `restored`).
- Attaching to a network the member is already on gives `NETWORK_ALREADY_ATTACHED`, detaching from one it is not on gives `NETWORK_NOT_ATTACHED`.
- An instance whose files were edited by hand ("modified") cannot be changed.

## Booting without the panel

Every compose header lists the exact command that creates its networks, once per host:

```text
# Generated by Loch.sh Panel (services). Boot without the panel with:
#   docker network create --internal --subnet 10.20.0.0/24 --label sh.loch.managed=true --label sh.loch.network=backend loch-net-backend   (once, if missing)
#   docker compose up -d
```

The `--subnet` part is written when the registry knows the subnet; the `sh.loch.network` label is omitted for the `services` network. On a fresh host, run the commands of the header, then `docker compose up -d`: the stack starts without the API, the registry or the web interface. The panel itself recreates a missing network on its next start.

## Address pools

Docker allocates a subnet when none is given, from the pools of the daemon (`default-address-pools` in `/etc/docker/daemon.json`). The default pools hold about 31 networks for the whole host, panel networks included. Extend them before creating many networks:

```json
{
  "default-address-pools": [
    { "base": "10.200.0.0/16", "size": 24 }
  ]
}
```

Restart the Docker daemon after changing it. Docker without a free pool makes creation fail with `INVALID_SUBNET` ("no free address pool"): choose a subnet by hand or extend the pools.

### Overlaps and reserved ranges

Docker refuses a subnet that overlaps another Docker network (`INVALID_SUBNET`, "overlaps another Docker network"), and never allocates one that does. It does not know the ranges used outside Docker: the LAN of the host, a VPN, static routes. List them in `NETWORKS_RESERVED_RANGES` (IPv4 CIDRs, at least /8):

- a manual subnet overlapping one is refused (`INVALID_SUBNET`, the range is named);
- a subnet Docker allocates in one is given back (the network is removed, `INVALID_SUBNET`): choose a subnet, or move `default-address-pools` away from it;
- a network already overlapping one (ranges added afterwards) is flagged (`reservedOverlap`), in the panel networks and in the other Docker networks.

`GET /networks/config` returns the rules: `{max, count, reservedRanges, privateRanges, minPrefix, maxPrefix}`.

## Other Docker networks

The networks the panel did not create are shown read-only, so that the whole addressing of the host is visible: Docker's own `bridge`, `host` and `none` (origin `docker`, listed last), the networks of compose stacks — the Traefik network, the stacks deployed by CI/CD (origin `compose`, with the project) — and the networks created by hand (origin `other`). For each: driver, internal or not, subnets, gateways, number of containers, overlap with a reserved range. The detail lists their containers with their addresses, recognised like the members of a panel network (a site or an instance is named, anything else is `unmanaged`). Nothing can be changed there: the panel only manages its own networks.

## Limits and what a network is not

- **Not an access control against CI or the Docker socket.** Any container can join a network by name, so a stack deployed by CI, or anything that can use the Docker socket, can join any network. This is accepted by design: the panel detects such members, shows them as `unmanaged` and lets an admin disconnect them. Do not treat a network as a secret boundary against whoever deploys stacks on the host.
- **Networks do not route to each other.** A container on several networks bridges them at the application level only (a site on `a` and `b` can talk to both); there is no `NET_ADMIN` and no forwarding.
- **No `NET_RAW`.** A hosted site on any panel network loses `NET_RAW` (no ARP spoofing of the traffic of the others).
- **No outbound access**: networks are `internal`; a container reaches the Internet only through its other, non-internal networks (for a site, the Traefik network).
- Traffic on a network is clear text unless the engine uses TLS (see [Managed services security](services-security.md#known-limitations)).
- IPv4 only.

## Interfaces

### Web interface

- **`/networks`** (Network menu), two tabs:
  - **Private networks**: state, members, reserved-range warning; deletion.
  - **Other Docker networks** (`?tab=docker`): read-only, with a detail page `/networks/docker/:id` listing the containers and their addresses.
- **`/networks/new`**: name, description, addressing (automatic, or a manual subnet checked against the private and reserved ranges of `GET /networks/config`), and optional members (sites and instances) attached right after the creation; the network page then follows the redeployment of the sites and reports the members that could not be attached.
- **`/networks/:name`**: information (description editable), members with their IPv4 and MAC, attach and detach, disconnection of the unmanaged containers.
- **Site page, "Networks" tab**: the networks of the site with its address on each, connect a network, disconnect one (the site is redeployed, a few seconds of downtime), and, read-only, the other Docker networks its container is on.
- The site form and the instance creation and edition pages have a network selector.

### CLI

```bash
loch networks ls [--all]                            # --all: also the other Docker networks
loch networks show backend
loch networks config                                # limit, private and reserved ranges
loch networks docker [ci_default]                   # other Docker networks (name, ID or ID prefix), read-only
loch networks create backend --subnet 10.20.0.0/24 --description "Shop"
loch networks update backend --description "Shop and its databases"
loch networks delete backend [--yes]
loch networks attach backend --site shop            # or --instance postgres-17
loch networks detach backend --instance postgres-17
loch networks disconnect backend ci-runner-1        # unmanaged container

loch hosting create shop --network backend --network services   # repeatable, replaces --services-network
loch hosting update shop --network backend
loch services create shop --engine postgres --version 17 --network backend
loch services networks postgres-17 --network backend --network services
loch services expose postgres-17 127.0.0.1:5417 [--tls-optional]
loch services unexpose postgres-17
```

Deleting asks for confirmation unless `--yes` is given. The `expose` commands are described in [Managed services](services.md#publishing-a-port).

### MCP

Tools `networks_list`, `networks_get`, `networks_config`, `networks_docker_list`, `networks_docker_get` (read-only), `networks_create`, `networks_attach` and `networks_detach`. There is no tool to publish a port: [exposure](services.md#publishing-a-port) is reserved to the web interface, the CLI and the API.

### REST API

Admin only; scopes `networks:read` and `networks:write`. Full details in the [API reference](api.md#private-networks).

| Method | Route | Description |
|---|---|---|
| GET | `/networks` | Networks with state and number of members |
| GET | `/networks/{name}` | Detail with the member list |
| POST | `/networks` | Create `{name, description?, subnet?}` (201) |
| PATCH | `/networks/{name}` | Change the description |
| DELETE | `/networks/{name}` | Delete (204) |
| POST | `/networks/{name}/members` | Attach `{kind, name}` |
| DELETE | `/networks/{name}/members/{kind}/{member}` | Detach |
| POST | `/networks/{name}/disconnect` | Disconnect an unmanaged container `{containerId}` (204) |
| PUT | `/services/instances/{instance}/networks` | Replace the networks of an instance |

### Audit events

`network created`, `network updated`, `network deleted`, `network recreated`, `network compromised`, `network member attached`, `network member detached`, `network container disconnected`, `service instance networks updated`, `service instance networks update failed`. See [Audit trail](audit.md).

## Configuration

| Variable | Description | Default |
|---|---|---|
| `NETWORKS_MAX` | Maximum number of private networks | `50` |
| `NETWORKS_RESERVED_RANGES` | IPv4 ranges used outside Docker (LAN, VPN, static routes) that no private network may overlap | empty |
| `SERVICES_NETWORK` | Docker name of the system network `services` | `loch-services` |

The other variables are in [Configuration](configuration.md).
