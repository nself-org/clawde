# Meridian Bridge Blueprint

The canonical Meridian bridge blueprint specification is maintained in the nSelf PPI documentation at:

**`.claude/docs/reference/meridian-bridge.md`** (in the nSelf project root)

This file links to that specification for ClawDE developers who need to reference the Meridian bridge contract.

---

## Quick Reference

**Service:** meridian-bridge (nSelf CS_N custom service slot)
**Port:** 3850 (127.0.0.1 only)
**ClawDE-exclusive:** Yes (ADR-001 — no other nSelf app uses this service)
**Protocol:** HTTP/1.1 with `X-Meridian-Version` negotiation header
**Auth:** `Authorization: Bearer <MERIDIAN_LOCAL_TOKEN>` on every request
**Health check:** `GET /health` → `{"status": "ok"|"degraded"|"error", ...}`
**Session attach:** `POST /bridge/attach` with `session_token` + `pty_session_id`

## Install (one-liner)

```bash
nself service add meridian-bridge \
  --image nself/meridian:1.0 \
  --port 3850 \
  --env MERIDIAN_LOCAL_TOKEN=$(openssl rand -hex 32) \
  --env CLAWDE_PTY_POOL_URL=localhost:4300
```

## Ops Runbook

See `.claude/docs/operations/meridian-ops.md` for full operations documentation including token rotation, log collection, and restart procedures.

## Architecture Notes

- Meridian is a P1 planned service — Go implementation target: `cli/internal/meridian/blueprint.go`
- ADR-001 (ClawDE Pivot): Meridian scope is ClawDE-only
- ADR-003 (MCP Policy): `MERIDIAN_LOCAL_TOKEN` scope must align with E3 MCP session auth scopes (P2 verification required)
