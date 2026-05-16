# Offline and Self-Hosted Deployment

## Runtime constraint

The product must be deployable in restricted networks. Development can use internet; production runtime cannot require SaaS or public APIs for Project A.

## Deployment profiles

### LAN-only

All users and services are inside the same private network.

### VPN-first

External users join company network through self-hosted VPN/mesh.

### DMZ-first

Users connect to a hardened DMZ gateway. The gateway reaches internal services through controlled routes or outbound connectors.

## MVP stack

| Layer | Candidate |
|---|---|
| Reverse proxy | Caddy or Traefik |
| Identity | Keycloak or Authentik |
| DB | PostgreSQL |
| Events/cache | Redis or NATS |
| Object storage | MinIO |
| GUI gateway | Apache Guacamole |
| Device management | MeshCentral |
| Terminal | xterm.js + tmux |
| VPN profile | NetBird / Headscale / WireGuard |
| DMZ profile | Pangolin / OpenZiti |
| Logs | Loki/OpenSearch |
| Registry | Harbor |

## Offline release bundle

```text
release-bundle/
  docker-images/
  helm-chart/
  docker-compose.yml
  deb-packages/
  rpm-packages/
  npm-cache/
  python-wheels/
  go-mod-cache/
  browser-binaries/
  model-files/
  sbom/
  checksums/
  signatures/
  install.sh
  upgrade.sh
  backup.sh
  restore.sh
```

## Release gate

No release is accepted until:

- deployed in an isolated test network
- all services start without internet
- image checksums pass
- SBOM generated
- backup/restore tested
- demo RDP/VNC/SSH target works
- audit logs persist
- docs include offline runbook
