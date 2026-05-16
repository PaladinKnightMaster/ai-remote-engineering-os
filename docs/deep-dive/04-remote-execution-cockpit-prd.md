# PRD - Project A: Remote Execution Cockpit

## Problem

Companies with restricted networks need mobile/browser access to internal PCs, GUI workspaces, terminals and demos without exposing RDP/SSH/VNC directly and without relying on SaaS runtime infrastructure.

## Users

- Employee needing phone/browser access to a workstation.
- IT/support engineer managing internal machines.
- Manager/demo presenter sharing read-only internal session.
- Security/admin reviewing audit logs.
- Future AI operator observing/approving agent work.

## MVP scope

### Must-have

- SSO/local login.
- LAN/VPN/DMZ deployment profiles.
- Resource registry.
- Browser GUI session via Guacamole.
- Mobile-friendly remote viewer.
- Terminal session via xterm.js + tmux.
- Read-only demo share.
- Session audit trail.
- Policy-gated clipboard/file/control.
- Admin kill switch.
- Offline install bundle.

### Should-have

- MeshCentral adapter.
- Session recording metadata.
- Watermarking.
- Health dashboard.
- Basic backup/restore.

### Out of scope for MVP

- Custom WebRTC host agent.
- Native mobile app.
- Autonomous AI.
- Full Jira/Confluence/GitHub replacement.

## Key workflows

### Start GUI session

1. User logs in.
2. User selects resource.
3. Policy checks permission.
4. Session broker creates Guacamole/Mesh session.
5. User joins browser viewer.
6. Audit events are emitted.

### Share demo

1. User opens active session.
2. User creates read-only link.
3. Expiry/passcode/watermark configured.
4. Viewer joins observe-only mode.
5. Owner/admin can revoke.

### Emergency control

1. User requests control.
2. Owner/admin approves if required.
3. Control token issued.
4. All input events are audited at coarse-grained level.
5. Admin kill switch can terminate session.

## Acceptance metrics

- Start RDP/VNC session from phone in under 30 seconds in lab.
- Terminal reconnect preserves running job.
- Demo link cannot send input.
- Security scan confirms no direct RDP/SSH/VNC exposure.
- Offline install succeeds with no internet.
