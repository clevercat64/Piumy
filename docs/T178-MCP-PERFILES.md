# T178 — MCP en dos perfiles (operator / manager)

Un solo binario, una sola clave, tres puertas. Cada puerta es una vista del
mismo registro de tools (`internal/mcpserver/profiles.go`).

```mermaid
flowchart LR
    A1[agente que atiende chats] -->|Bearer key| OP["/mcp/operator"]
    A2[agente que configura Piumy] -->|Bearer key| MG["/mcp/manager"]
    A3[agente ya conectado] -->|Bearer key| ALL["/mcp (todo)"]
    OP --> AUTH
    MG --> AUTH
    ALL --> AUTH
    AUTH["RequireBearerToken (mount.go) — UNA clave"] --> MW
    MW["middlewares: errorlog + floodguard + levelgate"] --> REG
    REG[("registro de tools — NewSet (server.go)")]
    REG -.deriva por nombre.-> OPS["operatorTools"]
    REG -.deriva por nombre.-> MGS["managerTools"]
```

Reglas del reparto:
- Toda tool registrada está en `operatorTools` o `managerTools` (test testigo).
- Se solapan `get_status`, `get_manual` y el triage por chat
  (`set_chat_rules/status/active`, `set_mode`): el manual del operador ya los usa.
- `/mcp` no pierde nada: los agentes ya conectados siguen igual.
- `agent-connect.json` suma `mcp_operator_url` y `mcp_manager_url`.

Pasos siguientes (T178): sacar los candados de configuración, `get_config` /
`set_config`, `gateway_action`, manuales.
