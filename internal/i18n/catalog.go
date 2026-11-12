package i18n

// esCatalog / enCatalog (T153, ct-2026-09-08-1656): the text tables behind
// GET /api/i18n. esCatalog's values are the EXACT strings that lived
// hardcoded before each pass — letter for letter, so a chat in Spanish
// looks identical to before.
//
// Etapa 2a: index.html's static markup (textContent + placeholder/title/
// aria-label/alt attributes), applied declaratively by app.js's
// applyI18n() over [data-i18n*] elements.
//
// Etapa 2b-i: the shared vocabulary for text app.js ASSEMBLES at runtime
// (status badges, the hero's connect/disconnect prompt, save/error
// messages) — applied via app.js's t(key, vars). Named by what the text
// IS (action.saving/action.saved/error.prefix), not by which screen calls
// it, so 2b-ii/iii/iv reuse these instead of creating near-duplicates.
// Text with a number inside stays ONE template entry with a {var} hole —
// never split into translatable fragments, since word order isn't the
// same across languages (see badge.backup_summary).
//
// Etapa 2b-ii: login and recovery (auth.*) — the only screens that run
// BEFORE a session exists. Translate WITHOUT changing what a message
// reveals: auth.invalid_credentials never says which of username/password
// was wrong, auth.invalid_or_expired_code covers both cases with the same
// text, auth.code_sent_generic is identical on success and failure (the
// backend already answers the same either way, on purpose, to not leak
// whether an account/method exists) — in EVERY language, not just Spanish.
//
// Etapa 2b-iii: agents (cards, create, delete, approver). "Principal" is
// translated as "Primary" throughout (agent.role_principal,
// agent.principal_prefix) — an English reader would parse "Principal" as
// the school-administrator sense first, "Primary" reads unambiguously as
// "the main one" instead.
//
// Etapa 2b-iv (last dashboard pass): chats, groups, contacts, drafts. The
// data/interface line is strict here — chat/group/contact names and message
// content are NEVER translated, only empty states/headers/labels (rule of
// thumb: if it changes depending on who wrote it or who it's with, it's
// data). Completes LEVELS: 2b-iii only touched .short (via
// originLevelShort); this pass adds levelLabel(key, fallback) for .label,
// same if-chain pattern, same reason (a key stored only as an object VALUE
// is invisible to TestAllKeysMatchBetweenFrontendAndCatalog's regex scan).
// Two more object-value traps found and fixed the same way:
// RULES_SOURCE_LABEL and SEARCHABLE_TABS in app.js, both replaced by
// literal-t() if-chain functions. Checked every new key against the whole
// catalog before adding it (Citrino: the test catches an unused key, never
// two different keys sharing one exact text) — three of
// RULES_SOURCE_LABEL's four values turned out to be EXACT letter-for-letter
// matches of existing keys (tab.groups, rules.new_messages_label,
// rules.contacts_label) and were reused instead of duplicated.
//
// Both maps MUST carry the same key set — see TestCatalogsAreComplete AND
// TestAllKeysMatchBetweenFrontendAndCatalog (catalog_usage_test.go).
var esCatalog = map[string]string{
	// Alertas
	"alert.factory_password": "⚠️ El tablero sigue con la contraseña de fábrica — cualquiera en tu red la conoce.",
	"alert.no_terminal":      "⚠️ No hay un terminal por defecto configurado — los mensajes del dueño no se van a poder despachar hasta que configures la antena o PIUMY_DEFAULT_TERMINAL_ID.",

	// Hero
	"hero.connect_qr":   "Conectar QR",
	"hero.edit_profile": "Editar perfil",
	"hero.disconnect":   "Desconectar",
	"stats.queue":       "cola:",
	"stats.sent":        "enviados:",
	"stats.agents":      "agentes:",

	// Barra de estado
	"badge.antenna":   "Antena",
	"badge.encrypted": "Cifrado",
	"badge.history":   "Historial",
	"badge.privacy":   "Privacidad",

	// Secciones de la barra. "Chats" queda IDÉNTICA en los dos idiomas
	// (T165, ct-2026-09-16-2016) — préstamo inglés ya establecido en este
	// tablero, mismo criterio que agent.label_endpoint/label_pin más abajo.
	// "Drafts" pasó a "Borradores" en T197: es como la llamaba el demo 1 que
	// eligió el boss.
	"tab.chats":    "Chats",
	"tab.groups":   "Grupos",
	"tab.contacts": "Contactos",
	"tab.agents":   "Agentes",
	"tab.rules":    "Reglas",
	"tab.drafts":   "Borradores",

	"sort.label":   "Ordenar por:",
	"sort.recent":  "recientes",
	"sort.level":   "nivel",
	"sort.ignored": "ignorados",

	"table.conversation": "Conversación",
	"table.level":        "Nivel",
	"table.agent":        "Agente",

	"legend.auto_reply":        "respuesta automática",
	"legend.with_confirmation": "con tu confirmación",
	"legend.unattended":        "sin atender",

	// Pestaña Reglas
	"rules.identity_label":     "Identidad",
	"rules.identity_hint":      `(rige el tono de las tres reglas de abajo — "asistente de qué")`,
	"rules.legend":             "Cada chat sigue una sola de estas reglas: los de grupo, Grupos; los 1 a 1 con alguien de tu agenda, Contactos del celular; todos los demás, Mensajes nuevos.",
	"rules.groups_hint":        "(todo chat de grupo sin reglas propias)",
	"rules.new_messages_label": "Mensajes nuevos",
	"rules.new_messages_hint":  "(números desconocidos)",
	"rules.contacts_label":     "Contactos del celular",
	"rules.contacts_hint":      "(agenda, pisa a Mensajes nuevos)",
	"rules.boss_hint":          "(tu propio chat: no lleva modo ni reglas)",
	"rules.unsaved":            "Cambios sin guardar",

	// Footer — el nombre del dueño NO va acá (es dato, no interfaz; queda
	// en el marcado como nodo aparte, ver index.html).
	"footer.thanks":  "Gracias por usar Piumy.",
	"footer.made_by": "Hecho por",

	// Popup de conversación
	"phone.view_chat":       "Ver chat",
	"phone.member_list":     "Lista de miembros",
	"phone.readonly_notice": "solo lectura · responder no está disponible todavía",

	// Acciones genéricas (reusadas en varios modales)
	"action.save":               "Guardar",
	"action.cancel":             "Cancelar",
	"action.close":              "Cerrar",
	"action.confirm":            "Confirmar",
	"action.change_password":    "Cambiar contraseña",
	"action.save_identity":      "Guardar identidad",
	"action.save_rules":         "Guardar reglas",
	"action.save_email":         "Guardar correo",
	"action.save_photo":         "Guardar foto",
	"action.save_status":        "Guardar estado",
	"action.reject":             "Rechazar",
	"action.delete_agent":       "Borrar agente",
	"action.confirm_disconnect": "Sí, desconectar",
	"action.sign_in":            "Entrar",
	"action.send_whatsapp":      "Enviar por WhatsApp",
	"action.send_email":         "Enviar por correo",
	"action.reset_password":     "Restablecer",
	"action.refresh_qr":         "🔄 Refrescar",

	// modal: editar reglas/memoria/contexto
	"modal.contact_name_label": "Nombre de contacto",
	"modal.edit_rules_label":   "Reglas (rules)",
	"modal.edit_memory_label":  "Memoria (memory)",
	"modal.edit_context_label": "Contexto (context)",

	// modal: editar/rechazar draft
	"modal.draft_text_label":  "Texto",
	"modal.draft_reject_hint": "El motivo le llega al agente junto con el mensaje original, para que redacte de nuevo.",
	"label.reason":            "Motivo",

	// modal: borrar agente / aprobador
	"titlebar.delete_agent":        "borrar agente",
	"titlebar.approver_permission": "permiso de aprobar",

	// modal: config (Opciones)
	"modal.settings_title":         "Configuración ⚙",
	"label.current_password":       "Contraseña actual",
	"label.new_password":           "Nueva contraseña",
	"label.confirm_password":       "Repetir contraseña",
	"modal.recovery_email_heading": "Correo de recuperación",
	"modal.dispatch_wait_heading":  "Espera antes de despachar",
	"modal.dispatch_wait_hint1":    "Cuánto espera el gateway, en silencio, antes de pasarle los mensajes al agente — así llegan agrupados en vez de uno por uno. 0 = despachar apenas llega cada mensaje.",
	"label.wait_seconds":           "Espera (segundos)",
	"modal.dispatch_wait_hint2":    "Si el chat no para de escribir, la espera de arriba se reinicia con cada mensaje — sin este límite, el agente podría no recibir nada nunca. Como mucho, esto es lo que espera desde el primer mensaje sin contestar antes de despachar igual.",

	// Reportes de drift (T174a, ct-2026-11-10-210158; el tablero los muestra
	// desde T172b tramo 4b). Cada frase de "qué sale" y "qué nunca sale"
	// describe lo que el código de internal/driftreport ya hace cumplir —
	// vocabularios cerrados, sin campo de texto libre. Si ese código cambia,
	// estos textos cambian con él: son la divulgación que el dueño pidió ver
	// junto al interruptor, no un texto de relleno.
	"modal.drift_heading":     "Reportes de drift",
	"modal.drift_intro":       "Cuando lo que Piumy cree difiere de lo que pasa de verdad —despachos retenidos porque nadie puede recibirlos, WhatsApp desconectado mucho rato, una antena que no responde— puede mandar un aviso corto a los desarrolladores. Es voluntario: viene apagado y solo sale si lo activas aquí.",
	"modal.drift_sent_label":  "Qué sale",
	"modal.drift_sent":        "El tipo de problema, cuántas veces ocurrió, cuánto duró, la versión de Piumy, el sistema operativo y la arquitectura, y un identificador aleatorio de esta instalación.",
	"modal.drift_never_label": "Qué nunca sale",
	"modal.drift_never":       "Ningún número de teléfono, nombre, contacto, mensaje ni chat, ni rutas de tu disco, ni claves. Esto está fijado en el código del programa: no depende de esta pantalla.",
	"modal.drift_limits":      "Como máximo 3 al día, y uno de cada tipo cada 6 a 9 horas. Si el envío falla, se descarta.",
	"modal.drift_destination": "Destino",
	"modal.drift_switch":      "Enviar reportes de drift a los desarrolladores",
	"label.ceiling_seconds":   "Techo (segundos)",
	"label.language":          "Idioma",
	"modal.language_hint":     `Idioma del tablero y de los avisos que manda Piumy. "Automático" usa el idioma del sistema operativo.`,
	"option.auto_language":    "Automático (idioma del sistema)",

	// Auto-actualización (T176, ct-2026-11-10-2103; el tablero la muestra
	// desde T182, ct-2026-11-11-0152). El servidor manda códigos y datos, no
	// frases: la UI arma cada estado con estas claves, porque el orden de las
	// palabras cambia entre idiomas. modal.update_state_off NO viene del
	// fragmento de T176 — la agregó T182 para que la frase no prometa una
	// instalación automática cuando el dueño apagó el interruptor.
	"modal.update_heading":                "Actualizaciones",
	"modal.update_intro":                  "Antes de instalar, Piumy verifica la firma de la versión: si algo no cuadra, no instala.",
	"modal.update_switch":                 "Actualizar Piumy automáticamente",
	"modal.update_state_current":          "Piumy {current} está al día.",
	"modal.update_state_found":            "Hay una versión nueva ({latest}); se está verificando.",
	"modal.update_state_ready":            "La versión {latest} está verificada y se instala sola en cuanto no haya mensajes pendientes.",
	"modal.update_state_installing":       "Instalando la versión {pending}…",
	"modal.update_state_failed":           "La versión {latest} no se pudo instalar (3 intentos). Piumy la deja de lado hasta que salga una más nueva; el motivo está en el registro.",
	"modal.update_state_nokey":            "Hay una versión nueva ({latest}), pero esta copia de Piumy no puede verificarla: actualízala a mano.",
	"modal.update_state_off":              "Hay una versión nueva ({latest}), pero la actualización automática está apagada: enciéndela aquí o instálala a mano.",
	"modal.update_state_unknown":          "No se pudo leer el estado de las actualizaciones.",
	"modal.update_state_unsupported":      "Esta copia de Piumy no se actualiza sola (es una copia de desarrollo, o no es la cuenta principal).",
	"modal.update_blocked_busy":           "Esperando un momento sin mensajes pendientes.",
	"modal.update_blocked_other_instance": "Hay otra cuenta de Piumy abierta: ciérrala para que se pueda actualizar.",
	"modal.update_installed":              "Última instalación: {version}, {when}.",

	// T193-B: la actualización queda en espera si no se pudo respaldar la sesión de WhatsApp antes de migrar su base.
	"modal.update_blocked_no_session_backup": "No se actualiza todavía: no se pudo guardar una copia de la sesión de WhatsApp.",

	// modal: perfil de WhatsApp
	"titlebar.profile":             "perfil",
	"modal.whatsapp_profile_title": "Editar perfil de WhatsApp",
	"modal.whatsapp_profile_hint":  "Foto, nombre y estado los ve toda tu cuenta al instante — se guardan solo al confirmar.",
	"modal.profile_photo_heading":  "Foto de perfil",
	"modal.name_heading":           "Nombre",
	"modal.name_hint":              "El nombre se cambia desde WhatsApp en tu teléfono (Ajustes → Perfil) — WhatsApp no ofrece esa opción a ningún programa externo.",
	"modal.about_heading":          `Estado ("About")`,
	"label.about_text":             "El texto debajo de tu nombre en WhatsApp",

	// modal: desconectar
	"titlebar.disconnect":    "desconectar",
	"modal.disconnect_title": "¿Desconectar WhatsApp?",
	"modal.disconnect_hint":  `Esto cierra la sesión actual — después usa "Ver QR / Reconectar" para escanear un QR nuevo sin reiniciar.`,

	// modal: login
	"label.username":       "Usuario",
	"label.password":       "Contraseña",
	"link.forgot_password": "¿Olvidaste la contraseña? · Recuperar",

	// modal: recuperar contraseña
	"titlebar.recover":    "recuperar",
	"modal.recover_title": "Recuperar contraseña",
	"modal.recover_hint":  "Elige cómo recibir el código. Vence en 10 minutos, un solo uso.",
	"label.recovery_code": "Código (6 dígitos)",

	// atributos: aria-label / title / alt (T153 2a — applyI18n() extendido)
	"aria.gateway_face":  "Cara viva del gateway",
	"tooltip.governor":   "Governor: anti-ban, ritmo de envío de mensajes",
	"tooltip.backup":     "Backup: cuántos chats, grupos, contactos de agenda y números respaldó el backfill en la base de datos",
	"tooltip.history":    "Historial de conversaciones descargadas (backfill): completas / total — se completa solo, en segundo plano",
	"tooltip.sync":       "Sincronización de historial en vivo tras un re-pareo",
	"tooltip.privacy":    "Oculta SOLO números de teléfono (incluido el tuyo) en todo el tablero. Nombres y avatares quedan visibles. Los MENSAJES no se ocultan — si alguien escribió un número a mano en un mensaje, sigue visible.",
	"alt.clevercat_logo": "Logo de clever.cat",
	"alt.qr":             "QR de WhatsApp",

	// atributos: placeholders
	"placeholder.identity_example":      "Ej.: asistente de un electricista que trabaja solo…",
	"placeholder.group_rules":           "Reglas por defecto para grupos…",
	"placeholder.new_number_rules":      "Reglas por defecto para números nuevos…",
	"placeholder.contact_rules":         "Reglas por defecto para contactos…",
	"placeholder.no_contact_name":       "(sin nombre de agenda)",
	"placeholder.current_password":      "contraseña actual",
	"placeholder.new_password":          "nueva contraseña",
	"placeholder.repeat_password":       "repetir contraseña",
	"placeholder.email_example":         "tu@correo.com",
	"placeholder.reject_reason_example": "Ej.: muy formal, sé más breve…",
	"placeholder.default_status":        "Disponible",

	// T153 etapa 2b-i (ct-2026-09-08-1656): vocabulario compartido para
	// texto que app.js arma en runtime — nombrado por lo que ES (guardando/
	// guardado/error), no por dónde aparece, para que 2b-ii/iii/iv lo
	// reusen en vez de crear duplicados. t(clave, vars) en app.js.
	"action.saving": "Guardando…",
	"action.saved":  "✓ Guardado.",
	// "Error: " — IDÉNTICO en los dos idiomas (coincidencia real, no
	// olvido: "Error" es la misma palabra en ambos, ver el cruce final).
	"error.prefix":                  "Error: ",
	"validation.passwords_mismatch": "Las contraseñas no coinciden.",

	// Estado del hero — la MISMA pasada que resuelve el texto escondido en
	// style.css (ver el comentario ahí).
	"hero.edit_status":         "Editar estado",
	"hero.status_placeholder":  "Agregar estado…",
	"hero.name_disconnected":   "Conecta tu WhatsApp",
	"hero.number_disconnected": "Escanea el QR para vincular tu cuenta al gateway",
	"data.unnamed":             "(sin nombre)",

	// #status (topbar) y el resumen de Backup (T153 2a's nota: los números
	// van adentro de la clave, nunca partidos en fragmentos — el orden de
	// palabras cambia entre idiomas).
	"status.connected":    "conectado",
	"status.disconnected": "desconectado",
	"badge.off":           "apagado",

	// T190 (ct-2026-11-11-101756): POR QUÉ está desconectado, no solo que
	// lo está — con un único texto genérico, una desvinculación se ve
	// igual que un corte de red y puede quedar inadvertida.
	// Son los códigos cerrados de state.WADownReason; el tray usa estas
	// MISMAS claves (tray_appearance.go), una sola fuente de verdad para
	// el nombre del motivo. "status.disconnected" de arriba SIGUE SIENDO
	// el respaldo cuando el motivo viene vacío (un arranque que nunca
	// llegó a WhatsApp): no se borra.
	"status.disconnected_logged_out": "desvinculado",
	"status.disconnected_temp_ban":   "bloqueado temporalmente",
	"status.disconnected_replaced":   "sesión abierta en otro dispositivo",
	"status.disconnected_outdated":   "Piumy desactualizado",
	"status.disconnected_no_network": "sin conexión",

	// T188: el ánimo del gateway (GET /api/status mood, state.ValidMoods) en el idioma del
	// tablero — antes salía el valor crudo del backend, en inglés, en el renglón de estado.
	"status.alive":    "activo",
	"mood.idle":       "en reposo",
	"mood.zero":       "sin pendientes",
	"mood.new_msg":    "mensaje nuevo",
	"mood.few":        "pocos pendientes",
	"mood.swamped":    "muchos pendientes",
	"mood.reading":    "leyendo",
	"mood.switching":  "cambiando de chat",
	"mood.thinking":   "pensando",
	"mood.working":    "trabajando",
	"mood.responding": "respondiendo",
	"mood.done":       "listo",
	"mood.ai_online":  "agente en línea",
	"mood.vip":        "chat prioritario",
	"mood.muted":      "silenciado",
	"mood.sleeping":   "durmiendo",
	"mood.alert":      "alerta",
	"mood.error":      "con error",
	"mood.qr":         "esperando QR",
	// T172b tramo 3: sin el ✅ del principio. El estado de cada renglón de la
	// barra de salud lo dice ahora un punto de color (setBadgeDot en app.js);
	// dejar el visto acá hacía que Backup fuera el único con emoji, al lado de
	// cuatro vecinos con punto. El texto queda contando lo que sabe contar:
	// cuántas cosas se respaldaron.
	"badge.backup_summary": "chats {chats} · grupos {groups} · Contactos {contacts} · Números {numbers}",
	// "sync.summary" — IDÉNTICO en los dos idiomas: "chats"/"msgs" ya son
	// préstamos ingleses en el español de esta interfaz (ver "kill"/
	// "Governor"/"Sync" de la etapa 2a); solo {ago} cambia, y viaja
	// traducido por separado (time.now/time.ago_seconds/time.ago_minutes).
	"sync.summary":     "📥 {chats} chats · {msgs} msgs · {ago}",
	"time.now":         "ahora",
	"time.ago_seconds": "hace {n}s",
	"time.ago_minutes": "hace {n}m",

	// #fresh (T172a, ct-2026-11-10-2000) — hace cuánto llegó el último dato
	// por el SSE, y el aviso de que dejó de llegar. {ago} viaja armado con
	// las tres claves de arriba, igual que en "sync.summary": el tiempo se
	// traduce en un solo lugar. "live.signal_lost" dice TAMBIÉN que se está
	// reconectando porque eso es lo que de verdad pasa (el watchdog ya
	// reintenta solo): sin esa mitad, el aviso parecería pedir un F5, que
	// es justo lo que el dueño no quiere.
	"live.updated_ago": "actualizado {ago}",
	"live.signal_lost": "sin señal, reconectando…",

	"qr.expired":    "Código expirado",
	"qr.expires_in": "Expira en {n}s",

	"profile.choose_image_first":  "Elige una imagen primero.",
	"profile.file_read_error":     "No se pudo leer el archivo.",
	"profile.saving_photo_notice": "Guardando… WhatsApp puede tardar más de un minuto en responder.",

	// T153 etapa 2b-ii: login y recuperación. Precisión de seguridad de
	// Citrino, no negociable — traducir SIN cambiar lo que el mensaje
	// revela. "Usuario o contraseña incorrectos" y "Código inválido o
	// vencido" ya son deliberadamente vagos en español (no dicen CUÁL de
	// los dos falló); el inglés tiene que quedar igual de vago, nunca
	// "User not found" ni "Incorrect password" por separado. Verificado
	// antes de traducir: ningún catch acá usa e.message (ignoran lo que
	// diga el backend, plantan un mensaje genérico propio) y
	// requestRecoveryCode ya usa el MISMO texto en éxito y error — no hay
	// nada que distinga de más para anotar como hallazgo.
	"auth.signing_in":              "Entrando…",
	"auth.invalid_credentials":     "Usuario o contraseña incorrectos.",
	"auth.sending_code":            "Enviando código…",
	"auth.code_sent_generic":       "Si corresponde, te enviamos un código.",
	"auth.invalid_or_expired_code": "Código inválido o vencido.",
	"auth.password_reset_success":  "Contraseña restablecida — inicia sesión con la nueva.",

	// T153 etapa 2b-iii: agentes.
	"agent.role_principal":                "⭐ Principal",
	"agent.role_secondary":                "🤖 Secundario",
	"agent.attends_label":                 "Atiende:",
	"agent.paste_label":                   "Pega la dirección de cAPI",
	"placeholder.antenna_line":            "La línea entera: ip:puerto  chat_id:…  pin:…",
	"agent.paste_hint_card":               "Se guarda sola al pegarla.",
	"agent.paste_unrecognized":            "No reconozco esa línea: tiene que traer ip:puerto, chat_id: y pin:",
	"agent.saved_host":                    "✓ Guardado: {host}",
	"agent.status_ready":                  "Listo para recibir · {host}",
	"agent.status_missing":                "Le falta {list}: pega la dirección de cAPI",
	"agent.missing_address":               "la dirección",
	"agent.missing_terminal":              "el terminal",
	"agent.missing_pin":                   "el PIN",
	"agent.ping_answered":                 "✓ respondió al Ping",
	"agent.ping_no_answer":                "✗ no respondió al Ping",
	"agent.advanced":                      "Avanzado",
	"hint.antenna_paste_create":           "Pegá la línea completa de capi_credentials — nombre, id, endpoint, terminal y pin se completan solos.",
	"hint.endpoint_lan_only":              "Loopback o red privada (LAN) se acepta — incluida la LAN de una Raspberry Pi. Una IP o dominio público se rechaza.",
	"placeholder.terminal_id_example":     "ID del terminal (guid)",
	"placeholder.pin_saved":               "••••••• (guardado)",
	"agent.autodetected":                  "✓ Detectado — campos completados.",
	"agent.sending_ping":                  "Enviando ping…",
	"action.promote_to_principal":         "Promover a principal",
	"agent.promoting":                     "Promoviendo…",
	"agent.assigned_numbers_heading":      "Números asignados",
	"placeholder.assign_search":           "Buscar por nombre o número para asignar…",
	"label.agent_id":                      "ID del agente",
	"placeholder.agent_name":              "Nombre del agente",
	"placeholder.agent_id_example":        "identificador único (ej: vendedor1)",
	"action.new_agent":                    "+ Nuevo agente",
	"action.create":                       "Crear",
	"validation.agent_required_fields":    "ID, endpoint, terminal id y pin son obligatorios.",
	"agent.creating":                      "Creando…",
	"agent.create_replaced_existing":      "✓ Tomó el lugar del agente que ya existía con ese id.",
	"agent.created":                       "✓ Agente creado.",
	"agent.calculating_chats":             `Calculando cuántos chats tiene asignados {name}…`,
	"agent.confirm_delete_question":       `¿Borrar el agente "{name}"?`,
	"agent.delete_impact_chats":           "Se van a desasignar {n} chat{s} — vuelven a la ruta normal, sin agente asignado.",
	"agent.delete_impact_none":            "No tiene chats asignados.",
	"agent.deleting":                      "Borrando…",
	"agent.delete_success":                "✓ Borrado. {n} chat{s} desasignado{s}.",
	"approver.revoke_confirm":             "¿Quitarle a {name} el permiso de aprobar? Deja de ver y de poder aprobar/descartar borradores de otras conversaciones.",
	"action.revoke_permission":            "Quitar permiso",
	"approver.grant_confirm_base":         "Vas a habilitar a {name} para aprobar. Va a poder ver los borradores pendientes de TODAS las conversaciones y aprobarlos o descartarlos.",
	"approver.third_party_warning":        " Es un tercero, no el dueño: le estás dando acceso a mensajes de OTRAS personas.",
	"approver.grant_confirm_restrictions": " NO va a poder cambiar reglas, marcar dueños, ni sacar confirmaciones.",
	"action.enable_approve":               "Habilitar aprobar",
	"level.short_confirm":                 "Confirmación",
	"level.short_ignored":                 "Ignorado",
	"agent.unassigned_option":             "Sin asignar",
	"agent.principal_prefix":              "Principal — {name}",
	"action.remove":                       "quitar",
	"agent.no_assigned_numbers":           "Sin números asignados.",
	"action.loading":                      "Cargando…",
	"agent.none_yet":                      "Sin agentes todavía.",

	// T153 etapa 2b-iv: chats, grupos, contactos, drafts.
	"time.no_messages":         "sin mensajes",
	"time.ago_moments":         "hace instantes",
	"time.ago_minutes_verbose": "hace {n} min",
	"time.ago_hours":           "hace {n}h",
	"time.ago_days":            "hace {n}d",

	"history.tooltip_downloading": "Historial: descargando…",
	"history.tooltip_loaded":      "Historial: completo",

	// "chat.alias_prefix" — IDÉNTICO en los dos idiomas: "alias" ya es
	// préstamo inglés en el español de esta interfaz (mismo criterio que
	// error.prefix/sync.summary).
	"chat.alias_prefix": " · alias: ",
	"chat.type_group":   "grupo",
	"chat.empty_none":   "Todavía no hay conversaciones. Aparecen aquí cuando alguien te escribe.",
	"chat.empty_filter": "Ninguna conversación coincide con la búsqueda.",
	"list.load_failed":  "No se pudo cargar la lista.",
	"action.retry":      "Reintentar",

	"level.label_auto":       "Respuesta automática",
	"level.label_confirm":    "Con tu confirmación",
	"level.label_unattended": "Sin atender",
	"level.label_ignored":    "Ignorados",

	// "rules.general_label" — IDÉNTICO en los dos idiomas (misma palabra).
	"rules.general_label":         "General",
	"rules.no_own_applies_prefix": "Sin regla propia — aplica: ",
	"rules.none_at_any_level":     "Sin reglas en ningún nivel",

	"group.member_count":    "{n} miembro{s}",
	"group.history_pending": "Historial en descarga… todavía no hay mensajes bajados de este grupo.",

	"contacts.section_contacts": "👤 Contactos ({n})",
	"contacts.section_numbers":  "🔢 Números ({n})",
	"contacts.show_more":        "Mostrar {n} más · {shown} de {total}",

	"action.edit":        "Editar",
	"action.approve_msg": "Aprueba MSG",
	"action.rejecting":   "Rechazando…",

	"media.photo_alt": "foto",
	// "media.sticker_alt" — IDÉNTICO en los dos idiomas (préstamo inglés).
	"media.sticker_alt": "sticker",
	"media.pending":     "⏳ descargando…",
	"media.failed":      "⚠ no disponible — WhatsApp ya no tiene este archivo",
	"media.doc_link":    "📎 documento",

	"message.forwarded": "⤷ Reenviado",

	"role.owner":             "Dueño",
	"role.approver":          "Aprobador",
	"agent.assigned_tooltip": "Asignado a este agente",

	"placeholder.search_chats":    "Buscar conversación…  (nombre, número, grupo)",
	"placeholder.search_groups":   "Buscar grupo o miembro…",
	"placeholder.search_contacts": "Buscar por nombre o número…",

	"draft.approve_button": "✓ aprobar",
	"draft.edit_button":    "✎ editar",
	"draft.reject_button":  "↩ rechazar",
	"draft.discard_button": "✕ descartar",
	"draft.pending_count":  "{n} pendiente{s}",
	"draft.round_suffix":   " — ronda {n}",

	"validation.reject_reason_required": "El motivo es obligatorio.",

	// T153 etapa 2b-v (ct-2026-09-08-1656): el barrido por patrón de Citrino
	// sobre TODO app.js, no por área — encontró huecos que el corte por
	// lista de 2b-i..iv no podía ver por diseño.
	"qr.scan_instructions": "Escanea con WhatsApp → Dispositivos vinculados",
	"qr.connecting_note":   "Conectando…",
	"qr.waiting_for_code":  "Esperando código…",
	"qr.generating":        "Generando QR…",

	// T188/T187: motivo de qr_error (GET /api/status): Piumy no pudo abrir la ronda de QR.
	"qr.error_unavailable": "No se pudo generar el QR. Prueba de nuevo en unos segundos.",

	"action.renewing":      "Renovando…",
	"action.disconnecting": "Desconectando…",

	// "agent.label_endpoint"/"agent.label_pin"/"agent.id_prefix"/
	// "agent.ping_button" — IDÉNTICOS en los dos idiomas: "Endpoint"/"PIN"/
	// "ID"/"Ping" ya son préstamos ingleses en el español técnico de esta
	// interfaz (mismo criterio que "Governor"/"Sync" de la etapa 2a).
	"agent.label_endpoint":    "Endpoint",
	"agent.label_terminal_id": "Terminal ID",
	"agent.label_pin":         "PIN",
	"agent.id_prefix":         "ID: ",
	"agent.ping_button":       "Ping 🏓",

	// "badge.governor_killed" — IDÉNTICO en los dos idiomas: "kill" ya es
	// préstamo inglés en este tablero (ver el comentario de sync.summary,
	// arriba). Hallazgo del TestNoUntranslatedProseInKnownSinks recién
	// escrito (prose_sink_test.go) — nadie lo había visto a mano.
	"badge.governor_killed": "⛔ kill",
	"governor.rate_info":    "Ritmo de envío: {n} mensajes/min — protege la cuenta de baneos por envío masivo.",

	// server.* (T153 etapa 3a, ct-2026-09-16-1803): texto que GENERA Go y
	// llega a una persona por fuera del tablero (avisos automáticos por
	// WhatsApp) — universo aparte del que pide app.js/index.html, resuelto
	// por i18n.T() en vez de t(). "server.agent_unreachable" es verbatim
	// del dueño y tres tests en capipush_test.go lo esperan letra por
	// letra — no tocar este valor sin avisar primero.
	"server.agent_unreachable": "agente sin conexión",
	"server.recovery_code":     "🔐 Código de recuperación del dashboard Piumy: {code} — vence en 10 minutos, un solo uso.",

	// T190 (ct-2026-11-11-101756): el aviso que Piumy le manda al agente
	// principal por la antena cuando WhatsApp se cae, y el de cuando
	// vuelve. Los produce internal/wanotice vía autoupdate_wire-style
	// cableado; el motivo {reason} sale de las claves
	// status.disconnected_* de más arriba. Sin datos personales: ni JID,
	// ni número, ni nombre — por eso el motivo es un código cerrado.
	"server.wa_down_notice": "WhatsApp se desconectó: {reason}. Desde {since}. Abre el tablero de Piumy y pulsa Conectar QR.",
	"server.wa_back_notice": "WhatsApp volvió a conectarse. Estuvo caído {duration}.",

	// server.* — etapa 3b (T160, ct-2026-09-16-1828): errores de la API que
	// SÍ llegan a pantalla (via app.js's t("error.prefix") + e.message) y
	// son prosa accionable por el usuario (Clase A del corte de Citrino).
	// Los otros ~29 literales de error medidos quedan sin traducir a
	// propósito (Clase B, error técnico) — ver la lista con motivo en
	// internal/restapi/error_literals_test.go.
	//
	// Estos 6 ya estaban en español en el código — valor ES verbatim, sin
	// tocar una letra (mismo criterio que server.agent_unreachable arriba):
	"server.unsupported_language": "idioma no soportado",
	"server.image_too_large":      "la imagen es demasiado grande",
	"server.invalid_email":        "email inválido",
	"server.values_not_negative":  "los valores no pueden ser negativos",
	"server.ceiling_below_floor":  "el techo no puede ser menor que la espera — bajá la espera o subí el techo",
	"server.unknown_agent_id":     "agent_id desconocido",

	// El resto ya estaba en inglés — este es el idioma NUEVO, traducción
	// fiel del texto en inglés (que queda verbatim en enCatalog):
	"server.unauthorized":                  "no autorizado",
	"server.current_password_incorrect":    "la contraseña actual es incorrecta",
	"server.new_password_required":         "la contraseña nueva no puede estar vacía",
	"server.draft_not_found_resolved":      "borrador no encontrado o ya resuelto",
	"server.draft_not_found_pending":       "borrador no encontrado o ya no está pendiente",
	"server.unknown_agent_id_create_first": "agent_id desconocido — hay que crearlo primero",
	"server.invalid_data_url":              "data_url inválida: {detail}",

	// server.tray_* — etapa 3c (T161, ct-2026-09-16-1854): el menú de la
	// bandeja del sistema (tray_windows.go), el hueco que las seis etapas
	// anteriores dejaron pasar — files_in_scope del contrato padre apuntaba
	// a internal/tray/, que no existe; la bandeja vive en tray_windows.go,
	// en la raíz. ES verbatim, sin tocar una letra. "Piumy Gateway" (título/
	// tooltip del ícono y prefijo del ítem de versión) NO entra acá — es el
	// nombre del producto, nunca se traduce.
	"server.tray_open_dashboard":         "Abrir dashboard",
	"server.tray_open_dashboard_tooltip": "Abrir el dashboard en una ventana",
	"server.tray_open_another":           "Abrir otro Piumy",
	"server.tray_open_another_tooltip":   "Abrir otra cuenta de Piumy, con su propio WhatsApp",
	"server.tray_quit":                   "Salir",
	"server.tray_quit_tooltip":           "Cerrar piumy-gateway",
	"server.tray_version_tooltip":        "Versión de piumy-gateway corriendo",

	// account.label — S2 (ct-2026-09-20-1134): el ítem de menú (y el
	// título/tooltip del ícono) que muestra qué cuenta es esta instancia,
	// cuando hay una (PIUMY_ACCOUNT). Solo la ETIQUETA se traduce — el
	// nombre de cuenta en sí es un dato, nunca texto de interfaz, se pasa
	// por el hueco {account} sin pasar por acá. SIN prefijo "server." a
	// propósito desde S3 (ct-2026-09-20-1202): ya no es solo de la
	// bandeja — el tablero (app.js) pide esta MISMA clave para su propio
	// acento de cuenta, misma etiqueta en los dos lugares.
	"account.label": "Cuenta: {account}",

	// server.recovery_email_* — etapa 3d (T162, ct-2026-09-16-1916): el
	// email de recuperación (deliverRecoveryEmail, recover.go) — el hueco
	// que 3a dejó a propósito ("solo los avisos que salen por WhatsApp") y
	// ninguna etapa posterior retomó hasta que Citrino lo pidió explícito.
	// ES verbatim, voseo incluido ("ignorá", "si no lo pediste vos") —
	// mismo criterio que "bajá la espera o subí el techo" en etapa 3b: el
	// texto que ya vivía a mano no se toca, se traduce al lado.
	"server.recovery_email_subject": "Código de recuperación — Piumy Gateway",
	"server.recovery_email_body":    "Tu código de recuperación es: {code}\n\nVence en 10 minutos y es de un solo uso. Si no lo pediste vos, ignorá este correo.\n",
	// ── Clientes (T172d, ct-2026-11-10-225415) ──────────────────────────
	// Un cliente es UN grupo de WhatsApp, no un cliente con varios chats:
	// el modelo de los tres demos lo descartó el boss. Las cuotas, el
	// estado y el consumo los lleva CleverCoder en un archivo de solo
	// lectura; el tablero solo los muestra. El "nivel" (tier) y el nombre
	// del grupo son DATO — llegan del archivo y no se traducen, igual que
	// los nombres de chat de la etapa 2b-iv.
	// Los cuatro textos de status son los que fijó el contrato, letra por
	// letra: el tablero no inventa su propia versión de "no está al día".
	"nav.summary":                    "Resumen",
	"attention.title":                "Necesita tu atención",
	"attention.drafts_one":           "borrador espera tu aprobación",
	"attention.drafts_many":          "borradores esperan tu aprobación",
	"attention.unattended_one":       "chat sin atender: nadie le contesta",
	"attention.unattended_many":      "chats sin atender: nadie les contesta",
	"attention.all_clear":            "Todo al día.",
	"nav.clients":                    "Clientes",
	"clients.intro":                  "Cada cliente es un grupo de WhatsApp. Las cuotas y el consumo los lleva CleverCoder.",
	"clients.meta_fresh":             "clientes: {count} · foto de las {time}",
	"clients.status_missing":         "sin datos de CleverCoder",
	"clients.status_empty":           "sin clientes todavía",
	"clients.status_stale":           "no está al día, última foto: {when}",
	"clients.status_unknown_version": "actualiza el tablero",
	"clients.tier":                   "nivel: {tier}",
	"clients.state_open":             "abierto",
	"clients.state_warn":             "aviso: pasó el 80 % del tope de la semana",
	"clients.state_paused":           "pausado",
	"clients.paused_until":           "pausado hasta {when}",
	"clients.reopened_note":          "reabierto a mano",
	"clients.week_title":             "Semana",
	"clients.resets_at":              "se reinicia {when}",
	"clients.lines_label":            "líneas",
	"clients.cost_label":             "US$",
	"clients.quota_value":            "{used} de {limit}",
	"clients.no_limit":               "sin tope",
	"clients.previous_week":          "semana pasada: {lines} líneas · US$ {cost}",
	"clients.month_title":            "Mínimos del mes",
	"clients.month_style_global":     "estilo global",
	"clients.month_text":             "texto",
	"clients.month_style_simple":     "estilo simple",
	"clients.chart_title":            "líneas por semana (12 últimas)",
	"clients.chart_limit":            "tope de la semana: {lines} líneas",
	"clients.chart_empty":            "sin historial todavía",
	"clients.chart_bar_title":        "semana del {when}: {lines} líneas · US$ {cost}",
	"clients.agent_label":            "Agente principal",
	"clients.no_group":               "Piumy todavía no conoce este grupo",
}

var enCatalog = map[string]string{
	"alert.factory_password": "⚠️ The dashboard still has the factory password — anyone on your network knows it.",
	"alert.no_terminal":      "⚠️ No default terminal is configured — the owner's messages won't be able to be dispatched until you configure the antenna or PIUMY_DEFAULT_TERMINAL_ID.",

	"hero.connect_qr":   "Connect QR",
	"hero.edit_profile": "Edit profile",
	"hero.disconnect":   "Disconnect",
	"stats.queue":       "queue:",
	"stats.sent":        "sent:",
	"stats.agents":      "agents:",

	"badge.antenna":   "Antenna",
	"badge.encrypted": "Encrypted",
	"badge.history":   "History",
	"badge.privacy":   "Privacy",

	"tab.chats":    "Chats",
	"tab.groups":   "Groups",
	"tab.contacts": "Contacts",
	"tab.agents":   "Agents",
	"tab.rules":    "Rules",
	"tab.drafts":   "Drafts",

	"sort.label":   "Sort by:",
	"sort.recent":  "recent",
	"sort.level":   "level",
	"sort.ignored": "ignored",

	"table.conversation": "Conversation",
	"table.level":        "Level",
	"table.agent":        "Agent",

	"legend.auto_reply":        "automatic reply",
	"legend.with_confirmation": "with your confirmation",
	"legend.unattended":        "unattended",

	"rules.identity_label":     "Identity",
	"rules.identity_hint":      `(sets the tone for the three rules below — "assistant of what")`,
	"rules.legend":             "Each chat follows exactly one of these rules: group chats follow Groups; one-to-one chats with someone in your address book, Phone contacts; everyone else, New messages.",
	"rules.groups_hint":        "(any group chat with no rules of its own)",
	"rules.new_messages_label": "New messages",
	"rules.new_messages_hint":  "(unknown numbers)",
	"rules.contacts_label":     "Phone contacts",
	"rules.contacts_hint":      "(address book, overrides New messages)",
	"rules.boss_hint":          "(your own chat: it has no mode or rules)",
	"rules.unsaved":            "Unsaved changes",

	"footer.thanks":  "Thanks for using Piumy.",
	"footer.made_by": "Made by",

	"phone.view_chat":       "View chat",
	"phone.member_list":     "Member list",
	"phone.readonly_notice": "read-only · replying isn't available yet",

	"action.save":               "Save",
	"action.cancel":             "Cancel",
	"action.close":              "Close",
	"action.confirm":            "Confirm",
	"action.change_password":    "Change password",
	"action.save_identity":      "Save identity",
	"action.save_rules":         "Save rules",
	"action.save_email":         "Save email",
	"action.save_photo":         "Save photo",
	"action.save_status":        "Save status",
	"action.reject":             "Reject",
	"action.delete_agent":       "Delete agent",
	"action.confirm_disconnect": "Yes, disconnect",
	"action.sign_in":            "Sign in",
	"action.send_whatsapp":      "Send via WhatsApp",
	"action.send_email":         "Send via email",
	"action.reset_password":     "Reset",
	"action.refresh_qr":         "🔄 Refresh",

	"modal.contact_name_label": "Contact name",
	"modal.edit_rules_label":   "Rules",
	"modal.edit_memory_label":  "Memory",
	"modal.edit_context_label": "Context",

	"modal.draft_text_label":  "Text",
	"modal.draft_reject_hint": "The reason reaches the agent along with the original message, so it can draft again.",
	"label.reason":            "Reason",

	"titlebar.delete_agent":        "delete agent",
	"titlebar.approver_permission": "approver permission",

	"modal.settings_title":         "Settings ⚙",
	"label.current_password":       "Current password",
	"label.new_password":           "New password",
	"label.confirm_password":       "Confirm password",
	"modal.recovery_email_heading": "Recovery email",
	"modal.dispatch_wait_heading":  "Wait before dispatching",
	"modal.dispatch_wait_hint1":    "How long the gateway waits, silently, before passing messages to the agent — so they arrive grouped instead of one by one. 0 = dispatch as soon as each message arrives.",
	"label.wait_seconds":           "Wait (seconds)",
	"modal.dispatch_wait_hint2":    "If the chat keeps typing without stopping, the wait above resets with every new message — without this cap, the agent might never receive anything. At most, this is how long it waits from the first unanswered message before dispatching anyway.",

	"modal.drift_heading":     "Drift reports",
	"modal.drift_intro":       "When what Piumy believes differs from what is really happening — dispatches held because nobody can receive them, WhatsApp disconnected for a long while, an antenna that does not answer — it can send the developers a short note. It is voluntary: it ships switched off and only goes out if you turn it on here.",
	"modal.drift_sent_label":  "What is sent",
	"modal.drift_sent":        "The kind of problem, how many times it happened, how long it lasted, the Piumy version, the operating system and architecture, and a random identifier of this installation.",
	"modal.drift_never_label": "What is never sent",
	"modal.drift_never":       "No phone number, name, contact, message or chat, no paths on your disk, no keys. This is fixed in the program's code: it does not depend on this screen.",
	"modal.drift_limits":      "At most 3 a day, and one of each kind every 6 to 9 hours. If sending fails, the report is dropped.",
	"modal.drift_destination": "Destination",
	"modal.drift_switch":      "Send drift reports to the developers",
	"label.ceiling_seconds":   "Ceiling (seconds)",
	"label.language":          "Language",
	"modal.language_hint":     `Language for the dashboard and the notifications Piumy sends. "Automatic" uses the operating system's language.`,
	"option.auto_language":    "Automatic (system language)",

	"modal.update_heading":                "Updates",
	"modal.update_intro":                  "Before installing, Piumy checks the version's signature: if anything does not add up, it does not install.",
	"modal.update_switch":                 "Update Piumy automatically",
	"modal.update_state_current":          "Piumy {current} is up to date.",
	"modal.update_state_found":            "There is a new version ({latest}); it is being verified.",
	"modal.update_state_ready":            "Version {latest} is verified and will install itself as soon as no messages are pending.",
	"modal.update_state_installing":       "Installing version {pending}…",
	"modal.update_state_failed":           "Version {latest} could not be installed (3 tries). Piumy sets it aside until a newer one comes out; the reason is in the log.",
	"modal.update_state_nokey":            "There is a new version ({latest}), but this copy of Piumy cannot verify it: update it by hand.",
	"modal.update_state_off":              "There is a new version ({latest}), but automatic updating is switched off: turn it on here or install it by hand.",
	"modal.update_state_unknown":          "The update status could not be read.",
	"modal.update_state_unsupported":      "This copy of Piumy does not update itself (it is a development copy, or not the main account).",
	"modal.update_blocked_busy":           "Waiting for a moment with no pending messages.",
	"modal.update_blocked_other_instance": "Another Piumy account is open: close it so this one can update.",
	"modal.update_installed":              "Last install: {version}, {when}.",

	// T193-B: the update stays on hold when the WhatsApp session could not be backed up before its database migrates.
	"modal.update_blocked_no_session_backup": "Not updating yet: could not save a copy of the WhatsApp session.",

	"titlebar.profile":             "profile",
	"modal.whatsapp_profile_title": "Edit WhatsApp profile",
	"modal.whatsapp_profile_hint":  "Photo, name and status are seen by your whole account instantly — they're only saved on confirm.",
	"modal.profile_photo_heading":  "Profile photo",
	"modal.name_heading":           "Name",
	"modal.name_hint":              "The name is changed from WhatsApp on your phone (Settings → Profile) — WhatsApp doesn't offer that option to any external program.",
	"modal.about_heading":          `Status ("About")`,
	"label.about_text":             "The text below your name in WhatsApp",

	"titlebar.disconnect":    "disconnect",
	"modal.disconnect_title": "Disconnect WhatsApp?",
	"modal.disconnect_hint":  `This closes the current session — afterward use "View QR / Reconnect" to scan a new QR without restarting.`,

	"label.username":       "Username",
	"label.password":       "Password",
	"link.forgot_password": "Forgot your password? · Recover",

	"titlebar.recover":    "recover",
	"modal.recover_title": "Recover password",
	"modal.recover_hint":  "Choose how to receive the code. Expires in 10 minutes, single use.",
	"label.recovery_code": "Code (6 digits)",

	"aria.gateway_face":  "Live face of the gateway",
	"tooltip.governor":   "Governor: anti-ban, message sending rate",
	"tooltip.backup":     "Backup: how many chats, groups, address-book contacts and numbers the backfill saved to the database",
	"tooltip.history":    "Downloaded conversation history (backfill): complete / total — fills in on its own, in the background",
	"tooltip.sync":       "Live history sync after a re-pairing",
	"tooltip.privacy":    "Hides ONLY phone numbers (including your own) across the whole dashboard. Names and avatars stay visible. MESSAGES are not hidden — if someone typed a number by hand in a message, it stays visible.",
	"alt.clevercat_logo": "clever.cat logo",
	"alt.qr":             "WhatsApp QR",

	"placeholder.identity_example":      "E.g.: assistant for a self-employed electrician…",
	"placeholder.group_rules":           "Default rules for groups…",
	"placeholder.new_number_rules":      "Default rules for new numbers…",
	"placeholder.contact_rules":         "Default rules for contacts…",
	"placeholder.no_contact_name":       "(no address-book name)",
	"placeholder.current_password":      "current password",
	"placeholder.new_password":          "new password",
	"placeholder.repeat_password":       "repeat password",
	"placeholder.email_example":         "you@email.com",
	"placeholder.reject_reason_example": "E.g.: too formal, be more concise…",
	"placeholder.default_status":        "Available",

	"action.saving":                 "Saving…",
	"action.saved":                  "✓ Saved.",
	"error.prefix":                  "Error: ",
	"validation.passwords_mismatch": "Passwords don't match.",

	"hero.edit_status":         "Edit status",
	"hero.status_placeholder":  "Add status…",
	"hero.name_disconnected":   "Connect your WhatsApp",
	"hero.number_disconnected": "Scan the QR to link your account to the gateway",
	"data.unnamed":             "(no name)",

	"status.connected":    "connected",
	"status.disconnected": "disconnected",

	// T190 (ct-2026-11-11-101756): WHY it is disconnected, not just that it
	// is — with a single generic word, an unlink looks exactly like a
	// network drop and can go unnoticed. These are
	// state.WADownReason's closed codes; the tray uses these SAME keys
	// (tray_appearance.go), one source of truth for what an outage is
	// called. "status.disconnected" above REMAINS the fallback for an empty
	// reason (a boot that never reached WhatsApp): it is not removed.
	"status.disconnected_logged_out": "unlinked",
	"status.disconnected_temp_ban":   "temporarily blocked",
	"status.disconnected_replaced":   "session opened on another device",
	"status.disconnected_outdated":   "Piumy out of date",
	"status.disconnected_no_network": "no connection",

	// T188: the gateway mood (GET /api/status mood, state.ValidMoods) in the dashboard's
	// language — it used to print the backend's raw value in the status line.
	"status.alive":         "alive",
	"mood.idle":            "idle",
	"mood.zero":            "nothing pending",
	"mood.new_msg":         "new message",
	"mood.few":             "a few pending",
	"mood.swamped":         "swamped",
	"mood.reading":         "reading",
	"mood.switching":       "switching chats",
	"mood.thinking":        "thinking",
	"mood.working":         "working",
	"mood.responding":      "responding",
	"mood.done":            "done",
	"mood.ai_online":       "agent online",
	"mood.vip":             "priority chat",
	"mood.muted":           "muted",
	"mood.sleeping":        "sleeping",
	"mood.alert":           "alert",
	"mood.error":           "error",
	"mood.qr":              "waiting for QR",
	"badge.off":            "off",
	"badge.backup_summary": "chats {chats} · groups {groups} · Contacts {contacts} · Numbers {numbers}",
	"sync.summary":         "📥 {chats} chats · {msgs} msgs · {ago}",
	"time.now":             "now",
	"time.ago_seconds":     "{n}s ago",
	"time.ago_minutes":     "{n}m ago",
	"live.updated_ago":     "updated {ago}",
	"live.signal_lost":     "no signal, reconnecting…",

	"qr.expired":    "Code expired",
	"qr.expires_in": "Expires in {n}s",

	"profile.choose_image_first":  "Choose an image first.",
	"profile.file_read_error":     "Could not read the file.",
	"profile.saving_photo_notice": "Saving… WhatsApp can take more than a minute to respond.",

	"auth.signing_in":              "Signing in…",
	"auth.invalid_credentials":     "Wrong username or password.",
	"auth.sending_code":            "Sending code…",
	"auth.code_sent_generic":       "If it applies, we sent you a code.",
	"auth.invalid_or_expired_code": "Invalid or expired code.",
	"auth.password_reset_success":  "Password reset — sign in with the new one.",

	"agent.role_principal":                "⭐ Primary",
	"agent.role_secondary":                "🤖 Secondary",
	"agent.attends_label":                 "Handles:",
	"agent.paste_label":                   "Paste the cAPI address",
	"placeholder.antenna_line":            "The whole line: ip:port  chat_id:…  pin:…",
	"agent.paste_hint_card":               "It saves itself when you paste it.",
	"agent.paste_unrecognized":            "That line isn't recognized: it needs ip:port, chat_id: and pin:",
	"agent.saved_host":                    "✓ Saved: {host}",
	"agent.status_ready":                  "Ready to receive · {host}",
	"agent.status_missing":                "It is missing {list}: paste the cAPI address",
	"agent.missing_address":               "the address",
	"agent.missing_terminal":              "the terminal",
	"agent.missing_pin":                   "the PIN",
	"agent.ping_answered":                 "✓ answered the Ping",
	"agent.ping_no_answer":                "✗ did not answer the Ping",
	"agent.advanced":                      "Advanced",
	"hint.antenna_paste_create":           "Paste the full capi_credentials line — name, id, endpoint, terminal and pin fill in on their own.",
	"hint.endpoint_lan_only":              "Loopback or a private network (LAN) is accepted — including a Raspberry Pi's LAN. A public IP or domain is rejected.",
	"placeholder.terminal_id_example":     "Terminal ID (guid)",
	"placeholder.pin_saved":               "••••••• (saved)",
	"agent.autodetected":                  "✓ Detected — fields filled in.",
	"agent.sending_ping":                  "Sending ping…",
	"action.promote_to_principal":         "Promote to primary",
	"agent.promoting":                     "Promoting…",
	"agent.assigned_numbers_heading":      "Assigned numbers",
	"placeholder.assign_search":           "Search by name or number to assign…",
	"label.agent_id":                      "Agent ID",
	"placeholder.agent_name":              "Agent name",
	"placeholder.agent_id_example":        "unique identifier (e.g.: agent1)",
	"action.new_agent":                    "+ New agent",
	"action.create":                       "Create",
	"validation.agent_required_fields":    "ID, endpoint, terminal id and pin are required.",
	"agent.creating":                      "Creating…",
	"agent.create_replaced_existing":      "✓ Took the place of the agent that already existed with that id.",
	"agent.created":                       "✓ Agent created.",
	"agent.calculating_chats":             `Calculating how many chats {name} has assigned…`,
	"agent.confirm_delete_question":       `Delete agent "{name}"?`,
	"agent.delete_impact_chats":           "{n} chat{s} will be unassigned — they go back to the normal route, with no agent assigned.",
	"agent.delete_impact_none":            "It has no assigned chats.",
	"agent.deleting":                      "Deleting…",
	"agent.delete_success":                "✓ Deleted. {n} chat{s} unassigned.",
	"approver.revoke_confirm":             "Revoke {name}'s permission to approve? They lose the ability to see and approve/discard drafts from other conversations.",
	"action.revoke_permission":            "Revoke permission",
	"approver.grant_confirm_base":         "You're about to enable {name} to approve. They'll be able to see pending drafts from ALL conversations and approve or discard them.",
	"approver.third_party_warning":        " This is a third party, not the owner: you're giving them access to OTHER people's messages.",
	"approver.grant_confirm_restrictions": " They will NOT be able to change rules, mark owners, or remove confirmations.",
	"action.enable_approve":               "Enable approving",
	"level.short_confirm":                 "Confirm",
	"level.short_ignored":                 "Ignored",
	"agent.unassigned_option":             "Unassigned",
	"agent.principal_prefix":              "Primary — {name}",
	"action.remove":                       "remove",
	"agent.no_assigned_numbers":           "No assigned numbers.",
	"action.loading":                      "Loading…",
	"agent.none_yet":                      "No agents yet.",

	"time.no_messages":         "no messages",
	"time.ago_moments":         "moments ago",
	"time.ago_minutes_verbose": "{n} min ago",
	"time.ago_hours":           "{n}h ago",
	"time.ago_days":            "{n}d ago",

	"history.tooltip_downloading": "History: downloading…",
	"history.tooltip_loaded":      "History: complete",

	"chat.alias_prefix": " · alias: ",
	"chat.type_group":   "group",
	"chat.empty_none":   "No conversations yet. They show up here when someone messages you.",
	"chat.empty_filter": "No conversation matches the search.",
	"list.load_failed":  "Couldn't load the list.",
	"action.retry":      "Retry",

	"level.label_auto":       "Automatic reply",
	"level.label_confirm":    "With your confirmation",
	"level.label_unattended": "Unattended",
	"level.label_ignored":    "Ignored",

	"rules.general_label":         "General",
	"rules.no_own_applies_prefix": "No rule of its own — applies: ",
	"rules.none_at_any_level":     "No rules at any level",

	"group.member_count":    "{n} member{s}",
	"group.history_pending": "History downloading… no messages from this group yet.",

	"contacts.section_contacts": "👤 Contacts ({n})",
	"contacts.section_numbers":  "🔢 Numbers ({n})",
	"contacts.show_more":        "Show {n} more · {shown} of {total}",

	"action.edit":        "Edit",
	"action.approve_msg": "Approve MSG",
	"action.rejecting":   "Rejecting…",

	"media.photo_alt":   "photo",
	"media.sticker_alt": "sticker",
	"media.pending":     "⏳ downloading…",
	"media.failed":      "⚠ not available — WhatsApp no longer has this file",
	"media.doc_link":    "📎 document",

	"message.forwarded": "⤷ Forwarded",

	"role.owner":             "Owner",
	"role.approver":          "Approver",
	"agent.assigned_tooltip": "Assigned to this agent",

	"placeholder.search_chats":    "Search conversation…  (name, number, group)",
	"placeholder.search_groups":   "Search group or member…",
	"placeholder.search_contacts": "Search by name or number…",

	"draft.approve_button": "✓ approve",
	"draft.edit_button":    "✎ edit",
	"draft.reject_button":  "↩ reject",
	"draft.discard_button": "✕ discard",
	// "{n} pending{s}" leería incompleto en inglés sin sustantivo (a
	// diferencia del español, donde "pendiente" solo alcanza) — se agrega
	// "draft{s}" para que el mismo número quede claro sin depender del
	// contexto visual (#draftcount es un párrafo suelto, sin la palabra
	// "drafts" al lado).
	"draft.pending_count": "{n} pending draft{s}",
	"draft.round_suffix":  " — round {n}",

	"validation.reject_reason_required": "A reason is required.",

	"qr.scan_instructions": "Scan with WhatsApp → Linked devices",
	"qr.connecting_note":   "Connecting…",
	"qr.waiting_for_code":  "Waiting for code…",
	"qr.generating":        "Generating QR…",

	// T188/T187: qr_error reason (GET /api/status): Piumy could not open the QR round.
	"qr.error_unavailable": "Could not generate the QR. Try again in a few seconds.",

	"action.renewing":      "Renewing…",
	"action.disconnecting": "Disconnecting…",

	"agent.label_endpoint":    "Endpoint",
	"agent.label_terminal_id": "Terminal ID",
	"agent.label_pin":         "PIN",
	"agent.id_prefix":         "ID: ",
	"agent.ping_button":       "Ping 🏓",

	"badge.governor_killed": "⛔ kill",
	"governor.rate_info":    "Send rate: {n} messages/min — protects the account from mass-send bans.",

	// server.* — see the matching comment in esCatalog. Only this English
	// value is free to read naturally; the Spanish one is pinned verbatim.
	"server.agent_unreachable": "Agent unreachable",
	"server.recovery_code":     "🔐 Piumy dashboard recovery code: {code} — expires in 10 minutes, single use.",

	// T190 (ct-2026-11-11-101756): the notice Piumy sends the principal
	// agent over the antenna when WhatsApp drops, and the one when it comes
	// back. Produced by internal/wanotice through its wiring; {reason}
	// comes from the status.disconnected_* keys above. No personal data: no
	// JID, no number, no name — which is why the reason is a closed code.
	"server.wa_down_notice": "WhatsApp disconnected: {reason}. Since {since}. Open the Piumy dashboard and press Connect QR.",
	"server.wa_back_notice": "WhatsApp is connected again. It was down for {duration}.",

	// server.* — etapa 3b, see the matching comment in esCatalog.
	//
	// These 6 were already English in the Go source — verbatim, unchanged
	// (the Spanish value above is the new translation):
	"server.unauthorized":                  "unauthorized",
	"server.current_password_incorrect":    "current password incorrect",
	"server.new_password_required":         "new_password must not be empty",
	"server.draft_not_found_resolved":      "draft not found or already resolved",
	"server.draft_not_found_pending":       "draft not found or not pending",
	"server.unknown_agent_id":              "unknown agent_id",
	"server.unknown_agent_id_create_first": "unknown agent_id — create it first",
	"server.invalid_data_url":              "invalid data_url: {detail}",

	// These 6 were already Spanish — this is the NEW translation, natural
	// English (the Spanish value in esCatalog stays verbatim, unchanged):
	"server.unsupported_language": "unsupported language",
	"server.image_too_large":      "the image is too large",
	"server.invalid_email":        "invalid email",
	"server.values_not_negative":  "values cannot be negative",
	"server.ceiling_below_floor":  "the ceiling can't be lower than the wait — lower the wait or raise the ceiling",

	// server.tray_* — see the matching comment in esCatalog.
	"server.tray_open_dashboard":         "Open dashboard",
	"server.tray_open_dashboard_tooltip": "Open the dashboard in a window",
	"server.tray_open_another":           "Open another Piumy",
	"server.tray_open_another_tooltip":   "Open another Piumy account, with its own WhatsApp",
	"server.tray_quit":                   "Quit",
	"server.tray_quit_tooltip":           "Close piumy-gateway",
	"server.tray_version_tooltip":        "piumy-gateway version currently running",

	// account.label — see the matching comment in esCatalog.
	"account.label": "Account: {account}",

	// server.recovery_email_* — see the matching comment in esCatalog.
	"server.recovery_email_subject": "Piumy Gateway recovery code",
	"server.recovery_email_body":    "Your recovery code is: {code}\n\nExpires in 10 minutes and is single-use. If you didn't request this, ignore this email.\n",
	// ── Clients (T172d) — see the Spanish block for the model: one client
	// is ONE WhatsApp group. Group name and tier are DATA, never translated.
	"nav.summary":                    "Summary",
	"attention.title":                "Needs your attention",
	"attention.drafts_one":           "draft is waiting for your approval",
	"attention.drafts_many":          "drafts are waiting for your approval",
	"attention.unattended_one":       "unattended chat: nobody answers it",
	"attention.unattended_many":      "unattended chats: nobody answers them",
	"attention.all_clear":            "All caught up.",
	"nav.clients":                    "Clients",
	"clients.intro":                  "Each client is a WhatsApp group. Quotas and usage are tracked by CleverCoder.",
	"clients.meta_fresh":             "clients: {count} · snapshot at {time}",
	"clients.status_missing":         "no data from CleverCoder",
	"clients.status_empty":           "no clients yet",
	"clients.status_stale":           "out of date, last snapshot: {when}",
	"clients.status_unknown_version": "update the dashboard",
	"clients.tier":                   "tier: {tier}",
	"clients.state_open":             "open",
	"clients.state_warn":             "warning: past 80% of the weekly limit",
	"clients.state_paused":           "paused",
	"clients.paused_until":           "paused until {when}",
	"clients.reopened_note":          "reopened by hand",
	"clients.week_title":             "Week",
	"clients.resets_at":              "resets {when}",
	"clients.lines_label":            "lines",
	"clients.cost_label":             "US$",
	"clients.quota_value":            "{used} of {limit}",
	"clients.no_limit":               "no limit",
	"clients.previous_week":          "last week: {lines} lines · US$ {cost}",
	"clients.month_title":            "Monthly minimums",
	"clients.month_style_global":     "global style",
	"clients.month_text":             "text",
	"clients.month_style_simple":     "simple style",
	"clients.chart_title":            "lines per week (last 12)",
	"clients.chart_limit":            "weekly limit: {lines} lines",
	"clients.chart_empty":            "no history yet",
	"clients.chart_bar_title":        "week of {when}: {lines} lines · US$ {cost}",
	"clients.agent_label":            "Primary agent",
	"clients.no_group":               "Piumy doesn't know this group yet",
}
