# Network Topologies

## Topology A - LAN-only

Used when the user device is on company Wi-Fi/LAN.

```mermaid
flowchart LR
    Phone[Phone on company Wi-Fi] --> Cockpit[cockpit.company.lan]
    Cockpit --> Internal[Internal PCs / Workspaces]
```

Use for labs, factories, internal demos and offline sites.

## Topology B - VPN-first

Used when external users can join the company private network through a self-hosted VPN or mesh.

```mermaid
flowchart LR
    User[External phone/laptop] --> VPN[Company VPN / NetBird / Headscale / WireGuard]
    VPN --> Cockpit[Internal Cockpit URL]
    Cockpit --> Resources[Internal resources]
```

Recommended for trusted employees and managed devices.

## Topology C - DMZ-first

Used when users should not receive full network access. They access one gateway, which reaches internal services through a controlled route or outbound connector.

```mermaid
flowchart LR
    User[External browser] --> DMZ[Self-hosted DMZ Gateway]
    DMZ --> Connector[Internal Connector]
    Connector --> Cockpit[Control Plane]
    Cockpit --> Resources[Internal PCs / Workspaces]
```

Recommended default for product UX: no VPN client for simple browser access, less broad network exposure.

## Rule

If there is no network path at all, remote operation is impossible. The missing path must be created with company VPN, DMZ gateway, private WAN, private APN/cellular, branch relay or direct LAN/Wi-Fi.

## MVP deployment profiles

- `lan-only`
- `vpn-first`
- `dmz-first`

All documentation and installers must identify which profile is active.
