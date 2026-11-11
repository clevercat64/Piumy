# El MCP oficial de WhatsApp (Meta) y qué significa para Piumy

Investigado el 2026-11-11 a pedido del boss («dijeron que WhatsApp tiene MCP ahora»).
Fuente principal verificada por Citrino; el resto, con su fuente, lo reunió un subagente.

## Qué existe

- **WhatsApp Business Tools MCP**, anunciado por Meta el 15-sep-2026:
  [anuncio](https://developers.facebook.com/blog/post/2026/09/15/whatsapp-business-messaging-mcp-ai-agent/),
  [guía](https://developers.facebook.com/documentation/mcp/whatsapp-business-tools-mcp).
- Solo para el **WhatsApp Business Platform (Cloud API)**. No hay MCP para WhatsApp personal
  ni para la app WhatsApp Business.
- Meta lo declara «para desarrollo y pruebas, no para envío de producción a escala»: alta y
  verificación de números, plantillas, webhooks, mensajes de prueba. Autenticación OAuth con
  cuenta de desarrollador de Meta.

## Qué no cambia

- Las reglas de la Cloud API siguen: verificación del negocio, ventana de 24 h para texto
  libre, plantillas aprobadas fuera de ella, y un número dedicado (pasar un número normal a la
  Cloud API lo saca de la app).
- **Términos de la plataforma, sección 4.7 «AI Providers»** (vigente desde el 15-ene-2026 para
  todos): prohibido cuando la IA de propósito general es la funcionalidad principal; los bots
  de soporte, ventas y avisos siguen permitidos.
  [Términos](https://www.facebook.com/legal/Meta-Terms-for-WhatsApp-Business-Platform).
  En el EEE hubo medidas de la Comisión Europea en 2026 (sin confirmar cómo terminaron).

## Qué significa para Piumy

- **No reemplaza a Piumy**: no cubre la cuenta vinculada (whatsmeow) ni las herramientas MCP
  de Piumy para agentes.
- **Complementa F6** (adaptador Cloud API, post-MVP): sirve para configurar ese adaptador; el
  tráfico real iría por la Cloud API directa. Antes de construir F6, revisar la 4.7: un
  adaptador que exponga un asistente de propósito general podría quedar prohibido.
- Recordatorio que trajo la investigación: los MCP de WhatsApp de la comunidad (mismo enfoque
  que Piumy) son vulnerables a inyección de instrucciones desde los chats
  ([Invariant Labs, 2025](https://invariantlabs.ai/blog/whatsapp-mcp-exploited)). Piumy es
  permisivo a propósito; el riesgo es de quien conecta el agente.
