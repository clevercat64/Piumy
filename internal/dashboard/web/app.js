// Vanilla JS, no build step, no framework — matches .clevercoder/dashboard.cui v5.
(function () {
  "use strict";

  // Same convention restapi.auth already accepts (?key= or X-API-Key) — if
  // the page was opened with ?key=..., carry it on every /api/* call too.
  var apiKey = new URLSearchParams(location.search).get("key");
  function withKey(path) {
    if (!apiKey) return path;
    return path + (path.indexOf("?") === -1 ? "?" : "&") + "key=" + encodeURIComponent(apiKey);
  }
  function api(path) {
    return fetch(withKey(path)).then(function (r) {
      if (!r.ok) throw new Error(path + " -> " + r.status);
      return r.json();
    });
  }
  // post: el mensaje del servidor gana sobre el codigo HTTP (2026-09-01).
  // Antes tiraba "path -> 500" y descartaba el cuerpo, que es justo donde el
  // gateway explica la causa ("WhatsApp rechazo la imagen...", "info query
  // timed out"). El dueño apretaba Guardar, veia un numero sin sentido o
  // nada, y lo leia como un boton roto. Mismo criterio que T87/T104 en el
  // lado del gateway: el error dice la causa, no el sintoma.
  function post(path, body) {
    return fetch(withKey(path), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then(function (r) {
      if (r.ok) return r.json();
      return r.json().catch(function () { return null; }).then(function (data) {
        throw new Error((data && data.error) || (path + " -> " + r.status));
      });
    });
  }

  // ── login overlay (ct-2026-07-19-1616, S1d) ─────────────────────────────
  // S5 (ct-2026-09-23-2038): the account's name and color are written into
  // <body> by the server (restapi indexWithAccount) — /api/status, where
  // applyAccountIdentity reads them, needs a session, so the login screen
  // couldn't say which account it was for. No account: attributes absent, the
  // chip stays hidden, nothing changes.
  function showLogin() {
    var account = document.body.dataset.account;
    var chip = document.getElementById("loginaccount");
    if (account) {
      chip.textContent = t("account.label", { account: account });
      chip.style.color = document.body.dataset.accountColor || "";
      chip.classList.remove("hidden");
    }
    document.getElementById("loginmodal").classList.remove("hidden");
  }
  function hideLogin() {
    document.getElementById("loginmodal").classList.add("hidden");
  }

  var state = { chats: [], groups: [], groupMembers: [], agents: [], originAgentDefaults: {}, selectedJID: null, appliedSort: "recientes", filterText: "", lastStatus: null, lastUpdate: null, updateUnreachable: false, privacyMode: false, i18n: { lang: "es", language: "", texts: {} } };

  // ── i18n (T153 etapa 1, ct-2026-09-08-1656): un solo catálogo de textos,
  // servido por el backend — el tablero no guarda su propia lista (si la
  // guardara, se desincronizan y el próximo idioma cuesta el doble). Etapa 1
  // es solo el mecanismo: el catálogo llega vacío hasta que la etapa 2 saque
  // los textos de este archivo y de index.html hacia internal/i18n.
  // applyI18n() ya funciona sobre [data-i18n] — hoy no traduce nada porque
  // ningún elemento tiene ese atributo todavía, y ESO es la prueba de que el
  // mecanismo no rompió nada (T153: "el tablero se ve exactamente igual").
  function loadI18n() {
    return api("/api/i18n").then(function (d) {
      state.i18n = { lang: d.lang || "es", language: d.language || "", texts: d.texts || {} };
    });
  }
  // T153 2a: además de textContent ([data-i18n]), un texto estático de
  // index.html puede vivir en un atributo (placeholder/title/aria-label/
  // alt) — el mismo patrón if-no-clobber, una clave que falta deja el
  // atributo como está.
  var I18N_ATTRS = {
    "data-i18n": null,
    "data-i18n-placeholder": "placeholder",
    "data-i18n-title": "title",
    "data-i18n-aria-label": "aria-label",
    "data-i18n-alt": "alt",
  };
  function applyI18n() {
    Object.keys(I18N_ATTRS).forEach(function (dataAttr) {
      var htmlAttr = I18N_ATTRS[dataAttr];
      document.querySelectorAll("[" + dataAttr + "]").forEach(function (el) {
        var text = state.i18n.texts[el.getAttribute(dataAttr)];
        if (!text) return;
        if (htmlAttr) el.setAttribute(htmlAttr, text);
        else el.textContent = text;
      });
    });
  }

  // T153 2b-i: t(clave, vars) es para texto que se ARMA en runtime — existe
  // antes que el elemento (a diferencia de applyI18n(), que traduce un
  // elemento ya declarado en el marcado). vars reemplaza cada "{nombre}"
  // del template — sin librería, alcanza para lo poco que hace falta. Los
  // textos con un número adentro son UNA sola clave con el hueco marcado,
  // nunca fragmentos separados (el orden de las palabras cambia entre
  // idiomas — ver badge.backup_summary). Si la clave todavía no existe en
  // el catálogo (loadI18n() sin resolver, o un typo), devuelve la clave
  // entre corchetes: roto y visible se encuentra, roto y callado no.
  function t(key, vars) {
    var template = state.i18n.texts[key];
    if (!template) return "[" + key + "]";
    if (!vars) return template;
    return template.replace(/\{(\w+)\}/g, function (m, name) {
      return name in vars ? vars[name] : m;
    });
  }

  // ── modo privacidad (T85, ct-2026-08-28-0059, boss: "un boton de un
  // ojito que oculte todos los numeros de telefono... asi puedo mostrar mi
  // trabajo en stream") — leído de localStorage ACÁ, en la parte del script
  // que corre antes de que exista un solo fetch en vuelo (el bootstrap real
  // está al final del archivo, `loadStatus().then(...)`): para cuando esa
  // promesa resuelve y cualquier función de render escribe al DOM,
  // state.privacyMode YA está fijado — no hay ventana entre "el dato llega"
  // y "se decide si se tapa". Sobrevive a F5 por lo mismo: se lee de nuevo
  // acá en cada carga de página, antes de pedir nada.
  var PRIVACY_KEY = "piumy_privacy_mode";
  state.privacyMode = localStorage.getItem(PRIVACY_KEY) === "1";

  // numberTag: un código de 3 caracteres ASIGNADO — no un hash — la
  // primera vez que se tapa CADA número real en esta sesión, incremental
  // (0, 1, 2... en base36). Se probó primero con un hash (FNV-1a, mismo
  // esquema que privacyTag de T85): sobre 500 números sintéticos con el
  // MISMO prefijo de 3 dígitos, dio 1 colisión — dos números DISTINTOS con
  // el mismo tapado. El contrato pide una garantía dura ("dos números
  // distintos se ven distinto"), no "casi siempre" — un hash nunca la da
  // (paradoja del cumpleaños), un contador SÍ, por construcción: cada
  // número real nuevo consume el próximo índice, nunca se repite. Cuesta la
  // estabilidad entre recargas (T85's privacyTag la tenía; acá no se pidió
  // y sostenerla exigiría volver al hash) — cambio aceptado a propósito,
  // documentado, no en silencio.
  var numberTags = {};
  var nextNumberTagIndex = 0;
  function numberTag(digits) {
    if (!(digits in numberTags)) {
      // Ancho mínimo 3, nunca recortado — un padStart que TRUNCARA en vez
      // de crecer pasado los 36^3 números distintos (46656, ningún tablero
      // real llega ahí) volvería a colisionar, justo lo que este esquema
      // existe para evitar.
      var t = (nextNumberTagIndex++).toString(36).toUpperCase();
      while (t.length < 3) t = "0" + t;
      numberTags[digits] = t;
    }
    return numberTags[digits];
  }
  // maskNumber: el ÚNICO lugar que decide si un número de CHAT se tapa
  // (T93 acotó el alcance a "solo números"; T94 lo hace PARCIAL). Boss
  // verbatim (ct-2026-08-28-1715), viendo el número tapado ENTERO: "se debe
  // tapar una parte del numero" — con todo tapado, ninguna fila se
  // distinguía de otra en stream. Requisitos duros del contrato: (1) dos
  // números DISTINTOS se ven distinto, (2) lo que queda visible no permite
  // reconstruir el número ni identificar a la persona — ojo con los
  // últimos dígitos, que es justo lo que la gente usa para reconocer a
  // alguien.
  //   digits.slice(0,3) + numberTag(digits): los primeros 3 dígitos
  //   (código de país + arranque del prefijo — p.ej. "569" es TODOS los
  //   celulares de Chile, no identifica a nadie puntual) satisfacen "parte
  //   visible" tal cual lo pidió el dueño; numberTag (arriba) es lo que
  //   garantiza (1), no los dígitos. Los dígitos FINALES —el riesgo real
  //   señalado en el contrato— nunca se muestran: solo se toma desde el
  //   principio del número, nunca desde el final.
  // Pass-through puro cuando privacyMode está apagado, así que llamarlo
  // siempre, incondicionalmente, en cada sitio que PINTA un número es
  // seguro. jidNumber(jid) crudo sigue sin tocar: alimenta buscador y
  // orden alfabético, que necesitan el dato real.
  function maskNumber(real) {
    if (!state.privacyMode) return real;
    var digits = String(real || "");
    return digits.slice(0, 3) + "•••" + numberTag(digits);
  }

  // looksLikeNumber / maskedName (T110, ct-2026-09-01-1429): WhatsApp entrega
  // muy seguido el propio número COMO nombre (c.name) cuando el contacto no
  // está guardado en la agenda — sin esto, ese "nombre" se pinta crudo en modo
  // privacidad, porque técnicamente SÍ hay un name y la rama de fallback
  // (maskNumber(jidNumber(...))) nunca se alcanza. Reconoce la FORMA de un
  // número (dígitos, +, espacios, guiones, paréntesis) — nunca un nombre real
  // con letras: T93/T94 ya establecieron que el modo privacidad tapa números,
  // no identidades escritas, y esto no cambia esa regla.
  function looksLikeNumber(s) {
    return /^[+\d(][\d\s\-()]*$/.test(s || "");
  }
  // maskedName no toca maskNumber/numberTag (fuera de alcance de T110) — solo
  // decide, ANTES de llegar a esa función, si lo que iba a pintarse como
  // nombre es en realidad un número, y si lo es, lo reduce a dígitos puros
  // (el formato que maskNumber ya espera en todo el resto del código, vía
  // jidNumber) antes de pasárselo. "" o undefined vuelve tal cual — el
  // caller decide su propio fallback cuando no hay nombre.
  function maskedName(name) {
    if (!name) return name;
    return looksLikeNumber(name) ? maskNumber(name.replace(/\D/g, "")) : name;
  }

  // ── carita viva: mood real (GET /api/status) → kaomoji ──────────────────
  // Catálogo portado de Piumy adapters/display/render.py KAOMOJI_CATALOG
  // (primera variante de cada mood — alcanza para un vistazo de estado; el
  // ciclo completo de variantes es cosa del e-paper, no de este dashboard).
  // "idle" no tiene entrada a propósito: usa el gaze engine (mirada+blink)
  // en vez de un kaomoji fijo, igual que en Piumy.
  // "qr" (ct-2026-07-19-1735, S1f): NOT in Piumy's original KAOMOJI_CATALOG
  // (there "qr" is a special e-paper render path with no face at all, see
  // adapters/display/render.py) — picked fresh here, distinct from every
  // other glyph already in this map.
  var MOOD_FACES = {
    zero: "(●‿●)", new_msg: "(◕‿◕)!", few: "(◕‿◕)", swamped: "(◑_◑)~",
    reading: "(◐_◐)", switching: "(◓_◓)", thinking: "(-.-)", working: "(--‿--)",
    responding: "(●‿●)>", done: "(^‿^)v", ai_online: "(⌐■_■)", vip: "(♥‿♥)",
    muted: "(-_-)", sleeping: "(⇀‿↼)", paused: "(◒_◒)", alert: "(☓o☓)!", error: "(☓_☓)",
    qr: "(⊙_⊙)",
  };
  var IDLE_FACE_HTML = '<span class="paren">(</span><span class="eye">◕</span><span class="mouth">‿</span><span class="eye">◕</span><span class="paren">)</span>';

  // APPROVER_ICON_SVG (Aprobador P1, ct-2026-07-31-0610 — ícono definitivo,
  // boss verbatim: "fluent-ui:gavel:gavel:16-filled ese está lindo") —
  // Fluent UI System Icons (Microsoft, MIT), ic_fluent_gavel_16_filled,
  // pegado inline: nada de CDN ni <link> a una librería de íconos — este
  // gateway corre en una Raspberry o una red sin salida a internet, y un
  // ícono de CDN es un cuadrado vacío el día que no hay red. fill
  // reemplazado por currentColor (el original traía #212121 fijo) para que
  // herede el color del botón, igual apagado que prendido.
  var APPROVER_ICON_SVG = '<svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M6.76551 1.49997C7.44147 0.824012 8.5688 0.94436 9.08683 1.74778L9.26649 2.02641L5.02641 6.26648L4.74782 6.08686C3.94436 5.56884 3.824 4.44148 4.49997 3.76551L6.76551 1.49997ZM5.88625 6.82085L9.82088 2.88623L11.0533 4.79753C11.0918 4.85734 11.1427 4.90823 11.2025 4.9468L13.1138 6.17911L9.17914 10.1138L7.94688 8.20247C7.90831 8.14264 7.85741 8.09174 7.79759 8.05317L5.88625 6.82085ZM9.7335 10.9736L9.91298 11.252C10.431 12.0555 11.5584 12.1758 12.2343 11.4999L14.4999 9.23434C15.1758 8.55838 15.0555 7.43103 14.252 6.91301L13.9736 6.73349L9.7335 10.9736ZM5.87755 8.0051L1.43934 12.4433C0.853553 13.0291 0.853553 13.9789 1.43934 14.5646C2.02513 15.1504 2.97487 15.1504 3.56066 14.5646L7.99799 10.1273L7.16487 8.83509L5.87755 8.0051ZM10 13C9.72386 13 9.5 13.2239 9.5 13.5C9.5 13.7761 9.72386 14 10 14H8.5C8.22386 14 8 14.2239 8 14.5C8 14.7761 8.22386 15 8.5 15H14.5C14.7761 15 15 14.7761 15 14.5C15 14.2239 14.7761 14 14.5 14H13C13.2761 14 13.5 13.7761 13.5 13.5C13.5 13.2239 13.2761 13 13 13H10Z"/></svg>';
  var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // gaze engine — idéntico al del mockup aprobado (dashboard-mockup.html).
  var gazeGen = 0;
  function animateFace(face, myGen) {
    var QUARTER = "◕", HALF = ["◐", "◑", "◒", "◓"], POINT = "⚆";
    function pick(a) { return a[Math.floor(Math.random() * a.length)]; }
    var eyes = face.querySelectorAll(".eye");
    if (eyes.length < 2) return;
    function setEyes(glyph, deg) {
      eyes.forEach(function (e) {
        e.classList.remove("blink");
        e.textContent = glyph; e.style.transform = "rotate(" + deg + "deg)";
      });
    }
    function blink(cb) {
      eyes.forEach(function (e) { e.classList.add("blink"); });
      setTimeout(function () { eyes.forEach(function (e) { e.classList.remove("blink"); }); cb && cb(); }, 165);
    }
    function buildProgram() {
      var prog = [], degs = [0, 90, 180, 270];
      [{ t: "q" }, { t: "h" }, { t: "p" }].forEach(function (stage) {
        for (var i = 0; i < 3; i++) {
          if (stage.t === "q") prog.push({ g: QUARTER, d: pick(degs) });
          else if (stage.t === "h") prog.push({ g: pick(HALF), d: 0 });
          else prog.push({ g: POINT, d: 0 });
        }
        prog.push({ blink: true });
      });
      return prog;
    }
    if (reduceMotion) { setEyes(QUARTER, 0); return; }
    var prog = buildProgram(), idx = 0;
    function step() {
      if (myGen !== gazeGen) return; // mood cambió — esta cadena de timeouts muere acá
      var s = prog[idx];
      idx = (idx + 1) % prog.length;
      if (idx === 0) prog = buildProgram();
      if (s.blink) { blink(function () { setTimeout(step, 220); }); }
      else { setEyes(s.g, s.d); setTimeout(step, 1500 + Math.random() * 900); }
    }
    setTimeout(step, 700 + Math.random() * 400);
  }

  var curMood = null;
  // moodLabel (T188): el ánimo del backend (state.ValidMoods) en el idioma del
  // tablero. Un case por ánimo con la clave escrita entera en su llamada a t:
  // el test de paridad del catálogo sólo ve las claves escritas así, no las
  // armadas con +.
  function moodLabel(mood) {
    switch (mood) {
      case "idle": return t("mood.idle");
      case "zero": return t("mood.zero");
      case "new_msg": return t("mood.new_msg");
      case "few": return t("mood.few");
      case "swamped": return t("mood.swamped");
      case "reading": return t("mood.reading");
      case "switching": return t("mood.switching");
      case "thinking": return t("mood.thinking");
      case "working": return t("mood.working");
      case "responding": return t("mood.responding");
      case "done": return t("mood.done");
      case "ai_online": return t("mood.ai_online");
      case "vip": return t("mood.vip");
      case "muted": return t("mood.muted");
      case "sleeping": return t("mood.sleeping");
      case "alert": return t("mood.alert");
      case "error": return t("mood.error");
      case "qr": return t("mood.qr");
    }
    return mood;
  }
  // renderMoodLabel: aparte de applyMood porque applyAllI18n() lo repinta al
  // cambiar de idioma — applyMood no hace nada si el ánimo no cambió.
  function renderMoodLabel() {
    document.getElementById("moodlabel").textContent = curMood ? moodLabel(curMood) : "";
  }
  function applyMood(mood) {
    mood = mood || "idle";
    if (mood === curMood) return;
    curMood = mood;
    renderMoodLabel();
    var face = document.getElementById("face");
    var literal = MOOD_FACES[mood];
    gazeGen++;
    if (literal) {
      face.innerHTML = "";
      face.textContent = literal;
    } else {
      face.innerHTML = IDLE_FACE_HTML;
      animateFace(face, gazeGen);
    }
  }

  // ── vinculación (ct-2026-07-19-1735, S1f) — gate por WhatsApp vinculado,
  // no solo por sesión: sin WhatsApp conectado, el panel admin (estado,
  // conversaciones, lectura) queda oculto y en su lugar se muestra la
  // pantalla de QR (reusada tal cual — #qroverlay ya existía) hasta que
  // clearErrorState (whatsmeow) marca wa_connected=true. ──────────────────
  function setQROverlay(visible) {
    var el = document.getElementById("qroverlay");
    el.classList.toggle("hidden", !visible);
    document.body.classList.toggle("qr-open", visible);
  }
  function applyLinkGate(s) {
    document.getElementById("adminpanel").classList.toggle("hidden", !s.connected);
    var qrVisible = !document.getElementById("qroverlay").classList.contains("hidden");
    if (s.connected) {
      setQROverlay(false);
      document.getElementById("qrbtn").classList.add("hidden");
      document.getElementById("disconnectbtn").classList.remove("hidden");
      // T116 (ct-2026-09-01-2040): #editprofilebtn ya no se togglea acá —
      // queda oculto siempre (ver el comentario junto al botón en
      // index.html, pedido explícito del dueño).
      return;
    }
    document.getElementById("qrbtn").classList.remove("hidden");
    document.getElementById("disconnectbtn").classList.add("hidden");
    // P1 (ct-2026-07-24-0015): QR overlay NO se abre solo al desconectar —
    // solo cuando el boss hace click en "Conectar QR". Pero precargamos el
    // QR data para que el overlay tenga algo que mostrar al abrirse.
    var note = document.getElementById("qrnote");
    if (s.show_qr) {
      var qrURL = withKey("/api/qr/image");
      qrURL += (qrURL.indexOf("?") === -1 ? "?" : "&") + "_=" + Date.now();
      document.getElementById("qrimg").src = qrURL;
      note.textContent = t("qr.scan_instructions");
    } else {
      document.getElementById("qrimg").removeAttribute("src");
      document.getElementById("qrimg").classList.add("hidden");
      // Con qr_error el motivo (qrUpdateTimer) habla solo: "Conectando…"
      // encima lo contradeciría.
      note.textContent = s.qr_error ? "" : t("qr.connecting_note");
    }
  }
  // T188: el <img> del QR sólo se ve cuando hay un QR cargado. Sin src, o con
  // el pedido fallido (todavía no hay código), quedaba el cuadrado blanco con
  // el ícono de imagen rota y el alt encima.
  (function () {
    var img = document.getElementById("qrimg");
    img.addEventListener("load", function () { img.classList.remove("hidden"); });
    img.addEventListener("error", function () { img.classList.add("hidden"); });
  })();

  // loadStatus doubles as the auth probe (ct-2026-07-19-1616, S1d): every
  // /api/* call is session-or-key gated now, so a 401 here — on first boot
  // OR mid-session if the session expired/was invalidated by a password
  // change — means "show the login overlay", handled once, right where the
  // failure actually surfaces instead of a separate parallel check.
  // ownNumberDisplay: own_number llega como JID crudo de whatsmeow
  // ("55500000042:8@s.whatsapp.net") — el header solo necesita el número
  // pelado, sin el sufijo de dispositivo (":8") ni el dominio (@...).
  // T94: NO pasa por maskNumber — el propio número es SIEMPRE una sola
  // fila, nunca hace falta distinguirlo de otro, así que se tapa entero
  // (sin prefijo ni tag) en vez del tapado parcial que sí necesitan los
  // números de chat, donde distinguir una fila de otra es todo el punto.
  function ownNumberDisplay(jid) {
    if (!jid) return "—";
    if (state.privacyMode) return "•••••••";
    return jid.split("@")[0].split(":")[0];
  }

  // initialsFor: hasta 2 letras de label ("Contacto Uno" -> "CU", "hola" ->
  // "H", "" -> "#"). Nunca vacío — T17 Parte 3's "sin foto, sin hueco"
  // también cubre el lado del texto, no solo la imagen.
  // T188 (hallazgo de Calcite): sólo cuentan las palabras que empiezan con
  // letra ("Ana 🌸" -> "A") y el primer carácter se toma entero con
  // Array.from — charAt(0) partía un emoji (par sustituto) a la mitad y salía
  // "A�". Un nombre sin ninguna letra ("🌸", "555") usa su primer
  // carácter entero.
  function initialsFor(label) {
    var words = (label || "").trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return "#";
    var first = function (w) { return Array.from(w)[0]; };
    var lettered = words.filter(function (w) { return /^\p{L}/u.test(w); });
    if (lettered.length === 0) return first(words[0]);
    if (lettered.length === 1) return first(lettered[0]).toUpperCase();
    return (first(lettered[0]) + first(lettered[1])).toUpperCase();
  }

  function initialsSpan(label) {
    var span = document.createElement("span");
    span.className = "avatar-initials";
    span.textContent = initialsFor(label);
    return span;
  }

  // buildAvatar (T17 Parte 3, ct-2026-08-05-1240): un círculo con la foto
  // cacheada (GET /api/avatar — sirve lo que ya haya en disco y de paso
  // dispara, del lado del gateway, un chequeo paceado bajo demanda; nunca
  // espera esa red acá) o, si no hay nada todavía / el pedido falla,
  // iniciales — nunca un cuadrado vacío ni un ícono roto. Sin jid (cuenta
  // todavía no conectada), ni siquiera intenta la imagen.
  function buildAvatar(jid, label, sizeClass) {
    var wrap = document.createElement("div");
    wrap.className = "avatar " + sizeClass;
    if (!jid) {
      wrap.appendChild(initialsSpan(label));
      return wrap;
    }
    var img = document.createElement("img");
    img.alt = "";
    img.onerror = function () {
      img.remove();
      wrap.appendChild(initialsSpan(label));
    };
    // El parametro de tiempo NO es decorativo (2026-09-02): sin el, el
    // navegador que guardo esta imagen ANTES de que /api/avatar mandara
    // Cache-Control sigue usando su copia vieja y ni siquiera pregunta —
    // el dueno reporto tres veces que no veia su foto nueva y solo aparecia
    // forzando la recarga con Ctrl+Shift+R. Cambiando la URL cada 5 minutos
    // el navegador la trata como otra imagen y la pide de verdad.
    // ponytail: invalidacion por TIEMPO, no por contenido. Lo correcto es
    // el PictureID que WhatsApp ya le da a cada foto (store.Avatar.PictureID,
    // avatar.go:14) y que cambia solo cuando la foto cambia — pero hoy no
    // llega al tablero. Cuando /api/status lo exponga, reemplazar esto por
    // "&v=" + pictureID y sacar el reloj.
    var bust = Math.floor(Date.now() / 300000);
    img.src = withKey("/api/avatar?jid=" + encodeURIComponent(jid) + "&v=" + bust);
    wrap.appendChild(img);
    return wrap;
  }

  // applyAccountIdentity (S3, ct-2026-09-20-1202) — same safeguard S2 built
  // for the tray, moved to the browser: two dashboards in two tabs are
  // today as indistinguishable as the two tray icons were before S2.
  // account/accountColor come straight from GET /api/status, which reports
  // EXACTLY what config.ColorForAccount (Go) computed — this function only
  // paints with it, never derives its own color (same split as
  // trayicon_recolor.go on the Go side). No account: the brand chip and
  // the tab title stay EXACTLY as before S3, byte for byte — same
  // condition S1/S2 already put on the no-account case.
  function applyAccountIdentity(account, accountColor) {
    var el = document.getElementById("brandaccount");
    if (!account) {
      el.classList.add("hidden");
      el.textContent = "";
      el.style.color = "";
      document.title = "Piumy Gateway";
      return;
    }
    el.textContent = t("account.label", { account: account });
    el.style.color = accountColor || "";
    el.classList.remove("hidden");
    document.title = "Piumy Gateway — " + account;
  }

  // statNumber (T172a, ct-2026-11-10-2000): una cifra del estado, o "—" si
  // el campo no vino. Vale para las tres (cola, enviados, agentes) y para
  // cualquier otra que se sume: lo que decide es si HAY número, no cuál es.
  // 0 es un dato tan válido como 7 y se muestra tal cual; null, undefined o
  // un campo que el backend dejó de mandar NO son cero. Number.isFinite
  // descarta de paso NaN e Infinity, que en pantalla serían ruido.
  function statNumber(v) {
    return Number.isFinite(v) ? String(v) : "—";
  }

  // setBadgeDot (T172b tramo 3): pinta en una insignia de la barra de salud
  // un punto de color y, si hace falta, una palabra al lado. `estado` es
  // "ok" | "off" | "bad", y el color de cada uno lo decide el CSS (.pip),
  // no esta función: acá sólo se dice QUÉ pasa, no de qué color se ve.
  // Reemplaza los emojis que se escribían a mano en cada sitio.
  function setBadgeDot(id, estado, texto) {
    var el = document.getElementById(id);
    if (!el) return;
    el.replaceChildren();
    var punto = document.createElement("span");
    punto.className = "pip " + estado;
    el.appendChild(punto);
    if (texto) el.appendChild(document.createTextNode(" " + texto));
  }

  function loadStatus() {
    return api("/api/status").then(function (s) {
      state.lastStatus = s;
      hideLogin();
      applyAccountIdentity(s.account, s.account_color);
      var heroAvatarWrap = document.getElementById("heroavatar");
      var heroNum = document.getElementById("num");
      heroNum.classList.toggle("prose", !s.connected);
      if (s.connected) {
        document.getElementById("name").textContent = s.name || t("data.unnamed");
        heroNum.textContent = ownNumberDisplay(s.own_number);
        heroAvatarWrap.replaceChildren(buildAvatar(s.own_number, s.name, "avatar-xl"));
      } else {
        document.getElementById("name").textContent = t("hero.name_disconnected");
        heroNum.textContent = t("hero.number_disconnected");
        heroAvatarWrap.replaceChildren();
      }
      var st = document.getElementById("status");
      if (s.connected) {
        st.textContent = "\ud83d\udfe2 " + t("status.connected") + (s.own_number ? " \u00b7 " + ownNumberDisplay(s.own_number) : "");
        st.classList.remove("disconnected");
      } else {
        st.textContent = "\ud83d\udd34 " + t("status.disconnected");
        st.classList.add("disconnected");
      }
      // T172b tramo 3: los estados de la barra de salud se dicen con un
      // punto de color, no con un emoji. El visto y el circulito eran lo
      // ultimo que quedaba del tablero viejo en esa fila, y un emoji cambia
      // de dibujo en cada sistema: el punto se ve igual en todos y hereda
      // el color del tema. Donde hay algo que LEER (el kill del governor,
      // "apagado" del cifrado) el texto se queda: el punto dice el estado,
      // la palabra dice el detalle.
      setBadgeDot("badgewa", s.connected ? "ok" : "bad");
      if (s.governor_killed) {
        document.getElementById("badgegovernor").textContent = t("badge.governor_killed");
      } else {
        setBadgeDot("badgegovernor", "ok");
      }
      document.getElementById("badgebackup").textContent = t("badge.backup_summary", {
        chats: s.backup_chats || 0,
        groups: s.backup_groups || 0,
        contacts: s.backup_contacts || 0,
        numbers: s.backup_numbers || 0,
      });
      setBadgeDot("badgeantena", s.antenna_configured ? "ok" : "off");
      setBadgeDot("badgecifrado", s.backup_encrypted ? "ok" : "off", s.backup_encrypted ? "" : t("badge.off"));
      document.getElementById("badgehistory").textContent = (s.history_loaded || 0) + " / " + (s.history_total || 0);
      var syncEl = document.getElementById("badgesync");
      if (s.history_sync_active) {
        var ago = s.history_sync_last_chunk_ago_sec;
        var agoText = ago < 5 ? t("time.now") : ago < 60 ? t("time.ago_seconds", { n: ago }) : t("time.ago_minutes", { n: Math.round(ago / 60) });
        syncEl.textContent = t("sync.summary", { chats: s.history_sync_chats || 0, msgs: s.history_sync_messages || 0, ago: agoText });
        syncEl.style.color = "#b3e6b3";
      } else {
        syncEl.textContent = "—";
        syncEl.style.color = "";
      }
      // T172a (ct-2026-11-10-2000): statNumber, no `s.queue || 0`. Con el
      // `||` un campo que NO VINO (backend mudo, respuesta recortada, un
      // rename del lado Go) se veía idéntico a un cero real: "0 en cola"
      // cuando la verdad es "no sé cuántos hay". Son dos estados distintos
      // y la pantalla tiene que distinguirlos — un cero de verdad sigue
      // saliendo 0, lo ausente sale "—", como ya hace #badgesync.
      document.getElementById("statqueue").textContent = statNumber(s.queue);
      document.getElementById("statsent").textContent = statNumber(s.sent);
      document.getElementById("statagents").textContent = statNumber(s.agents);
      // T9 (ct-2026-08-05-1137): factory_password ya viene resuelto del
      // servidor (bcrypt contra el hash guardado) — acá solo mostramos u
      // ocultamos, nunca comparamos ni vemos una contraseña. Corre en
      // cada poll de loadStatus (15s) y justo después de un login exitoso
      // (submitLogin), así que desaparece sola al cambiarla — sin lógica
      // aparte, sin recargar.
      document.getElementById("factorypwalert").classList.toggle("hidden", !s.factory_password);
      // T25 (ct-2026-08-05-1833, hallazgo 2): default_terminal_configured
      // ya viene resuelto del servidor (env O antena, ver main.go's
      // resolveDefaultTerminalID) — false solo cuando ninguna de las dos
      // está puesta, mismo criterio que factory_password de arriba.
      document.getElementById("noterminalalert").classList.toggle("hidden", !!s.default_terminal_configured);
      applyMood(s.mood);
      applyLinkGate(s);
      qrUpdateTimer(s);
    }).catch(function (e) {
      showLogin();
      throw e;
    });
  }

  // renderHeroStatusView(text): arma #herostatus en modo VISTA — el texto
  // (o vacío, el CSS ":empty::before" pinta hero.status_placeholder vía
  // attr(data-placeholder) — T153 2b-i: el texto ya no vive hardcodeado en
  // style.css, ver el comentario ahí) + el lápiz. Un solo lugar arma este
  // HTML — lo comparten loadProfileStatus (carga inicial), "Cancelar" y
  // "guardado con éxito" en modo edición, para que los tres terminen
  // exactamente en el mismo estado.
  function renderHeroStatusView(text) {
    var el = document.getElementById("herostatus");
    el.replaceChildren();
    var span = document.createElement("span");
    span.className = "hero-status-text";
    span.id = "herostatus_text";
    span.textContent = text || "";
    span.setAttribute("data-placeholder", t("hero.status_placeholder"));
    var pencil = document.createElement("button");
    pencil.type = "button";
    pencil.className = "hero-status-pencil";
    pencil.id = "herostatus_pencil";
    pencil.title = t("hero.edit_status");
    pencil.setAttribute("aria-label", t("hero.edit_status"));
    pencil.textContent = "✎";
    pencil.addEventListener("click", function () { openHeroStatusEdit(text || ""); });
    el.appendChild(span);
    el.appendChild(pencil);
  }

  // openHeroStatusEdit (T116, ct-2026-09-01-2040): el lápiz junto al
  // estado — editar ahí mismo, sin abrir #profilemodal. Mismo POST
  // /api/admin/profile-status que el modal ya usa (NO un segundo camino,
  // pedido explícito del contrato) y el mismo patrón de aviso que
  // profile_status_save (más abajo): botón deshabilitado + "Guardando…"
  // mientras dura — una operación contra WhatsApp puede tardar hasta 75
  // segundos medidos, sin esta señal se lee como un control roto. Guarda
  // SOLO al confirmar (nunca con cada tecla) y, al éxito, repinta con
  // renderHeroStatusView — sin recargar la página.
  function openHeroStatusEdit(currentText) {
    var el = document.getElementById("herostatus");
    el.replaceChildren();
    var input = document.createElement("input");
    input.className = "inp hero-status-input";
    input.type = "text";
    input.value = currentText;
    var saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "btn pri sm";
    saveBtn.textContent = t("action.save");
    var cancelBtn = document.createElement("button");
    cancelBtn.type = "button";
    cancelBtn.className = "btn gho sm";
    cancelBtn.textContent = t("action.cancel");
    var errSpan = document.createElement("span");
    errSpan.className = "hero-status-error";
    cancelBtn.addEventListener("click", function () { renderHeroStatusView(currentText); });
    saveBtn.addEventListener("click", function () {
      var newText = input.value;
      saveBtn.disabled = true;
      cancelBtn.disabled = true;
      input.disabled = true;
      saveBtn.textContent = t("action.saving");
      errSpan.textContent = "";
      post("/api/admin/profile-status", { status: newText }).then(function () {
        renderHeroStatusView(newText);
      }).catch(function (e) {
        saveBtn.disabled = false;
        cancelBtn.disabled = false;
        input.disabled = false;
        saveBtn.textContent = t("action.save");
        errSpan.textContent = t("error.prefix") + e.message;
      });
    });
    el.appendChild(input);
    el.appendChild(saveBtn);
    el.appendChild(cancelBtn);
    el.appendChild(errSpan);
    input.focus();
  }

  // loadProfileStatus (T96, ct-2026-08-28-1743): el estado/"About" de
  // WhatsApp bajo el nombre. GetProfileStatus no cachea (lee vivo cada
  // llamada, ver whatsmeow_catalog.go) — a diferencia de loadStatus/15s de
  // arriba, esta NO va al poll: es "un adorno, no un dato crítico" (Citrino),
  // no vale una consulta usync a WhatsApp cada 15s para siempre. Se llama
  // una vez al cargar la página y de nuevo cada vez que se abre el modal de
  // Config (mismo patrón que dispatch-debounce, T90).
  // T116 (ct-2026-09-01-2040): el renglón YA NO se oculta con estado vacío
  // — antes (T96) un estado en blanco escondía #herostatus entero (correcto
  // cuando era de solo lectura); ahora que ES el lugar donde se edita, tiene
  // que estar SIEMPRE (el lápiz solo, o el placeholder tenue del CSS,
  // invitan a ponerlo).
  function loadProfileStatus() {
    return api("/api/admin/profile-status").then(function (d) {
      renderHeroStatusView(d.status || "");
    }).catch(function () {
      renderHeroStatusView("");
    });
  }

  // Sort is entirely client-side over the already-loaded list — no re-query
  // to the DB just to reorder (the boss's own call: live-sorting the DB is
  // a bad idea). A click on sortopts commits state.appliedSort+render
  // immediately (segunda vuelta, boss: "igual que la barra de busqueda") —
  // no intermediate "pending" state, see sortopts' own click listener.
  var SORTERS = {
    recientes: function (a, b) { return b.last_ts - a.last_ts; },
    nivel: function (a, b) { return levelRank(a.level) - levelRank(b.level); },
    ignorados: function (a, b) { return Number(isIgnored(a)) - Number(isIgnored(b)); },
  };
  function levelRank(l) { return { boss: 0, caution: 1, danger: 2 }[l] ?? 3; }
  function isIgnored(c) { return c.status === "ignored"; }

  // chatLevel (punto 6 parte 2, ct-2026-07-21-1234): el nivel unificado ya
  // viene CALCULADO del backend (store.ConfigLevel, campo config_level de
  // GET /api/chats) — boss/auto/confirm/unattended/ignored. Antes
  // (subcontrato S2) esto se recalculaba acá mismo a partir de
  // is_boss/status/confirmation_mode/mode, duplicando store.ConfigLevel;
  // ahora hay una sola fuente de verdad (el backend), esta función solo la
  // lee. Fallback "unattended" si config_level no viniera — mismo default
  // seguro que ya usa store.ConfigLevel.
  function chatLevel(c) { return c.config_level || "unattended"; }
  // LEVELS: orden VISUAL de las 5 secciones del tab Chats + las opciones del
  // selector de nivel (renderLevelCell) + la leyenda. `short` alimenta las
  // <option> del selector (tienen que ser cortas para no volver a ensanchar
  // la columna); `label` alimenta el header de sección (más descriptivo, sin
  // límite de ancho).
  var LEVELS = [
    { key: "boss", icon: "⭐", label: "Boss", short: "BOSS" },
    { key: "auto", icon: "🟢", label: "Respuesta automática", short: "Auto" },
    { key: "confirm", icon: "🟡", label: "Con tu confirmación", short: "Confirmación" },
    { key: "unattended", icon: "⚪", label: "Sin atender", short: "Sin atender" },
    { key: "ignored", icon: "⚫", label: "Ignorados", short: "Ignorado" },
  ];
  var LEVEL_BY_KEY = {};
  LEVELS.forEach(function (b) { LEVEL_BY_KEY[b.key] = b; });
  // levelLabel (T153 2b-iv): completa lo que agentTypeLabel/originLevelShort
  // (2b-iii, más abajo) ya empezaron — el ARRAY LEVELS queda con .label sin
  // tocar a propósito (2b-iii solo traducía .short); acá se resuelve igual,
  // if-chain con t("clave literal"), nunca un mapa {key: "clave"}, porque
  // TestAllKeysMatchBetweenFrontendAndCatalog lee app.js con un grep y no
  // ejecuta el JS. "boss" devuelve el fallback sin traducir — "Boss" es la
  // misma palabra en los dos idiomas (mismo criterio que "BOSS" en
  // originLevelShort).
  function levelLabel(key, fallback) {
    if (key === "auto") return t("level.label_auto");
    if (key === "confirm") return t("level.label_confirm");
    if (key === "unattended") return t("level.label_unattended");
    if (key === "ignored") return t("level.label_ignored");
    return fallback;
  }

  // isRealConversation: the Chats-tab filter — "Chats" means real
  // conversations only, not every address-book contact or group-only
  // number. Contacts with zero messages still exist in state.chats (type
  // p2p) — they just move to the Contactos tab (renderContacts) instead of
  // showing here.
  //
  // T18 (ct-2026-08-05-1243, boss: su bandeja mezclaba gente que le
  // escribió con números que solo aparecieron en un grupo) — was
  // has_messages (store.ChatJIDsWithMessages' criterion); now c.origin ===
  // "inbound_spoke" (store.ChatOrigin, T18-fixed to count a real message in
  // EITHER direction — a chat the boss started and nobody answered still
  // counts). Same underlying "does this chat have a real message" question
  // as has_messages, one source now instead of two computations that could
  // quietly disagree — see docs/T18-DIAGRAMA-SEPARAR-POR-ORIGEN.md.
  function isRealConversation(c) { return c.origin === "inbound_spoke"; }

  // historyBadge (ct-2026-07-21-1837): ícono sutil de history_state junto al
  // nombre — "pending" (o ausente, chat no tocado aún por el worker) no
  // dibuja nada, per contrato ("nada si pending").
  var HISTORY_BADGES = {
    downloading: { cls: "hdownloading", glyph: " ⏳" },
    loaded: { cls: "hloaded", glyph: " ✓" },
  };
  // historyBadgeTitle (T153 2b-iv, sin fallback desde 2b-v): a diferencia de
  // levelLabel/agentTypeLabel — que SÍ necesitan un fallback porque LEVELS/
  // AGENT_DEFAULT_TYPES tienen una clave ("boss") que de verdad cae afuera
  // del if-chain — acá las DOS claves de HISTORY_BADGES están cubiertas
  // siempre. Un fallback nunca alcanzable solo sería texto en español
  // durmiendo en el objeto sin ninguna función real (el barrido por patrón
  // de Citrino en 2b-v encontró exactamente ese texto muerto) — se sacó el
  // campo `title` del objeto en vez de justificarlo como excepción.
  function historyBadgeTitle(state) {
    if (state === "downloading") return t("history.tooltip_downloading");
    if (state === "loaded") return t("history.tooltip_loaded");
  }
  function historyBadge(c) {
    var b = HISTORY_BADGES[c.history_state];
    if (!b) return null;
    var span = document.createElement("span");
    span.className = "hbadge " + b.cls;
    span.title = historyBadgeTitle(c.history_state);
    span.textContent = b.glyph;
    return span;
  }

  function isGroup(jid) { return jid.indexOf("@g.us") !== -1; }
  // jidNumber: the part before "@" — the real phone number for
  // @s.whatsapp.net, but still the unresolved @lid pseudo-id when WhatsApp
  // delivered via LID (ct-2026-07-11-0030 Fase 2 fixes that; Fase 1 shows
  // whatever's real today, doesn't fake a number that isn't resolved yet).
  // Tolerant of undefined/null (ct-2026-07-29 render-crash regression): a
  // caller like renderRow does jidNumber(c.resolved_number) || jidNumber(c.jid)
  // relying on a falsy first result to fall through — that only holds if
  // jidNumber never throws on a missing value. The real fix is the backend
  // always sending resolved_number (no omitempty); this stays as the
  // function's own contract, not a patch for that one bug.
  function jidNumber(jid) { return (jid || "").split("@")[0]; }
  function pushNameSuffix(c) { return c.pushname && c.pushname !== c.name ? t("chat.alias_prefix") + c.pushname : ""; }
  function timeAgo(ts) {
    if (!ts) return t("time.no_messages");
    var mins = Math.max(0, Math.round((Date.now() / 1000 - ts) / 60));
    if (mins < 1) return t("time.ago_moments");
    if (mins < 60) return t("time.ago_minutes_verbose", { n: mins });
    var hrs = Math.round(mins / 60);
    if (hrs < 24) return t("time.ago_hours", { n: hrs });
    return t("time.ago_days", { n: Math.round(hrs / 24) });
  }

  // /api/chats returns one flat list tagged by type (S1g, ct-2026-07-19-1801):
  // "p2p" (real 1:1, existing table, unchanged), "group" (an @g.us row,
  // collapsible header) and "group_member" (synthetic, projected from
  // group_members — not a real chat, just a name+number under a group).
  // Splitting here keeps every existing p2p sort/filter/render function
  // untouched; only render() and the new renderGroups() need to know the
  // three arrays exist.
  function loadChats() {
    return api("/api/chats").then(function (all) {
      state.chats = all.filter(function (c) { return c.type === "p2p"; });
      state.groups = all.filter(function (c) { return c.type === "group"; });
      state.groupMembers = all.filter(function (c) { return c.type === "group_member"; });
      render();
    });
  }

  // Filtro de búsqueda: client-side sobre lo ya cargado (misma lógica que el
  // sort — no re-consulta la DB solo para achicar la lista visible). Sin
  // acentos en ambos lados: "mama" tiene que encontrar "Mamá".
  // Combining Diacritical Marks block is U+0300..U+036F.
  function foldAccents(s) {
    return s.normalize("NFD").split("").filter(function (ch) {
      var code = ch.codePointAt(0);
      return code < 0x300 || code > 0x36f;
    }).join("");
  }
  // matchesNeedle is the one accent-folded name/number match rule, shared
  // by the p2p table filter and the group/member search below — no reason
  // to duplicate it (both compare a display name + jid against the query).
  function matchesNeedle(name, jid, needle) {
    return foldAccents((name || "").toLowerCase()).indexOf(needle) !== -1 || jidNumber(jid).indexOf(needle) !== -1;
  }
  function matchesFilter(c) {
    if (!state.filterText) return true;
    return matchesNeedle(c.name, c.jid, foldAccents(state.filterText.toLowerCase()));
  }

  function render() {
    // Chats = SOLO conversaciones reales (origin === "inbound_spoke", T18) —
    // el filtro corre ANTES del agrupamiento por nivel (config_level, punto
    // 6) más abajo. Los contactos sin conversación real (agenda pura, o
    // vistos solo en un grupo) viven en el tab Contactos (renderContacts),
    // no acá.
    var sorted = state.chats.filter(isRealConversation).filter(matchesFilter).sort(SORTERS[state.appliedSort] || SORTERS.recientes);
    var tbody = document.getElementById("chatrows");
    tbody.innerHTML = "";
    // Agrupar por config_level (punto 6 parte 2) — el sort ya aplicado
    // decide el orden DENTRO de cada sección; LEVELS decide el orden de las
    // secciones mismas. Una sección sin ningún chat no se dibuja (nada de
    // headers vacíos).
    var byLevel = { boss: [], auto: [], confirm: [], unattended: [], ignored: [] };
    sorted.forEach(function (c) { byLevel[chatLevel(c)].push(c); });
    LEVELS.forEach(function (b) {
      var list = byLevel[b.key];
      if (!list.length) return;
      tbody.appendChild(renderSectionHeader(b));
      // Una fila que falla al dibujarse no puede tumbar la lista entera
      // (ct-2026-07-29, regresión: un TypeError en una sola fila mataba
      // render() a mitad de camino, dejando solo la primera sección
      // dibujada — y con ella se perdía renderGroups/renderContacts
      // también, llamados al final de esta función). Se saltea SOLO esa
      // fila y queda logueada con el dato culpable — el bug sigue visible
      // en consola en vez de taparse en silencio.
      list.forEach(function (c) {
        try {
          tbody.appendChild(renderRow(c));
        } catch (e) {
          console.error("render: fila de chat salteada por error", c, e);
        }
      });
    });
    // T188: sin filas la tabla se quedaba con sólo sus encabezados y la leyenda
    // debajo, que se leía como una fila más ("BOSS"). Ahora dice por qué está
    // vacía, y la leyenda — que explica filas que no hay — se esconde.
    var emptyTable = !tbody.children.length;
    if (emptyTable) tbody.appendChild(renderEmptyRow());
    document.getElementById("legend").classList.toggle("hidden", emptyTable);
    var totalLoaded = state.chats.length + state.groups.length + state.groupMembers.length;
    document.getElementById("more").textContent = totalLoaded >= 5000 ? t("chat.load_limit_notice", { n: 5000 }) : "";
    renderGroups();
    renderContacts();
  }

  // renderEmptyRow: la fila única de una tabla sin conversaciones — con la
  // búsqueda activa dice que nada coincide, sin ella que todavía no hay.
  function renderEmptyRow() {
    var tr = document.createElement("tr");
    var td = document.createElement("td");
    td.className = "list-empty";
    td.colSpan = document.querySelectorAll("#chattable thead th").length || 4;
    td.textContent = state.filterText ? t("chat.empty_filter") : t("chat.empty_none");
    tr.appendChild(td);
    return tr;
  }

  // renderSectionHeader: una <tr> de ancho completo (colspan = nº de
  // columnas de #chattable) con el look .list-sub del mockup aprobado
  // (dashboard-mockup.html) — mismo nombre de clase, mismo estilo dim/
  // uppercase, sticky debajo del thead (ver CSS: top offset != 0 porque acá
  // SÍ hay un thead sticky arriba, a diferencia del mockup).
  function renderSectionHeader(b) {
    var tr = document.createElement("tr");
    var td = document.createElement("td");
    td.className = "list-sub";
    td.colSpan = document.querySelectorAll("#chattable thead th").length || 3;
    td.textContent = b.icon + " " + levelLabel(b.key, b.label);
    tr.appendChild(td);
    return tr;
  }

  // ── grupos colapsables (S1g, ct-2026-07-19-1801) ────────────────────────
  // Colapsado por default, persistido por grupo en localStorage — "yo
  // colapso el grupo y no veo los items, veo que el grupo tiene X" (boss
  // verbatim). Mientras hay búsqueda activa, un grupo con algún miembro (o
  // el propio nombre del grupo) que matchea se fuerza expandido, así la
  // búsqueda "atraviesa" un grupo colapsado en vez de esconder el resultado.
  var COLLAPSE_KEY = "piumy_collapsed_groups";
  function isCollapsed(jid) {
    var saved = {};
    try { saved = JSON.parse(localStorage.getItem(COLLAPSE_KEY) || "{}"); } catch (e) {}
    return saved[jid] !== false; // unset -> collapsed (the documented default)
  }
  function setCollapsed(jid, collapsed) {
    var saved = {};
    try { saved = JSON.parse(localStorage.getItem(COLLAPSE_KEY) || "{}"); } catch (e) {}
    saved[jid] = collapsed;
    localStorage.setItem(COLLAPSE_KEY, JSON.stringify(saved));
  }

  function renderGroups() {
    var container = document.getElementById("grouplist");
    container.innerHTML = "";
    var needle = state.filterText ? foldAccents(state.filterText.toLowerCase()) : "";

    var groups = state.groups.slice().sort(function (a, b) {
      return (a.name || jidNumber(a.jid)).localeCompare(b.name || jidNumber(b.jid));
    });

    groups.forEach(function (g) {
      var members = state.groupMembers.filter(function (m) { return m.group_jid === g.jid; });
      var visibleMembers = members;
      var forceExpand = false;
      if (needle) {
        var groupNameMatches = matchesNeedle(g.name, g.jid, needle);
        var matchingMembers = members.filter(function (m) { return matchesNeedle(m.name, m.jid, needle); });
        if (!groupNameMatches && matchingMembers.length === 0) return; // no match anywhere in this group — hide it while searching
        visibleMembers = groupNameMatches ? members : matchingMembers;
        forceExpand = matchingMembers.length > 0;
      }
      var collapsed = forceExpand ? false : isCollapsed(g.jid);

      var header = document.createElement("div");
      header.className = "groupheader";
      var toggle = document.createElement("span");
      toggle.className = "grouptoggle";
      toggle.textContent = collapsed ? "▶" : "▼";
      var name = document.createElement("span");
      name.className = "groupname";
      name.textContent = g.name || jidNumber(g.jid);
      // Tramo D (ct-2026-07-22-123556): click en el nombre abre el detalle
      // del grupo (mismo popup que un chat/contacto, reusado — ver
      // openGroupDetail) — stopPropagation porque el resto del header
      // colapsa/expande, un solo click no puede hacer las dos cosas.
      name.onclick = function (ev) { ev.stopPropagation(); openGroupDetail(g); };
      var count = document.createElement("span");
      count.className = "groupcount";
      count.textContent = t("group.member_count", { n: members.length, s: members.length === 1 ? "" : "s" });
      header.appendChild(toggle);
      // T116 punto 7 (ct-2026-09-01-2040), pedido del dueño verbatim: "que
      // los grupos muestren la foto del grupo". buildAvatar ya existe y ya
      // lo usa renderRow para chats — el camino de avatares (/api/avatar,
      // RequestAvatar/checkAvatar) no distingue @g.us de un contacto,
      // verificado leyendo whatsmeow's GetProfilePictureInfo: la única
      // rama especial es IsCommunity, que este código nunca pide. Un grupo
      // sin foto es el caso común — mismo fallback a iniciales que
      // buildAvatar ya resuelve para cualquier chat, nada nuevo acá.
      header.appendChild(buildAvatar(g.jid, g.name || jidNumber(g.jid), "avatar-sm"));
      header.appendChild(name);
      header.appendChild(count);
      // Nivel/reglas del grupo (Tramo C, ct-2026-07-22-1235) — mismo par que
      // la tabla Chats; stopPropagation porque el header entero es
      // clickeable para colapsar/expandir y estos controles no deben
      // disparar ese toggle.
      var groupControls = document.createElement("span");
      groupControls.className = "groupcontrols";
      groupControls.onclick = function (ev) { ev.stopPropagation(); };
      groupControls.appendChild(buildLevelControl(g));
      groupControls.appendChild(buildAgentAssignControl(g));
      groupControls.appendChild(buildRulesControl(g));
      header.appendChild(groupControls);
      header.onclick = function () { setCollapsed(g.jid, !isCollapsed(g.jid)); render(); };
      container.appendChild(header);

      var list = document.createElement("div");
      list.className = "groupmembers" + (collapsed ? " hidden" : "");
      visibleMembers.forEach(function (m) {
        list.appendChild(contactRow(m.name || t("data.unnamed"), m.jid, m.resolved_number, m));
      });
      container.appendChild(list);
    });
  }

  // groupMemberDecisionMark (T66c, ct-2026-08-11-1843): a group-only
  // member the owner decided something about (assigned to an agent,
  // pinned boss/approver) must say so where the boss actually looks —
  // the group's own member list/popup — instead of looking like an
  // unremarkable member (T66b made the data travel in /api/chats; nothing
  // painted it). Speaks the SAME visual language buildLevelControl/
  // buildAgentAssignControl already use in the Conversaciones table (★
  // ámbar for boss, the approver icon, the assigned agent's name) —
  // "usá el precedente antes que inventar" — but READ-ONLY on purpose:
  // this is an indicator, not a control. Assigning/approving stays the
  // Conversaciones table's job alone; whether that should change is a
  // product call for the owner, not decided here.
  // Returns null when m carries no decision at all (the common case —
  // most group members are exactly agenda noise, unchanged from before).
  function groupMemberDecisionMark(m) {
    var wrap = document.createElement("span");
    wrap.className = "memberdecision";
    var any = false;
    if (m.is_boss) {
      var star = document.createElement("span");
      star.className = "indicator boss";
      star.title = t("role.owner");
      star.textContent = "★";
      wrap.appendChild(star);
      any = true;
    }
    if (m.is_approver) {
      var appr = document.createElement("span");
      appr.className = "memberapprovericon";
      appr.title = t("role.approver");
      appr.innerHTML = APPROVER_ICON_SVG;
      wrap.appendChild(appr);
      any = true;
    }
    var agentID = agentExclusiveID(m.status);
    if (agentID) {
      var agent = state.agents.filter(function (a) { return a.agent_id === agentID; })[0];
      var tag = document.createElement("span");
      tag.className = "memberagenttag";
      tag.title = t("agent.assigned_tooltip");
      tag.textContent = "→ " + (agent ? (agent.name || agent.agent_id) : agentID);
      wrap.appendChild(tag);
      any = true;
    }
    return any ? wrap : null;
  }

  // contactRow: one flat name+number row (.memberrow) — shared by the
  // Grupos tab's member list (above) and the Contactos tab (below), so the
  // two look/behave identically instead of each hand-rolling the same DOM.
  // The number column is skipped when name already IS the number (a p2p
  // contact with neither contact_name nor a WhatsApp name falls back to
  // jidNumber for its label — showing the same digits twice would be a
  // pointless duplicate, not a second piece of information).
  //
  // m (T66c, optional): the group_member's own chatOut row, passed ONLY by
  // the two group-member call sites (Grupos tab's inline list, and the
  // group popup) — never by the Contactos tab, which is out of this
  // contract's scope. When present, appends groupMemberDecisionMark(m).
  function contactRow(name, jid, resolvedNumber, m) {
    var row = document.createElement("div");
    row.className = "memberrow";
    var nameSpan = document.createElement("span");
    nameSpan.className = "membername";
    nameSpan.textContent = name;
    nameSpan.title = name; // title (T188): el nombre se recorta con "…" — el completo, al pasar el cursor.
    row.appendChild(nameSpan);
    if (m) {
      var mark = groupMemberDecisionMark(m);
      if (mark) row.appendChild(mark);
    }
    // resolvedNumber (ct-2026-07-21-1809): a @lid row's real phone number,
    // resolved backend-side (read.go's ResolvedNumber) — shown instead of
    // the raw @lid digits, which read like a nonsense phone number
    // (WhatsApp's internal ID, not the contact's actual number).
    // jidNumber(...) both sides (D1 smoke fix, ct-2026-07-22-2100):
    // ResolvedNumber is a FULL jid (with @s.whatsapp.net), not a bare
    // number — using it raw showed "55500000046@s.whatsapp.net" instead of
    // just the digits. jidNumber("") is "" (falsy), so the fallback to
    // jidNumber(jid) still holds when nothing was resolved.
    var num = jidNumber(resolvedNumber) || jidNumber(jid);
    if (name !== num) {
      var numSpan = document.createElement("span");
      numSpan.className = "membernumber";
      numSpan.textContent = maskNumber(num);
      row.appendChild(numSpan);
    }
    return row;
  }

  // ── tab Contactos (D1 smoke fix, ct-2026-07-22-2100 — simplificado desde
  // el split S1h-fix/Tramo B) ──────────────────────────────────────────────
  // SOLO state.chats (p2p) — el boss quiere miembros de grupo ÚNICAMENTE en
  // la pestaña Grupos (renderGroups, ya los muestra colapsables con nombre
  // resuelto). Antes esta pestaña TAMBIÉN mezclaba state.groupMembers acá
  // (una fila "GRUPO · N miembros" incrustada en "Números", y los que
  // resultaban ser contacto duplicados en "Contactos") — leía como "hay un
  // grupo metido en Contactos", exactamente lo que el boss reportó en el
  // smoke. Dos secciones planas por is_contact (chatOut, backend):
  //   - "Contactos": is_contact=true (agenda real). Alfabético por nombre.
  //   - "Números": el resto (chats sin contact_name).
  function contactLabel(c) { return c.contact_name || c.name || jidNumber(c.jid); }
  // contactDisplayLabel: como contactLabel pero para PINTAR (T93) — cuando
  // no hay ni contact_name ni name, lo único que queda es el número crudo,
  // y ESE sigue tapado (el mandato es "solo números", no "solo si hay
  // nombre al lado"). contactLabel sigue intacto: lo usan buscador y orden
  // alfabético, que necesitan el dato real.
  function contactDisplayLabel(c) { return maskedName(c.contact_name) || maskedName(c.name) || maskNumber(jidNumber(c.jid)); }
  function sectionHeader(text) {
    var div = document.createElement("div");
    div.className = "list-sub";
    div.textContent = text;
    return div;
  }

  function renderContacts() {
    var container = document.getElementById("contactlist");
    container.innerHTML = "";
    var needle = state.filterText ? foldAccents(state.filterText.toLowerCase()) : "";
    var frag = document.createDocumentFragment();

    var chats = state.chats.filter(function (c) { return !needle || matchesNeedle(contactLabel(c), c.jid, needle); });

    var contactChats = chats.filter(function (c) { return c.is_contact; })
      .sort(function (a, b) { return contactLabel(a).localeCompare(contactLabel(b)); });
    frag.appendChild(sectionHeader(t("contacts.section_contacts", { n: contactChats.length })));
    contactChats.forEach(function (c) { frag.appendChild(contactRow(contactDisplayLabel(c), c.jid, c.resolved_number)); });

    var numberChats = chats.filter(function (c) { return !c.is_contact; })
      .sort(function (a, b) { return contactLabel(a).localeCompare(contactLabel(b)); });
    frag.appendChild(sectionHeader(t("contacts.section_numbers", { n: numberChats.length })));
    numberChats.forEach(function (c) { frag.appendChild(contactRow(contactDisplayLabel(c), c.jid, c.resolved_number)); });

    container.appendChild(frag);
  }

  // ── tab Agentes (M1, ct-2026-07-22-1301; paso 2 editar/crear/borrar +
  // buscador, ct-2026-07-29) — el principal (sintetizado backend-side desde
  // el cableado cAPI + SettingPrincipalName) + cada secundario (tabla
  // agents, alta por MCP o por acá). Un solo patrón de tarjeta para
  // cualquier agente — mismo punto que unificó agent-update del lado del
  // backend (paso 1): la pantalla no necesita saber que el principal vive
  // en KV y los secundarios en otra tabla, solo llama agent-update con el
  // agent_id que sea.
  //
  // agentInput: helper compartido por la tarjeta de cada agente Y el form
  // de alta — un <div class="field"> con <label>+<input>, mismo par que
  // editmodal/config ya usan (.field/.inp), no un widget nuevo.
  function agentInput(container, label, value, opts) {
    opts = opts || {};
    var field = document.createElement("div");
    field.className = "field";
    var lbl = document.createElement("label");
    lbl.textContent = label;
    field.appendChild(lbl);
    var input = document.createElement("input");
    input.className = "inp";
    input.type = opts.password ? "password" : "text";
    input.autocomplete = opts.password ? "new-password" : "off";
    input.value = value || "";
    if (opts.placeholder) input.placeholder = opts.placeholder;
    if (opts.readonly) { input.readOnly = true; input.tabIndex = -1; }
    field.appendChild(input);
    if (opts.hint) {
      var hint = document.createElement("p");
      hint.className = "dimnote";
      hint.textContent = opts.hint;
      field.appendChild(hint);
    }
    container.appendChild(field);
    return input;
  }

  // isCreateAgentFormOpen: "abierto" es exactamente lo que el propio
  // toggleBtn de renderCreateAgentForm alterna — la clase "hidden" en
  // .agentcard-body. formEl es el wrap (#createagentform) entero, no el
  // body — se busca el body adentro para no depender de la estructura
  // exacta desde afuera de esta función.
  function isCreateAgentFormOpen(formEl) {
    var body = formEl.querySelector(".agentcard-body");
    return !!body && !body.classList.contains("hidden");
  }

  // editingAgentIDs (T141, ct-2026-09-07-1155): agent_id -> true mientras
  // esa tarjeta tiene texto pegado/tocado sin guardar todavía. Mismo
  // espíritu que isCreateAgentFormOpen, pero por-tarjeta en vez de un
  // único form: se limpia al guardar con éxito (renderAgentCard), nunca
  // por el paso del tiempo.
  var editingAgentIDs = {};
  function agentCardElementID(agentID) {
    return "agentcard-" + agentID;
  }

  // renderAgents refresca la lista de tarjetas cada vez que se llama
  // (loadAgents, cada 15s — "cero F5" sigue intacto para eso), pero el
  // form de "+ Nuevo agente" NO se toca si ya está abierto (T140,
  // ct-2026-09-05-1607) y una tarjeta con edición en curso NO se
  // reconstruye (T141, mismo bug reaparecido acá: el refresco automático
  // borraba lo que el dueño acababa de pegar/tocar en su propia tarjeta).
  // Reutilizar el nodo existente (moverlo al fragment, no recrearlo)
  // conserva el texto tal cual estaba — sin pausar el refresco de las
  // demás tarjetas. La sección de tarjetas vive en su PROPIO
  // sub-contenedor (#agentcardlist), separado del form a propósito.
  function renderAgents() {
    var container = document.getElementById("agentlist");
    var form = document.getElementById("createagentform");
    if (!form || !isCreateAgentFormOpen(form)) {
      if (form) form.remove();
      form = renderCreateAgentForm();
      container.insertBefore(form, container.firstChild);
    }
    var list = document.getElementById("agentcardlist");
    if (!list) {
      list = document.createElement("div");
      list.id = "agentcardlist";
      container.appendChild(list);
    }
    if (!state.agents.length) {
      list.innerHTML = "";
      var empty = document.createElement("p");
      empty.className = "sub";
      empty.textContent = t("agent.none_yet");
      list.appendChild(empty);
      return;
    }
    var frag = document.createDocumentFragment();
    state.agents.forEach(function (a) {
      var existing = document.getElementById(agentCardElementID(a.agent_id));
      frag.appendChild(editingAgentIDs[a.agent_id] && existing ? existing : renderAgentCard(a));
    });
    list.innerHTML = "";
    list.appendChild(frag);
  }

  // agentTypeLabel (T153 2b-iii): traduce el label de un AGENT_DEFAULT_TYPES
  // por su .key — NO un campo labelKey en el array, a propósito:
  // TestAllKeysMatchBetweenFrontendAndCatalog lee app.js con un grep, no
  // ejecuta el JS, así que una clave que solo existe como VALOR de un
  // campo de objeto le resulta invisible (mismo motivo que
  // originLevelShort, más abajo, no es un mapa {key: "clave"}). "boss"
  // devuelve el fallback sin traducir — misma palabra en los dos idiomas.
  function agentTypeLabel(key, fallback) {
    if (key === "new_number") return t("rules.new_messages_label");
    if (key === "contact") return t("rules.contacts_label");
    if (key === "group") return t("tab.groups");
    return fallback;
  }

  // buildAgentTypesControl (T72, ct-2026-08-27-1625) — la vista inversa de
  // AGENT_DEFAULT_TYPES: parada en el agente, no en el tipo. Un checkbox por
  // tipo, tildado si state.originAgentDefaults[tipo] es ESTE agente. Tildar
  // asigna el tipo a este agente (mismo endpoint/key que la pestaña Reglas);
  // destildar lo limpia — cero dato nuevo, la MISMA fuente de verdad,
  // solo otra forma de escribirla (decisión de Citrino: no tabla/clave
  // paralela). Boss verbatim: "que en la parte de agentes se le asignen
  // los mensajes nuevos, de contacto, de grupos o al boss".
  function buildAgentTypesControl(a) {
    var wrap = document.createElement("div");
    wrap.className = "agenttypes";
    var label = document.createElement("p");
    label.className = "dimnote";
    label.textContent = t("agent.attends_label");
    wrap.appendChild(label);
    // T153 2b-iii: el parámetro de este forEach se llamaba "t" — choca con
    // t(clave) del mecanismo de i18n (dentro de este callback, t(...)
    // hubiera intentado invocar el objeto {key,label,...} como función).
    // Renombrado a "entry" acá y en los otros 2 lugares que iteran
    // AGENT_DEFAULT_TYPES (loadOriginDefaults, más abajo) — nunca más un
    // nombre de una letra para algo que no es la función de traducción.
    AGENT_DEFAULT_TYPES.forEach(function (entry) {
      var row = document.createElement("label");
      row.className = "agenttype-row";
      var cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = state.originAgentDefaults[entry.key] === a.agent_id;
      cb.onchange = function () {
        var agentID = cb.checked ? a.agent_id : "";
        post("/api/admin/" + entry.endpoint, { agent_id: agentID }).then(function () {
          loadOriginDefaults();
        }).catch(function (e) {
          alert(t("error.prefix") + e.message);
          cb.checked = !cb.checked;
        });
      };
      row.appendChild(cb);
      row.appendChild(document.createTextNode(" " + agentTypeLabel(entry.key, entry.label)));
      wrap.appendChild(row);
    });
    return wrap;
  }

  // renderAgentCard: nombre/endpoint/terminal/pin editables contra
  // agent-update — sirve igual para el principal y los secundarios. El
  // endpoint del principal (agentes paso 3, ct-2026-07-29) dejó de ser
  // readonly: el boss corrigió que "siempre local, esta máquina" rompía el
  // caso Raspberry Pi (antena en otra máquina de la misma LAN) — el gate
  // real (loopback + red privada, nunca pública) vive en el backend
  // (store.isAllowedPrincipalEndpoint), no en este input.
  function renderAgentCard(a) {
    var isPrincipal = a.role === "principal";
    var card = document.createElement("div");
    card.className = "agentcard";
    // id estable (T141): renderAgents() lo busca por acá para decidir si
    // esta tarjeta puntual está en edición y, de estarlo, reutilizarla tal
    // cual en vez de reconstruirla — mismo mecanismo que #createagentform
    // (T140), uno por agente en vez de uno solo.
    card.id = agentCardElementID(a.agent_id);

    var head = document.createElement("div");
    head.className = "agentcard-head";
    var badge = document.createElement("span");
    badge.className = "agentbadge " + a.role;
    badge.textContent = isPrincipal ? t("agent.role_principal") : t("agent.role_secondary");
    var headName = document.createElement("span");
    headName.className = "agentname";
    headName.textContent = a.name || t("data.unnamed");
    head.appendChild(badge);
    head.appendChild(headName);
    card.appendChild(head);

    var idLine = document.createElement("p");
    idLine.className = "dimnote";
    idLine.textContent = t("agent.id_prefix") + a.agent_id;
    card.appendChild(idLine);

    card.appendChild(buildAgentTypesControl(a));

    var body = document.createElement("div");
    body.className = "agentcard-body";

    // Zona de pegado (T141, ct-2026-09-07-1155, boss: "en vez de borrar y
    // agregar a un agente, tambien es necesario actualizarlo con su link
    // de antena de una linea") — mismo parseo y mismo tratamiento que la
    // del form de alta (T73/T75), con una sola diferencia: el nombre NO se
    // pisa si el agente ya tiene uno (el boss ya nombró sus agentes, pegar
    // una antena nueva no debería rebautizarlos).
    var pasteZoneInput = agentInput(body, t("label.paste_credentials"), "", {
      placeholder: t("placeholder.antenna_line"),
      hint: t("hint.antenna_paste_card"),
    });
    var nameInput = agentInput(body, t("modal.name_heading"), a.name, { placeholder: t("placeholder.agent_name") });
    var endpointInput = agentInput(body, t("agent.label_endpoint"), a.endpoint, isPrincipal
      ? { placeholder: "http://192.168.1.10:8787", hint: t("hint.endpoint_lan_only") }
      : { placeholder: "http://192.168.1.10:8787" });
    var terminalInput = agentInput(body, t("agent.label_terminal_id"), a.antenna_terminal_id, { placeholder: t("placeholder.terminal_id_example") });
    var pinInput = agentInput(body, t("agent.label_pin"), "", { password: true, placeholder: a.pinpass_set ? t("placeholder.pin_saved") : "••••••••" });
    card.appendChild(body);

    var saveRow = document.createElement("div");
    saveRow.className = "agentcard-actions";
    var saveBtn = document.createElement("button");
    saveBtn.className = "btn pri sm";
    saveBtn.textContent = t("action.save");
    var saveResult = document.createElement("span");
    saveResult.className = "agentpingresult";

    // markDirty (T141): cualquier toque en la tarjeta la saca de la rotación
    // del refresco de 15s (renderAgents) hasta el próximo Guardar exitoso —
    // "input" delegado cubre tipeo a mano y pegado sin marca reconocida; el
    // pegado que SÍ parsea llama markDirty directo (más abajo, applyPaste)
    // porque ahí se previene el evento nativo y "input" nunca llega a
    // disparar solo.
    function markDirty() { editingAgentIDs[a.agent_id] = true; }
    body.addEventListener("input", markDirty);

    // applyPaste — mismo criterio que applyParsedCredentials del form de
    // alta (nunca inventa un campo que el texto no trae, nunca dispara
    // Guardar solo), con la única diferencia de este contrato: el nombre
    // no se pisa si nameInput ya tiene algo.
    function applyPaste(parsed) {
      markDirty();
      if (parsed.endpoint) endpointInput.value = parsed.endpoint;
      if (parsed.terminalID) terminalInput.value = parsed.terminalID;
      if (parsed.pin) pinInput.value = parsed.pin;
      if (parsed.name && !nameInput.value) nameInput.value = parsed.name;
      saveResult.textContent = t("agent.autodetected");
      setTimeout(function () { saveResult.textContent = ""; }, 2500);
    }
    body.addEventListener("paste", function (ev) {
      var clip = ev.clipboardData || window.clipboardData;
      var parsed = parseAgentCredentialsPaste(clip.getData("text"));
      if (!parsed) return;
      ev.preventDefault();
      applyPaste(parsed);
    });
    pasteZoneInput.addEventListener("input", function () {
      var parsed = parseAgentCredentialsPaste(pasteZoneInput.value);
      if (!parsed) return;
      applyPaste(parsed);
      pasteZoneInput.value = ""; // scratch — nunca viaja en agent-update
    });
    pasteZoneInput.addEventListener("change", function () {
      var parsed = parseAgentCredentialsPaste(pasteZoneInput.value);
      if (!parsed) return;
      applyPaste(parsed);
      pasteZoneInput.value = "";
    });

    saveBtn.onclick = function () {
      var reqBody = {
        agent_id: a.agent_id, name: nameInput.value,
        endpoint: endpointInput.value, antenna_terminal_id: terminalInput.value,
      };
      // pinpass: SOLO si el boss tecleó algo nuevo — el campo siempre
      // arranca vacío (nunca se muestra el secreto guardado), así que
      // "vacío" significa "no tocar", no "borrar" (mismo contrato que
      // set_agent_capi/agent-update ya documentan: "omit to keep current").
      if (pinInput.value) reqBody.pinpass = pinInput.value;
      saveResult.textContent = t("action.saving");
      post("/api/admin/agent-update", reqBody).then(function () {
        saveResult.textContent = t("action.saved");
        delete editingAgentIDs[a.agent_id]; // T141: recién acá vuelve a la rotación del refresco
        loadAgents();
      }).catch(function (e) {
        saveResult.textContent = t("error.prefix") + e.message;
      });
    };
    saveRow.appendChild(saveBtn);
    saveRow.appendChild(saveResult);
    card.appendChild(saveRow);

    // Ping por-agente (M2, ct-2026-07-22-1301) — sin cambios.
    var actions = document.createElement("div");
    actions.className = "agentcard-actions";
    var pingBtn = document.createElement("button");
    pingBtn.className = "smbtn";
    pingBtn.textContent = t("agent.ping_button");
    var pingResult = document.createElement("span");
    pingResult.className = "agentpingresult";
    pingBtn.onclick = function () {
      pingResult.textContent = t("agent.sending_ping");
      post("/api/admin/capi-ping", { agent_id: a.agent_id }).then(function (d) {
        // d.result: texto armado por el backend (Go) — servidor, no
        // catálogo. Anotado para la etapa 3 (ver nota del contrato), sin
        // tocarlo acá.
        pingResult.textContent = (d.ok ? "✓ " : "✗ ") + d.result;
      }).catch(function (e) {
        pingResult.textContent = t("error.prefix") + e.message;
      });
    };
    actions.appendChild(pingBtn);
    actions.appendChild(pingResult);
    card.appendChild(actions);

    // Cambiar de rol (T76, ct-2026-08-27-1752, boss verbatim: "poder
    // cambiar si tengo un secundario a al principal de manera rápida") —
    // click directo, sin modal ni confirmación: el dueño lo pidió rápido y
    // explícitamente rechazó cualquier candado nuevo ("me da lo mismo que
    // piensen que van a haber problemas... yo lo quiero, como yo lo
    // digo"). Solo en la card de un SECUNDARIO — la del principal no
    // muestra el botón porque "promoverse a sí mismo" no es una acción,
    // no un candado.
    if (!isPrincipal) {
      var promoteRow = document.createElement("div");
      promoteRow.className = "agentcard-actions";
      var promoteBtn = document.createElement("button");
      promoteBtn.className = "btn gho sm";
      promoteBtn.textContent = t("action.promote_to_principal");
      var promoteResult = document.createElement("span");
      promoteResult.className = "agentpingresult";
      promoteBtn.onclick = function () {
        promoteResult.textContent = t("agent.promoting");
        post("/api/admin/agent-promote", { agent_id: a.agent_id }).then(function () {
          promoteResult.textContent = "";
          loadAgents();
        }).catch(function (e) {
          promoteResult.textContent = t("error.prefix") + e.message;
        });
      };
      promoteRow.appendChild(promoteBtn);
      promoteRow.appendChild(promoteResult);
      card.appendChild(promoteRow);
    }

    // Borrar (M3, ct-2026-07-22-1301 + paso 2; el principal incluido desde
    // T76, ct-2026-08-27-1752 — "no puedo borrar a la gente principal...
    // a mí no me interesa [los problemas], yo lo quiero, como yo lo
    // digo"): TODOS los agentes pueden borrarse ahora, mismo modal de
    // confirmación que ya existía (no uno nuevo). "Borrar" al principal
    // no deja la fila vacía porque nunca fue una fila — lo vacía a KV
    // (store.ClearPrincipalAgent), el mismo estado de una instalación
    // recién hecha.
    //
    // T86 (ct-2026-08-28-0451): envuelto en .agentcard-actions, como las
    // otras dos filas de botones (Guardar/Ping) — antes iba suelto
    // directo en `card`, sin el margin-top:10px que .agentcard-actions ya
    // da, así que el espacio contra "Ping" quedaba distinto del espacio
    // entre las otras filas ("espaciados distintos entre sí", boss).
    var deleteRow = document.createElement("div");
    deleteRow.className = "agentcard-actions";
    var deleteBtn = document.createElement("button");
    deleteBtn.className = "btn danger sm";
    deleteBtn.textContent = t("action.delete_agent");
    deleteBtn.onclick = function () { openDeleteAgentModal(a); };
    deleteRow.appendChild(deleteBtn);
    card.appendChild(deleteRow);

    // Números asignados (T86, ct-2026-08-28-0451): el `if (!isPrincipal)`
    // que vivía acá describía el mundo anterior a T70 (ct-2026-08-27-1404)
    // — quedó documentado como desactualizado en T76 sin que nadie lo
    // levantara a contrato, y el dueño lo encontró usándolo. El principal
    // lista, quita y recibe asignaciones igual que cualquier secundario;
    // `store.ChatsForAgent`/`GET /api/agents/chats` y
    // `POST /api/admin/agent-assign` ya eran agnósticos al agente desde
    // T70 — esto era pura condición sobrante del lado del tablero.
    var subhead = document.createElement("div");
    subhead.className = "agentcard-subhead";
    subhead.textContent = t("agent.assigned_numbers_heading");
    card.appendChild(subhead);

    var assignedList = document.createElement("div");
    assignedList.className = "agentassigned";
    card.appendChild(assignedList);
    renderAssignedNumbers(assignedList, a.agent_id);

    card.appendChild(renderAssignSearch(a.agent_id, assignedList));

    return card;
  }

  // renderAssignSearch (Agentes paso 2, ct-2026-07-29 — boss: "que tenga un
  // buscador de numeros y contactos... eso es pedirle al usuario que hable
  // como la base de datos") — reusa matchesNeedle/foldAccents, la MISMA
  // lógica del buscador de Conversaciones (#search), no una segunda
  // implementación. Busca sobre state.chats (p2p) — contactos Y
  // conversaciones son la misma fuente, ya la comparten los tabs Chats/
  // Contactos.
  function renderAssignSearch(agentID, assignedList) {
    var wrap = document.createElement("div");
    wrap.className = "agentassign-form";
    var input = document.createElement("input");
    input.className = "inp";
    input.type = "search";
    input.autocomplete = "off";
    input.placeholder = t("placeholder.assign_search");
    var results = document.createElement("div");
    results.className = "agentsearch-results hidden";

    function renderResults(query) {
      results.innerHTML = "";
      if (!query) { results.classList.add("hidden"); return; }
      var needle = foldAccents(query.toLowerCase());
      var matches = state.chats.filter(function (c) {
        return matchesNeedle(contactLabel(c), c.jid, needle);
      }).slice(0, 8);
      if (!matches.length) { results.classList.add("hidden"); return; }
      matches.forEach(function (c) {
        var row = document.createElement("div");
        row.className = "agentsearch-result";
        var nameSpan = document.createElement("span");
        nameSpan.className = "membername";
        nameSpan.textContent = contactDisplayLabel(c);
        nameSpan.title = nameSpan.textContent;
        var numSpan = document.createElement("span");
        numSpan.className = "membernumber";
        numSpan.textContent = maskNumber(jidNumber(c.resolved_number) || jidNumber(c.jid));
        row.appendChild(nameSpan);
        row.appendChild(numSpan);
        // mousedown, no click: dispara ANTES del blur del input — con
        // click, el blur esconde la lista primero y el click nunca llega.
        row.addEventListener("mousedown", function (ev) {
          ev.preventDefault();
          post("/api/admin/agent-assign", { chat_id: c.jid, agent_id: agentID }).then(function () {
            input.value = "";
            results.classList.add("hidden");
            renderAssignedNumbers(assignedList, agentID);
          }).catch(function (e) { alert(t("error.prefix") + e.message); });
        });
        results.appendChild(row);
      });
      results.classList.remove("hidden");
    }
    input.addEventListener("input", function () { renderResults(input.value.trim()); });
    input.addEventListener("focus", function () { renderResults(input.value.trim()); });
    input.addEventListener("blur", function () { results.classList.add("hidden"); });

    wrap.appendChild(input);
    wrap.appendChild(results);
    return wrap;
  }

  // renderCreateAgentForm (Agentes paso 2, ct-2026-07-29) — alta de un
  // secundario contra agent-create. Colapsado por defecto (botón "+ Nuevo
  // agente") para no ensuciar la vista con un form vacío siempre a la
  // vista.
  function renderCreateAgentForm() {
    var wrap = document.createElement("div");
    wrap.className = "agentcard";
    // T140 (ct-2026-09-05-1607): id estable — renderAgents() lo busca por
    // acá para decidir si está abierto y, de estarlo, dejarlo en paz en
    // cada refresco automático.
    wrap.id = "createagentform";

    var toggleBtn = document.createElement("button");
    toggleBtn.className = "btn gho sm";
    toggleBtn.type = "button";
    toggleBtn.textContent = t("action.new_agent");
    wrap.appendChild(toggleBtn);

    var form = document.createElement("div");
    form.className = "agentcard-body hidden";

    var result = document.createElement("span");
    result.className = "agentpingresult";

    // Zona propia de pegado (T75, ct-2026-08-27 — el dueño probó T73 y
    // pegó en el campo Endpoint por error: sin un lugar visible que diga
    // "pegá acá", tuvo que adivinar entre los 5 campos de abajo. Primer
    // campo del form a propósito — lo primero que se ve al abrir "+ Nuevo
    // agente"). Mismo parseo (parseAgentCredentialsPaste, agentpaste.js,
    // sin tocar — 8 casos verdes), mismo llenado que el onpaste de más
    // abajo (aplicado vía applyParsedCredentials): esta zona solo cambia
    // DE DÓNDE llega el texto, nunca cómo se interpreta.
    var pasteZoneInput = agentInput(form, t("label.paste_credentials"), "", {
      placeholder: t("placeholder.antenna_line"),
      hint: t("hint.antenna_paste_create"),
    });

    // T140 (ct-2026-09-05-1607, el dueño: "que solo se requiera el campo
    // del pegado de antena, nada de ediciones a mano") — los 5 campos
    // individuales dejan de ser el camino normal: quedan detrás de este
    // toggle, colapsados por defecto sea que el pegado los haya llenado
    // todos o no. Siguen ahí para corrección puntual, nunca a la vista
    // por defecto — "un campo en vez de seis" (el mismo pedido resuelve
    // de paso el desorden de la zona).
    var manualToggle = document.createElement("button");
    manualToggle.className = "btn gho sm manualfieldstoggle";
    manualToggle.type = "button";
    manualToggle.textContent = t("label.edit_fields_manually");
    form.appendChild(manualToggle);

    var manualFields = document.createElement("div");
    manualFields.className = "hidden";
    manualToggle.onclick = function () { manualFields.classList.toggle("hidden"); };

    var idInput = agentInput(manualFields, t("label.agent_id"), "", { placeholder: t("placeholder.agent_id_example") });
    var nameInput = agentInput(manualFields, t("modal.name_heading"), "", { placeholder: t("placeholder.agent_name") });
    var endpointInput = agentInput(manualFields, t("agent.label_endpoint"), "", { placeholder: "http://192.168.1.10:8787" });
    var terminalInput = agentInput(manualFields, t("agent.label_terminal_id"), "", { placeholder: t("placeholder.terminal_id_example") });
    var pinInput = agentInput(manualFields, t("agent.label_pin"), "", { password: true, placeholder: "••••••••" });
    form.appendChild(manualFields);

    var actions = document.createElement("div");
    actions.className = "agentcard-actions";
    var createBtn = document.createElement("button");
    createBtn.className = "btn pri sm";
    createBtn.textContent = t("action.create");

    // Pegado de credenciales (T73, ct-2026-08-27-1713) — onpaste sobre el
    // form entero (el evento burbujea desde cualquiera de los inputs), NO
    // un campo "pegá acá" nuevo: el navegador ya sabe interceptar un paste
    // sobre un campo que existe. El dueño ya lo usó así — se queda, sin
    // tocar (T75: "el onpaste sobre los 5 campos SE QUEDA"). null
    // significa "no es una credencial", pegado normal, sin preventDefault.
    // Match parcial: solo pisa los campos que el texto realmente trae —
    // "no borres lo que el usuario ya había escrito" (contrato). No
    // dispara Crear — el dueño lo dijo explícito: "el usuario pone
    // guardar" después de revisar.
    function applyParsedCredentials(parsed) {
      if (parsed.endpoint) endpointInput.value = parsed.endpoint;
      if (parsed.terminalID) terminalInput.value = parsed.terminalID;
      if (parsed.pin) pinInput.value = parsed.pin;
      if (parsed.name) nameInput.value = parsed.name;
      if (parsed.agentID) idInput.value = parsed.agentID;
      // Aviso discreto de que algo pasó (contrato: "sin esto parece un
      // glitch") — mismo span/timing que el resto del form ya usa para
      // "Creando…"/errores, se va solo.
      result.textContent = t("agent.autodetected");
      setTimeout(function () { result.textContent = ""; }, 2500);
    }

    form.addEventListener("paste", function (ev) {
      var clip = ev.clipboardData || window.clipboardData;
      var parsed = parseAgentCredentialsPaste(clip.getData("text"));
      if (!parsed) return;
      ev.preventDefault();
      applyParsedCredentials(parsed);
    });

    // La zona propia escucha "input"/"change", no "paste" — a propósito
    // (Citrino, T75): un Ctrl+V real dispara los tres, pero un paste con
    // el mouse (menú contextual) o texto tipeado a mano solo dispara
    // input/change, y hasta acá quedaban sin detectar. Efecto colateral
    // correcto, no buscado pero bienvenido: un valor puesto por código
    // (`element.value` + su propio evento input/change, como hace
    // cualquier test de automatización de browser) también dispara el
    // detect — cierra el hueco de verificación que T73 dejó abierto (Ctrl+V
    // sintético vía CDP no dispara un evento paste real, límite conocido
    // del navegador, no del código).
    function tryPasteZone() {
      var parsed = parseAgentCredentialsPaste(pasteZoneInput.value);
      if (!parsed) return;
      applyParsedCredentials(parsed);
      pasteZoneInput.value = ""; // scratch — nunca viaja en agent-create
    }
    pasteZoneInput.addEventListener("input", tryPasteZone);
    pasteZoneInput.addEventListener("change", tryPasteZone);

    createBtn.onclick = function () {
      if (!idInput.value.trim() || !endpointInput.value.trim() || !terminalInput.value.trim() || !pinInput.value.trim()) {
        // T140: los campos que faltan viven detrás de "Editar campos
        // manualmente" — el mensaje de error no sirve de nada si lo que
        // falta queda escondido, así que esto los muestra de una.
        manualFields.classList.remove("hidden");
        result.textContent = t("validation.agent_required_fields");
        return;
      }
      result.textContent = t("agent.creating");
      post("/api/admin/agent-create", {
        agent_id: idInput.value.trim(), name: nameInput.value,
        endpoint: endpointInput.value, antenna_terminal_id: terminalInput.value, pinpass: pinInput.value,
      }).then(function (data) {
        // T155: el alta nunca se niega — el que llega gana el id y "updated"
        // dice que tomó el lugar de uno que ya existía, no que creó uno
        // nuevo (distinción que el boss pidió visible en el tablero).
        result.textContent = data && data.status === "updated"
          ? t("agent.create_replaced_existing")
          : t("agent.created");
        loadAgents();
      }).catch(function (e) { result.textContent = t("error.prefix") + e.message; });
    };
    actions.appendChild(createBtn);
    actions.appendChild(result);
    form.appendChild(actions);

    toggleBtn.onclick = function () { form.classList.toggle("hidden"); };

    wrap.appendChild(form);
    return wrap;
  }

  // openDeleteAgentModal/closeDeleteAgentModal (Agentes paso 2, ct-2026-07-29
  // — boss: "confirmación explícita, y que diga cuántos chats se van a
  // desasignar... que el boss sepa qué está soltando antes de soltarlo").
  // Modal propio (#agentdeletemodal), nada de window.confirm. Cuenta los
  // chats asignados ANTES de borrar (mismo GET /api/agents/chats?agent_id=
  // que ya alimenta "Números asignados") — "antes de soltarlo" es antes,
  // no en el resultado.
  var pendingDeleteAgent = null;
  function openDeleteAgentModal(a) {
    pendingDeleteAgent = a;
    var text = document.getElementById("agentdelete_text");
    var result = document.getElementById("agentdelete_result");
    result.textContent = "";
    // T82 (ct-2026-08-27-2256, boss: "la ventana no cierra... cancelar se
    // convierte en cerrar") — cada apertura arranca de cero: si el modal
    // quedó en el estado "post-éxito" de un borrado anterior (botón
    // destructivo oculto, "Cancelar" convertido en "Cerrar"), reponerlo
    // ANTES de mostrar el nuevo agente a confirmar. Sin esto, borrar un
    // agente dejaría el modal permanentemente sin botón destructivo para
    // el próximo.
    document.getElementById("agentdelete_confirm").classList.remove("hidden");
    document.getElementById("agentdelete_cancel2").textContent = t("action.cancel");
    text.textContent = t("agent.calculating_chats", { name: a.name || a.agent_id });
    document.getElementById("agentdeletemodal").classList.remove("hidden");
    api("/api/agents/chats?agent_id=" + encodeURIComponent(a.agent_id)).then(function (chats) {
      if (pendingDeleteAgent !== a) return; // el modal cambió de agente mientras esto viajaba
      var question = t("agent.confirm_delete_question", { name: a.name || a.agent_id });
      var impact = chats.length
        ? t("agent.delete_impact_chats", { n: chats.length, s: chats.length === 1 ? "" : "s" })
        : t("agent.delete_impact_none");
      text.textContent = question + " " + impact;
    }).catch(function () {
      text.textContent = t("agent.confirm_delete_question", { name: a.name || a.agent_id });
    });
  }
  function closeDeleteAgentModal() {
    pendingDeleteAgent = null;
    document.getElementById("agentdeletemodal").classList.add("hidden");
  }
  document.getElementById("agentdelete_cancel").addEventListener("click", closeDeleteAgentModal);
  document.getElementById("agentdelete_cancel2").addEventListener("click", closeDeleteAgentModal);
  document.getElementById("agentdelete_confirm").addEventListener("click", function () {
    if (!pendingDeleteAgent) return;
    var a = pendingDeleteAgent;
    var result = document.getElementById("agentdelete_result");
    result.textContent = t("agent.deleting");
    post("/api/admin/agent-delete", { agent_id: a.agent_id }).then(function (d) {
      var n = d.chats_unassigned || 0;
      result.textContent = t("agent.delete_success", { n: n, s: n === 1 ? "" : "s" });
      // T82: ya no queda nada que borrar ni que cancelar — el botón
      // destructivo se va (uno deshabilitado seguiría invitando a
      // apretarlo) y "Cancelar" pasa a "Cerrar", así el resultado queda
      // visible hasta que el dueño decida salir. Si la acción falla (más
      // abajo), el modal se queda como está hoy: reintentar y cancelar
      // siguen teniendo sentido ahí.
      document.getElementById("agentdelete_confirm").classList.add("hidden");
      document.getElementById("agentdelete_cancel2").textContent = t("action.close");
      loadAgents();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });

  // openApproverModal/closeApproverModal (Aprobador P1, ct-2026-07-31-0610
  // — segunda vuelta tras la decisión del boss): modal propio, nada de
  // window.confirm — mismo criterio que #agentdeletemodal. Habilitar pide
  // confirmación EXPLICANDO qué se habilita (no un "¿seguro?" pelado): que
  // va a ver y poder aprobar/descartar los borradores de TODAS las
  // conversaciones, y que NO va a poder tocar reglas, marcar dueños, ni
  // sacar confirmaciones. Aviso más fuerte si el chat NO es boss (boss
  // verbatim: "o si se habilita a uno no boss") — habilitar a un tercero
  // es dar acceso a mensajes de otras personas, el texto lo dice así.
  // Quitar el permiso también confirma, pero corto — no hay nada nuevo que
  // explicar, solo asegurarse de que fue un click a propósito.
  var pendingApproverChat = null;
  function openApproverModal(c) {
    pendingApproverChat = c;
    var text = document.getElementById("approver_text");
    var confirmBtn = document.getElementById("approver_confirm");
    var result = document.getElementById("approver_result");
    result.textContent = "";
    var name = maskedName(c.name) || maskNumber(jidNumber(c.jid));
    if (c.is_approver) {
      text.textContent = t("approver.revoke_confirm", { name: name });
      confirmBtn.textContent = t("action.revoke_permission");
      confirmBtn.className = "btn danger";
    } else {
      var thirdPartyWarning = c.is_boss ? "" : t("approver.third_party_warning");
      text.textContent = t("approver.grant_confirm_base", { name: name }) + thirdPartyWarning + t("approver.grant_confirm_restrictions");
      confirmBtn.textContent = t("action.enable_approve");
      confirmBtn.className = "btn";
    }
    document.getElementById("approvermodal").classList.remove("hidden");
  }
  function closeApproverModal() {
    pendingApproverChat = null;
    document.getElementById("approvermodal").classList.add("hidden");
  }
  document.getElementById("approver_cancel").addEventListener("click", closeApproverModal);
  document.getElementById("approver_cancel2").addEventListener("click", closeApproverModal);
  document.getElementById("approver_confirm").addEventListener("click", function () {
    if (!pendingApproverChat) return;
    var c = pendingApproverChat;
    var result = document.getElementById("approver_result");
    result.textContent = t("action.saving");
    post("/api/admin/approver", { chat_id: c.jid, is_approver: !c.is_approver }).then(function () {
      closeApproverModal();
      loadChats();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });

  function loadAgents() {
    return api("/api/agents").then(function (agents) {
      state.agents = agents;
      renderAgents();
    });
  }

  // renderAssignedNumbers (M3, ct-2026-07-22-1301) — la lista de "quitar"
  // dentro de la .agentcard de un secundario; GET /api/agents/chats?agent_id=
  // (store.ChatsForAgent). Reconsulta después de cada asignar/quitar en vez
  // de tocar state — la lista es chica (asignación manual, no todos los
  // chats) y así queda siempre fiel al backend sin duplicar su estado acá.
  function renderAssignedNumbers(container, agentID) {
    container.textContent = t("action.loading");
    api("/api/agents/chats?agent_id=" + encodeURIComponent(agentID)).then(function (chats) {
      container.innerHTML = "";
      if (!chats.length) {
        var empty = document.createElement("p");
        empty.className = "sub";
        empty.textContent = t("agent.no_assigned_numbers");
        container.appendChild(empty);
        return;
      }
      chats.forEach(function (c) {
        var row = document.createElement("div");
        row.className = "memberrow";
        var nameSpan = document.createElement("span");
        nameSpan.className = "membername";
        nameSpan.textContent = contactDisplayLabel(c);
        nameSpan.title = nameSpan.textContent;
        var removeBtn = document.createElement("button");
        removeBtn.className = "smbtn";
        removeBtn.textContent = t("action.remove");
        removeBtn.onclick = function () {
          post("/api/admin/agent-assign", { chat_id: c.jid, agent_id: "" }).then(function () {
            renderAssignedNumbers(container, agentID);
          });
        };
        row.appendChild(nameSpan);
        row.appendChild(removeBtn);
        container.appendChild(row);
      });
    });
  }

  // ── Defaults por origen (M5, ct-2026-07-22-1903) — cabecera: 2 pares
  // (modo + reglas), "mensajes nuevos" (números desconocidos) y "contactos
  // de la libreta". Visualmente reusa el mismo indicador+select que la
  // columna Nivel (LEVELS, SIN "boss" — un default nunca es la identidad
  // del dueño) apuntando a los 4 endpoints globales en vez de un chat_id.
  // Las reglas son texto libre en un <textarea> directo (no el modal
  // compartido de chat/grupo — acá no hace falta preview+editar, hay lugar
  // de sobra en la cabecera para 2 cajas). T71 (ct-2026-08-27-1410) suma un
  // TERCER selector a cada uno de los 2 pares: agente por defecto para ese
  // origen — ruteo, misma jerarquía KV que las reglas de arriba
  // (store.EffectiveAgentDefault), sin bloque de texto propio porque no
  // hay nada que escribir, solo elegir.
  var ORIGIN_LEVELS = LEVELS.filter(function (l) { return l.key !== "boss"; });
  // originLevelShort (T153 2b-iii): LEVELS es compartido con
  // renderLevelCell/la leyenda (2b-iv, sin tocar todavía) — esto traduce
  // SOLO el .short que buildOriginLevelControl consume, sin tocar el
  // array compartido ni su .label. "BOSS"/"Auto" quedan afuera a
  // propósito: mismas palabras en los dos idiomas. A propósito NO es un
  // mapa {key: "clave"} — TestAllKeysMatchBetweenFrontendAndCatalog lee
  // app.js con un grep, no ejecuta el JS; una clave que solo existe como
  // VALOR de un objeto le resulta invisible. Las llamadas a t con la
  // clave escrita literal, abajo, son lo que el test puede ver y verificar.
  function originLevelShort(key, fallback) {
    if (key === "confirm") return t("level.short_confirm");
    if (key === "unattended") return t("legend.unattended");
    if (key === "ignored") return t("level.short_ignored");
    return fallback;
  }
  function buildOriginLevelControl(endpoint, currentLevel) {
    var wrap = document.createElement("div");
    wrap.className = "levelcell";
    var sel = document.createElement("select");
    sel.className = "levelselect";
    ORIGIN_LEVELS.forEach(function (l) {
      var opt = document.createElement("option");
      opt.value = l.key;
      opt.textContent = originLevelShort(l.key, l.short);
      if (l.key === currentLevel) opt.selected = true;
      sel.appendChild(opt);
    });
    sel.onchange = function () {
      post("/api/admin/" + endpoint, { level: sel.value }).catch(function (e) { alert(t("error.prefix") + e.message); });
    };
    wrap.appendChild(sel);
    return wrap;
  }
  // buildOriginAgentControl (T71, ct-2026-08-27-1410) — el tercer selector
  // junto al de modo: agente por defecto para ese origen, ruteo puro (no
  // reglas). Mismas opciones que buildAgentAssignControl (state.agents,
  // principal incluido desde T70) pero apuntando al KV de origen en vez de
  // a un chat_id — "Sin asignar" limpia el default, igual criterio que
  // agent-assign.
  function buildOriginAgentControl(endpoint, currentAgentID) {
    var wrap = document.createElement("div");
    wrap.className = "agentassigncell";
    var sel = buildAgentSelect(currentAgentID);
    sel.onchange = function () {
      post("/api/admin/" + endpoint, { agent_id: sel.value }).catch(function (e) { alert(t("error.prefix") + e.message); });
    };
    wrap.appendChild(sel);
    return wrap;
  }
  // AGENT_DEFAULT_TYPES (T71 par origen + T72 grupos/boss, ct-2026-08-27-1625)
  // — los 4 tipos del eje "agente por defecto", una sola lista para las DOS
  // vistas que los editan: acá (por-tipo, la pestaña Reglas) y
  // buildAgentTypesControl más abajo (por-agente, la ficha). Mismo endpoint,
  // misma key KV — cambiar desde un lado se refleja en el otro sin recargar
  // porque ambos leen/escriben este mismo state.originAgentDefaults.
  var AGENT_DEFAULT_TYPES = [
    { key: "new_number", label: "Mensajes nuevos", endpoint: "agent-default-new-number", domId: "origin_new_agent" },
    { key: "contact", label: "Contactos del celular", endpoint: "agent-default-contact", domId: "origin_contact_agent" },
    { key: "group", label: "Grupos", endpoint: "agent-default-group", domId: "origin_group_agent" },
    { key: "boss", label: "Boss", endpoint: "agent-default-boss", domId: "origin_boss_agent" },
  ];
  function loadOriginDefaults() {
    return Promise.all([
      api("/api/admin/config-level-default-new"),
      api("/api/admin/config-level-default-contact"),
      api("/api/admin/rules-default-new-number"),
      api("/api/admin/rules-default-contact"),
      api("/api/admin/type-rules"),
      api("/api/admin/identity"),
    ].concat(AGENT_DEFAULT_TYPES.map(function (entry) { return api("/api/admin/" + entry.endpoint); }))
    ).then(function (results) {
      var newLevelEl = document.getElementById("origin_new_level");
      newLevelEl.innerHTML = "";
      newLevelEl.appendChild(buildOriginLevelControl("config-level-default-new", results[0].level));
      var contactLevelEl = document.getElementById("origin_contact_level");
      contactLevelEl.innerHTML = "";
      contactLevelEl.appendChild(buildOriginLevelControl("config-level-default-contact", results[1].level));
      document.getElementById("origin_new_rules").value = results[2].rules || "";
      document.getElementById("origin_contact_rules").value = results[3].rules || "";
      document.getElementById("type_group_rules").value = results[4].rules || "";
      document.getElementById("identity_rules").value = results[5].identity || "";
      state.originAgentDefaults = {};
      AGENT_DEFAULT_TYPES.forEach(function (entry, i) {
        var agentID = results[6 + i].agent_id || "";
        state.originAgentDefaults[entry.key] = agentID;
        var el = document.getElementById(entry.domId);
        el.innerHTML = "";
        el.appendChild(buildOriginAgentControl(entry.endpoint, agentID));
      });
      // T72: la ficha del agente (renderAgentCard) muestra el mismo dato —
      // sin este refresh, cambiar acá no se vería ahí sin recargar.
      renderAgents();
    });
  }
  // flashSaveResult (T13, ct-2026-08-05-123147): "guardar tiene que
  // confirmar que guardó, sin recargar la página" — mismo texto/criterio
  // que config_email_save ya usa (más abajo), factorizado acá porque la
  // pestaña Reglas lo repite 4 veces (T79, ct-2026-08-27-2034, sacó la
  // quinta — la regla general).
  function flashSaveResult(elId) {
    var el = document.getElementById(elId);
    el.textContent = t("action.saved");
    setTimeout(function () { el.textContent = ""; }, 2500);
  }
  document.getElementById("identity_save").addEventListener("click", function () {
    post("/api/admin/identity", { identity: document.getElementById("identity_rules").value })
      .then(function () { flashSaveResult("identity_save_result"); })
      .catch(function (e) { alert(t("error.prefix") + e.message); });
  });
  document.getElementById("origin_new_rules_save").addEventListener("click", function () {
    post("/api/admin/rules-default-new-number", { rules: document.getElementById("origin_new_rules").value })
      .then(function () { flashSaveResult("origin_new_rules_save_result"); })
      .catch(function (e) { alert(t("error.prefix") + e.message); });
  });
  document.getElementById("origin_contact_rules_save").addEventListener("click", function () {
    post("/api/admin/rules-default-contact", { rules: document.getElementById("origin_contact_rules").value })
      .then(function () { flashSaveResult("origin_contact_rules_save_result"); })
      .catch(function (e) { alert(t("error.prefix") + e.message); });
  });
  document.getElementById("type_group_rules_save").addEventListener("click", function () {
    post("/api/admin/type-rules", { rules: document.getElementById("type_group_rules").value })
      .then(function () { flashSaveResult("type_group_rules_save_result"); })
      .catch(function (e) { alert(t("error.prefix") + e.message); });
  });

  document.getElementById("search").addEventListener("input", function (ev) {
    state.filterText = ev.target.value;
    render();
  });

  // searchPlaceholder (T14, ct-2026-08-05-1232; i18n T153 2b-iv) — el ÚNICO
  // lugar que decide qué pestañas buscan y con qué placeholder;
  // Agentes/Reglas/Drafts no filtran nada hoy y no es lo que este contrato
  // pidió agregar, así que ausentes ("mejor ausente que presente y muerto",
  // el boss) en vez de un buscador presente que no hace nada. El input en sí
  // sigue siendo UNO solo (index.html) — esto solo lo muestra/oculta/
  // etiqueta, nunca lo duplica. Antes era un mapa {chats: "Buscar..."} —
  // convertido a if-chain con t("clave literal") por el mismo motivo que
  // levelLabel/originLevelShort: una clave que solo existe como VALOR de un
  // objeto es invisible para TestAllKeysMatchBetweenFrontendAndCatalog.
  function searchPlaceholder(name) {
    if (name === "chats") return t("placeholder.search_chats");
    if (name === "grupos") return t("placeholder.search_groups");
    if (name === "contactos") return t("placeholder.search_contacts");
    return null;
  }

  // ── tabs: chats / grupos / contactos / agentes / reglas / drafts (S1h
  // shell, extendido T14) — un search/estado compartido, el switch alterna
  // qué panel se ve Y si el buscador aplica acá.
  var tabBtns = document.querySelectorAll(".tab-btn");
  var tabPanels = document.querySelectorAll(".tab-panel");
  var searchInput = document.getElementById("search");

  // applySearchPlaceholder (T165, ct-2026-09-16-2016): SOLO la parte de
  // abajo que también hay que re-correr cada vez que el catálogo cambia —
  // separada de applySearchForTab (que además resetea el filtro/valor, algo
  // que NO querés hacer con el texto que el dueño ya tecleó solo porque
  // cambió el idioma en Opciones). Lee la pestaña activa del DOM en vez de
  // recibirla por parámetro — así funciona igual llamada desde un click de
  // pestaña o desde applyAllI18n(), sin que ninguna le tenga que pasar nada.
  function applySearchPlaceholder() {
    var activeBtn = document.querySelector(".tab-btn.active");
    var placeholder = searchPlaceholder(activeBtn && activeBtn.getAttribute("data-tab"));
    searchInput.classList.toggle("hidden", !placeholder);
    if (placeholder) searchInput.placeholder = placeholder;
    return placeholder;
  }

  // applySearchForTab: la MISMA lógica para el switch de pestaña Y para el
  // estado inicial de la página (evita que "chats", activa por default en
  // index.html, arranque con el placeholder genérico/sin ocultar hasta el
  // primer click). focusInput=false en el arranque — no le robamos el foco
  // a la página recién cargada.
  function applySearchForTab(focusInput) {
    // T14: no arrastra el filtro de la pestaña anterior — una lista vacía
    // sin ninguna pista de que hay un filtro viejo activo se lee como
    // "está roto", no como "no hay resultados acá".
    state.filterText = "";
    searchInput.value = "";
    if (!applySearchPlaceholder()) return;
    if (focusInput) searchInput.focus();
  }

  tabBtns.forEach(function (btn) {
    btn.addEventListener("click", function () {
      tabBtns.forEach(function (b) { b.classList.remove("active"); });
      btn.classList.add("active");
      var name = btn.getAttribute("data-tab");
      tabPanels.forEach(function (p) {
        p.classList.toggle("active", p.getAttribute("data-panel") === name);
      });
      // Fix del smoke (ct-2026-07-22-2018): foco automático al buscador al
      // cambiar de subpestaña — antes había que hacer click en el campo a
      // mano cada vez.
      applySearchForTab(true);
      render();
    });
  });
  applySearchForTab(false);

  // renderLevelCell: LA columna "Nivel" (punto 6 parte 2, ct-2026-07-21-1234)
  // — colapsa lo que antes eran 4 columnas (Nivel/Modo/Confirmación/
  // Ignorado, con los botones auto/dedicated + none/discretion/always que
  // metían scroll horizontal ~803px, queja explícita del boss) en un solo
  // <td>: el indicador visual (mismo mapeo/caja de siempre: ★ ámbar para
  // "boss", esfera de color para el resto) + UN <select> de 5 opciones que
  // muestra el config_level actual y, al cambiar, hace POST
  // /api/admin/config-level — reemplaza el toggle de is_boss y los 3 grupos
  // de botones sueltos por un solo control.
  // buildLevelControl: the indicator+select pair itself, container-agnostic
  // (Chats table wraps it in a <td>; the Grupos header, added in Tramo C
  // ct-2026-07-22-1235, wraps it directly in its flex row instead).
  function buildLevelControl(c) {
    var level = chatLevel(c);

    var wrap = document.createElement("div");
    wrap.className = "levelcell";

    var mark = document.createElement("span");
    mark.className = "indicator " + level;
    mark.title = levelLabel(level, LEVEL_BY_KEY[level].label);
    if (level === "boss") {
      mark.textContent = "★";
    } else {
      var dot = document.createElement("span");
      dot.className = "idot";
      mark.appendChild(dot);
    }
    wrap.appendChild(mark);

    var sel = document.createElement("select");
    sel.className = "levelselect";
    // T121 (ct-2026-09-02-1722, boss verbatim: "boss es un numero"): un
    // grupo no es una persona, así que "Boss" no es una opción real para
    // él — sacado del <select>. El gate que garantiza esto vive en el
    // código (store.SetIsBoss), no acá: esto es solo la parte visible.
    var levelOptions = isGroup(c.jid) ? LEVELS.filter(function (l) { return l.key !== "boss"; }) : LEVELS;
    levelOptions.forEach(function (l) {
      var opt = document.createElement("option");
      opt.value = l.key;
      opt.textContent = originLevelShort(l.key, l.short);
      if (l.key === level) opt.selected = true;
      sel.appendChild(opt);
    });
    sel.onchange = function () {
      post("/api/admin/config-level", { chat_id: c.jid, level: sel.value }).then(loadChats);
    };
    wrap.appendChild(sel);

    // Botón de aprobador (Aprobador P1, ct-2026-07-31-0610) — marca aparte,
    // no un 6to valor del selector de arriba: aprobador mide qué puede
    // PEDIR esta persona (aprobar/descartar borradores, incluso de otros
    // chats), el selector mide cuánta autonomía tiene el AGENTE en este
    // chat. Ortogonales a propósito.
    //
    // Segunda vuelta (boss, tras ver el pin 📌 original): "no comunicaba
    // nada" — un símbolo no alcanza para "puede aprobar mensajes de otros
    // chats". Reemplazado por un botón rectangular con estado legible
    // (relleno=habilitado, contorno=no) + modal propio (openApproverModal,
    // abajo) que explica qué habilita ANTES de tocar nada — nunca
    // window.confirm(), mismo criterio que #agentdeletemodal.
    //
    // Tercera vuelta (boss, sobre el botón ya puesto): "mucho aire...
    // quedó un martillo de contruccion" — texto largo se partía en dos
    // renglones y el 🔨 (emoji de construcción, no existe gavel en Unicode)
    // se veía pesado. Texto corto en una línea ("Aprueba MSG",
    // white-space:nowrap en .approverbtn) + APPROVER_ICON_SVG (arriba).
    // El ícono no explica nada por sí solo a propósito — el boss: "ya con
    // el mensaje de confirmacion quedaría claro su uso" — toda la
    // explicación vive en openApproverModal, no en el botón ni un tooltip.
    //
    // Cuarta vuelta (T81, ct-2026-08-27-2251, boss verbatim: "aprobar
    // mensaje ese boton deberia verse solo para numeros boss y auto, y
    // nada mas") — visible solo en nivel boss/auto, O si el chat YA es
    // aprobador (c.is_approver) sea cual sea su nivel: esconderlo ahí
    // dejaría un aprobador activo sin forma de desmarcarse desde donde se
    // marcó — un permiso que no se puede sacar es peor que un botón de
    // más. Explícito (boss/auto O is_approver), no implícito.
    if (level === "boss" || level === "auto" || c.is_approver) {
      var approverBtn = document.createElement("button");
      approverBtn.type = "button";
      approverBtn.className = "approverbtn" + (c.is_approver ? " active" : "");
      approverBtn.innerHTML = APPROVER_ICON_SVG + " " + t("action.approve_msg");
      approverBtn.onclick = function () {
        openApproverModal(c);
      };
      wrap.appendChild(approverBtn);
    }

    return wrap;
  }

  function renderLevelCell(c) {
    var td = document.createElement("td");
    td.setAttribute("data-label", t("table.level"));
    td.appendChild(buildLevelControl(c));
    return td;
  }

  // agentExclusiveID: mismo parseo que store.AgentExclusiveID (agents.go)
  // — "agent_exclusive:<id>" → <id>, o "" si el chat no está asignado
  // (status de cualquier otro valor: new/whitelist/blacklist/ignored).
  function agentExclusiveID(status) {
    var prefix = "agent_exclusive:";
    if ((status || "").indexOf(prefix) !== 0) return "";
    return status.slice(prefix.length);
  }

  // buildAgentSelect (T56, factorizado en T71, ct-2026-08-27-1410) — el
  // <select> "Sin asignar" + state.agents (principal incluido desde T70)
  // que buildAgentAssignControl y buildOriginAgentControl comparten sin
  // compartir manejo de error: el caller pone su propio onchange.
  function buildAgentSelect(currentAgentID) {
    var sel = document.createElement("select");
    sel.className = "levelselect";
    var optNone = document.createElement("option");
    optNone.value = "";
    optNone.textContent = t("agent.unassigned_option");
    sel.appendChild(optNone);
    state.agents.forEach(function (a) {
      var opt = document.createElement("option");
      opt.value = a.agent_id;
      // T89 (ct-2026-08-28-0628, boss: "falta opcion 'principal' para que se
      // auto asigne en caso de cambio") — la función YA existía, mal
      // etiquetada: a.agent_id para el principal es PrincipalTerminalID, un
      // valor de configuración fijo que una promoción nunca cambia (solo
      // las credenciales detrás cambian, vía PromoteToPrincipal) — elegir
      // esta opción YA ata el chat al puesto, no a la persona. Pero el
      // texto decía "Citrino" a secas, como si atara a la persona; el dueño
      // pidió de nuevo algo que ya tenía porque la etiqueta no lo decía.
      // Un prefijo alcanza — nada de opción/valor nuevo.
      opt.textContent = a.role === "principal" ? t("agent.principal_prefix", { name: a.name || a.agent_id }) : (a.name || a.agent_id);
      if (a.agent_id === currentAgentID) opt.selected = true;
      sel.appendChild(opt);
    });
    return sel;
  }

  // buildAgentAssignControl (T56, ct-2026-08-10-201641) — el control que
  // faltaba: desde CADA chat, elegir qué agente lo atiende. El buscador que
  // ya vive en cada tarjeta de agente (renderAssignSearch, arriba) hace el
  // camino inverso — este es el que el dueño pidió y no existía: parado en
  // el chat, no en el agente. Mismo endpoint, mismo contrato de datos (el
  // propio POST /api/admin/agent-assign no cambia): agent_id vacío limpia
  // la asignación (el chat cae al principal por default de dispatch).
  // El principal SÍ aparece como opción (T70, ct-2026-08-27-1404): asignarlo
  // en exclusiva no es lo mismo que caer en él por defecto — un chat asignado
  // no lo toma otro agente. Con un solo agente registrado (el principal), no
  // ofrecerlo dejaba al dueño sin poder asignar nada.
  // Sin recargar (vara del dueño, ya dicha más de una vez): post + loadChats()
  // en el .then, mismo patrón que buildLevelControl ya usa para Nivel —
  // loadChats() vuelve a traer /api/chats entero, así que si asignar
  // cambiara algo más del chat (status, y lo que dependa de él), se refleja
  // solo, sin código extra acá.
  function buildAgentAssignControl(c) {
    var wrap = document.createElement("div");
    wrap.className = "agentassigncell";
    var currentID = agentExclusiveID(c.status);
    var sel = buildAgentSelect(currentID);

    var result = document.createElement("span");
    result.className = "agentassignresult";

    sel.onchange = function () {
      result.textContent = t("action.saving");
      post("/api/admin/agent-assign", { chat_id: c.jid, agent_id: sel.value }).then(function () {
        result.textContent = "";
        loadChats();
      }).catch(function (e) {
        result.textContent = t("error.prefix") + e.message;
        sel.value = currentID; // revertir la selección visual — el backend no aplicó el cambio
      });
    };

    wrap.appendChild(sel);
    wrap.appendChild(result);

    return wrap;
  }

  function renderAgentAssignCell(c) {
    var td = document.createElement("td");
    td.setAttribute("data-label", t("table.agent"));
    td.appendChild(buildAgentAssignControl(c));
    return td;
  }

  // rulesSourceLabel (ct-2026-07-31, "las reglas por defecto no se ven";
  // i18n T153 2b-iv) — traduce el rules_source que calcula read.go
  // (rulesSourceFor) a texto para el usuario. "particular" no aparece acá:
  // buildRulesControl solo llama a esto cuando c.rules YA está vacío. Antes
  // era un mapa {"tipo:grupo": "Grupos", ...} — convertido a if-chain por el
  // mismo motivo que levelLabel/originLevelShort (clave-como-valor-de-objeto
  // invisible para el test). Tres de los cuatro textos resultaron ser
  // IDÉNTICOS letra por letra a claves que ya existían — reusadas en vez de
  // duplicadas: "Grupos" es tab.groups, "Mensajes nuevos" es
  // rules.new_messages_label, "Contactos del celular" es
  // rules.contacts_label. Solo "General" era nueva (rules.general_label).
  function rulesSourceLabel(source) {
    if (source === "tipo:grupo") return t("tab.groups");
    if (source === "origen:nuevo") return t("rules.new_messages_label");
    if (source === "origen:contacto") return t("rules.contacts_label");
    if (source === "general") return t("rules.general_label");
  }

  // buildRulesControl: reglas-preview + botón ✎ que abre el editmodal
  // (rules/memory/context/nombre de contacto) — mismo par que la columna
  // "Reglas" de la tabla Chats, extraído en Tramo C (ct-2026-07-22-1235)
  // para reusarlo también en el header de grupo.
  //
  // El texto de fallback (ct-2026-07-31, boss: "las reglas por defecto no
  // se ven") distingue DOS estados que "(sin reglas propias)" mezclaba en
  // uno: el chat puede no tener reglas PROPIAS pero sí estar gobernado por
  // General/Grupos/Mensajes nuevos/Contactos (rules_source lo dice) — o
  // puede no haber ninguna regla en ningún nivel, y ESO sí hay que decirlo
  // distinto, porque ahí el agente literalmente no actúa.
  function buildRulesControl(c) {
    var wrap = document.createElement("span");
    wrap.className = "rulescell";
    var preview = document.createElement("span");
    preview.className = "rulespreview";
    if (c.rules) {
      preview.textContent = c.rules;
    } else {
      var sourceLabel = rulesSourceLabel(c.rules_source);
      preview.textContent = sourceLabel
        ? t("rules.no_own_applies_prefix") + sourceLabel
        : t("rules.none_at_any_level");
    }
    // title (T188): la vista previa se corta a dos líneas — las reglas completas, al pasar el cursor.
    preview.title = preview.textContent;
    var editBtn = document.createElement("button");
    editBtn.className = "smbtn";
    editBtn.style.marginLeft = "6px";
    editBtn.textContent = "✎";
    editBtn.onclick = function () { openEditModal(c); };
    wrap.appendChild(preview);
    wrap.appendChild(editBtn);
    return wrap;
  }

  function renderRow(c) {
    var tr = document.createElement("tr");
    if (c.jid === state.selectedJID) tr.classList.add("selected");

    var tdName = document.createElement("td");
    tdName.setAttribute("data-label", t("table.conversation"));
    var rowFlex = document.createElement("div");
    rowFlex.className = "row-flex";
    // T93: si no hay contact_name ni name (WhatsApp), lo único que queda es
    // el número crudo — sigue tapado, sea grupo (id, no es un número de
    // teléfono, no se tapa) o p2p (sí lo es).
    var displayName = maskedName(c.contact_name) || maskedName(c.name) || (isGroup(c.jid) ? jidNumber(c.jid) : maskNumber(jidNumber(c.jid)));
    rowFlex.appendChild(buildAvatar(c.jid, displayName, "avatar-sm"));
    var textWrap = document.createElement("div");
    textWrap.className = "row-textwrap";
    // Name hierarchy (ct-2026-07-11-0030, extendida en S1h con contact_name):
    // agenda del teléfono (primary, big, cuando aplica) > nombre de WhatsApp
    // > número (secondary, small, 1:1 only — a group has no phone number)
    // > pushname (tertiary, "extra" info, only shown if it adds something
    // beyond the name already displayed).
    var nameDiv = document.createElement("div");
    nameDiv.className = "cname";
    nameDiv.textContent = displayName;
    // title (T188): el nombre largo se recorta con "…" — el completo, al pasar el cursor.
    nameDiv.title = displayName;
    var hbadge = historyBadge(c);
    if (hbadge) nameDiv.appendChild(hbadge);
    textWrap.appendChild(nameDiv);
    if (c.name && !isGroup(c.jid)) {
      var numDiv = document.createElement("div");
      numDiv.className = "csub";
      // D1 smoke fix (ct-2026-07-22-2100): a @lid row's raw jidNumber(c.jid)
      // is WhatsApp's internal ID, not the real phone number — prefer the
      // backend-resolved one (same fix as contactRow above).
      numDiv.textContent = maskNumber(jidNumber(c.resolved_number) || jidNumber(c.jid));
      textWrap.appendChild(numDiv);
    }
    // Preview del último mensaje (S1h, /api/chats ahora devuelve last_text).
    if (c.last_text) {
      var previewDiv = document.createElement("div");
      previewDiv.className = "cpreview";
      previewDiv.textContent = c.last_text;
      previewDiv.title = c.last_text;
      textWrap.appendChild(previewDiv);
    }
    var subDiv = document.createElement("div");
    subDiv.className = "csub";
    subDiv.textContent = (isGroup(c.jid) ? t("chat.type_group") : "1:1") + " · " + timeAgo(c.last_ts) + pushNameSuffix(c);
    textWrap.appendChild(subDiv);
    rowFlex.appendChild(textWrap);
    tdName.appendChild(rowFlex);
    tdName.onclick = function () {
      state.selectedJID = c.jid;
      render();
      openPhonePopup(c.jid);
    };
    tr.appendChild(tdName);

    tr.appendChild(renderLevelCell(c));
    tr.appendChild(renderAgentAssignCell(c));

    var tdRules = document.createElement("td");
    tdRules.setAttribute("data-label", t("tab.rules"));
    tdRules.appendChild(buildRulesControl(c));
    tr.appendChild(tdRules);

    return tr;
  }

  // Sort bar (segunda vuelta, boss verbatim: "igual que la barra de
  // busqueda") — el click aplica al instante, mismo patrón que #search's
  // "input" listener arriba: sin "pending" intermedio, sin botón Aplicar.
  // El "Aplicar" existía para no reordenar contra la DB en cada click, pero
  // el orden siempre fue 100% client-side sobre la lista ya cargada — ese
  // problema nunca existió, el botón resolvía algo que no pasaba.
  document.getElementById("sortopts").addEventListener("click", function (ev) {
    if (ev.target.tagName !== "BUTTON") return;
    state.appliedSort = ev.target.dataset.val;
    Array.prototype.forEach.call(document.getElementById("sortopts").children, function (b) {
      b.classList.toggle("active", b.dataset.val === state.appliedSort);
    });
    render();
  });

  // ── Popup celular (S6, ct-2026-07-21-1543) ──────────────────────────────
  // Reemplaza el panel de lectura inline (#chatpanel) por una ventana
  // flotante con forma de celular. Al abrir dispara la ráfaga on-demand
  // (POST /api/media/fetch) y hace polling de /api/messages hasta que toda
  // la media de la página quedó downloaded — el worker de fondo sigue
  // bajando el resto por su cuenta, esto solo acelera lo que se está mirando.
  function selectedChat() {
    return state.chats.find(function (c) { return c.jid === state.selectedJID; });
  }

  function renderMediaEl(media) {
    if (!media) return null;
    // failed (ct-2026-07-29, boss: "los audios dicen descargando... un
    // adjunto que falló con 403 no puede decir 'descargando' para
    // siempre") — WhatsApp ya no tiene el archivo (o se agotaron los
    // reintentos), estado FINAL, distinto de "todavía en cola". Chequear
    // ANTES que !downloaded — failed también implica !downloaded.
    if (media.failed) {
      var failed = document.createElement("div");
      failed.className = "bmedia bfailed";
      failed.textContent = t("media.failed");
      return failed;
    }
    if (!media.downloaded) {
      var pending = document.createElement("div");
      pending.className = "bmedia bpending";
      pending.textContent = t("media.pending");
      return pending;
    }
    var el;
    switch (media.kind) {
      case "photo":
        el = document.createElement("img"); el.className = "bphoto"; el.alt = t("media.photo_alt"); el.src = media.url; return el;
      case "sticker":
        el = document.createElement("img"); el.className = "bsticker"; el.alt = t("media.sticker_alt"); el.src = media.url; return el;
      case "audio":
        el = document.createElement("audio"); el.className = "baudio"; el.controls = true; el.src = media.url; return el;
      case "video":
        el = document.createElement("video"); el.className = "bvideo"; el.controls = true; el.src = media.url; return el;
      case "doc":
        el = document.createElement("a"); el.className = "bdoc"; el.href = media.url; el.target = "_blank"; el.rel = "noopener"; el.textContent = t("media.doc_link"); return el;
      default:
        return null;
    }
  }

  function renderBubble(m) {
    var b = document.createElement("div");
    b.className = "bubble " + (m.from_me ? "out" : "in");
    // D3 (ct-2026-07-22-2100): remitente por burbuja, SOLO presente cuando
    // el backend lo resolvió (grupos, mensaje entrante — read.go's
    // resolveSenderNames) — un 1:1 nunca trae sender/sender_name, así que
    // este bloque es naturalmente un no-op ahí.
    if (m.sender_name || m.sender) {
      var senderEl = document.createElement("div");
      senderEl.className = "bsender";
      // sender solo aparece en mensajes de GRUPO (D3) — el fallback sin
      // sender_name es el número real del miembro que habló, se tapa (T93).
      senderEl.textContent = m.sender_name || maskNumber(jidNumber(m.sender));
      b.appendChild(senderEl);
    }
    if (m.forwarded) {
      var fwd = document.createElement("div");
      fwd.className = "bfwd";
      fwd.textContent = t("message.forwarded");
      b.appendChild(fwd);
    }
    if (m.quoted_preview) {
      var quote = document.createElement("div");
      quote.className = "bquote";
      quote.textContent = "↳ " + m.quoted_preview;
      b.appendChild(quote);
    }
    var mediaEl = renderMediaEl(m.media);
    if (mediaEl) b.appendChild(mediaEl);
    if (m.text) {
      var txt = document.createElement("div");
      txt.className = "btxt";
      txt.textContent = m.text;
      b.appendChild(txt);
    }
    return b;
  }

  var POPUP_POLL_MS = 2500;
  var popupPollTimer = null;
  var currentPopupJID = null;
  // currentPopupGroup/currentPopupView (D3, ct-2026-07-22-2100): only set
  // while a GROUP popup is open — null/"" for a 1:1 popup. currentPopupView
  // is "chat" (default, Citrino's call) or "members"; gates both the SSE
  // live-refresh below and the poll timer so switching to "Lista de
  // miembros" doesn't get silently overwritten by an incoming chat refresh.
  var currentPopupGroup = null;
  var currentPopupView = "chat";

  function stopPopupPolling() {
    if (popupPollTimer) { clearInterval(popupPollTimer); popupPollTimer = null; }
  }

  function loadPhoneMessages(jid) {
    return api("/api/messages?chat=" + encodeURIComponent(jid) + "&limit=50").then(function (msgs) {
      if (jid !== currentPopupJID) return; // el popup cambió de chat o cerró mientras esto viajaba
      var box = document.getElementById("phonebody");
      box.innerHTML = "";
      msgs.forEach(function (m) { box.appendChild(renderBubble(m)); });
      box.scrollTop = box.scrollHeight;
      // ct-2026-07-29: failed cuenta como "terminado" acá igual que
      // downloaded — un adjunto que ya se dio por vencido nunca va a
      // volverse downloaded solo, así que seguir el poll por él es
      // esperar algo que no va a pasar.
      var allSettled = msgs.every(function (m) { return !m.media || m.media.downloaded || m.media.failed; });
      if (allSettled) stopPopupPolling();
    });
  }

  function openPhonePopup(jid) {
    currentPopupJID = jid;
    currentPopupGroup = null;
    document.getElementById("phone_group_toggle").classList.add("hidden");
    var c = state.chats.find(function (x) { return x.jid === jid; });
    document.getElementById("phone_avatar").textContent = "💬";
    document.getElementById("phone_name").textContent = (c && (maskedName(c.contact_name) || maskedName(c.name))) || maskNumber(jidNumber(jid));
    document.getElementById("phone_sub").textContent = isGroup(jid) ? t("chat.type_group") : maskNumber(jidNumber(jid));
    document.getElementById("phonepopup").classList.remove("hidden");
    stopPopupPolling();
    loadPhoneMessages(jid);
    post("/api/media/fetch", { chat_id: jid }).catch(function () {}); // fire-and-forget, arranca la ráfaga de fondo
    popupPollTimer = setInterval(function () { loadPhoneMessages(jid); }, POPUP_POLL_MS);
  }

  // renderGroupMembersView: la lista de miembros (Tramo D original) — ahora
  // una de las 2 vistas del toggle, no la única.
  function renderGroupMembersView(g) {
    var members = state.groupMembers.filter(function (m) { return m.group_jid === g.jid; })
      .slice().sort(function (a, b) { return (a.name || "").localeCompare(b.name || ""); });
    var box = document.getElementById("phonebody");
    box.innerHTML = "";
    members.forEach(function (m) { box.appendChild(contactRow(maskedName(m.name) || t("data.unnamed"), m.jid, m.resolved_number, m)); });
    box.scrollTop = 0;
  }

  // setGroupView (D3, ct-2026-07-22-2100): alterna entre "Ver chat" (default,
  // pedido explícito del boss) y "Lista de miembros". "Ver chat" sin
  // has_messages todavía (el worker D2 no bajó nada de este grupo) muestra
  // un estado de espera en vez de una lista vacía silenciosa.
  function setGroupView(view) {
    currentPopupView = view;
    document.getElementById("phone_toggle_chat").classList.toggle("active", view === "chat");
    document.getElementById("phone_toggle_members").classList.toggle("active", view === "members");
    if (view === "members") {
      renderGroupMembersView(currentPopupGroup);
      return;
    }
    if (!currentPopupGroup.has_messages) {
      var box = document.getElementById("phonebody");
      box.innerHTML = "";
      var pending = document.createElement("p");
      pending.className = "sub";
      pending.textContent = t("group.history_pending");
      box.appendChild(pending);
      return;
    }
    loadPhoneMessages(currentPopupGroup.jid);
  }
  document.getElementById("phone_toggle_chat").addEventListener("click", function () { setGroupView("chat"); });
  document.getElementById("phone_toggle_members").addEventListener("click", function () { setGroupView("members"); });

  // openGroupDetail (Tramo D, ct-2026-07-22-123556 — toggle agregado en D3,
  // ct-2026-07-22-2100): reusa el mismo #phonepopup/#phonebody. currentPopupJID
  // SÍ se setea ahora (a diferencia de la versión Tramo D) — "Ver chat"
  // necesita el mismo refresco en vivo (SSE + poll) que un 1:1; el guard
  // real contra pisar "Lista de miembros" vive en currentPopupView, no en
  // dejar currentPopupJID en null.
  function openGroupDetail(g) {
    stopPopupPolling();
    currentPopupJID = g.jid;
    currentPopupGroup = g;
    document.getElementById("phone_avatar").textContent = "👥";
    document.getElementById("phone_name").textContent = g.name || jidNumber(g.jid);
    var memberCount = state.groupMembers.filter(function (m) { return m.group_jid === g.jid; }).length;
    document.getElementById("phone_sub").textContent = t("chat.type_group") + " · " + t("group.member_count", { n: memberCount, s: memberCount === 1 ? "" : "s" });
    document.getElementById("phone_group_toggle").classList.remove("hidden");
    document.getElementById("phonepopup").classList.remove("hidden");
    setGroupView("chat");
    if (g.has_messages) {
      post("/api/media/fetch", { chat_id: g.jid }).catch(function () {});
      popupPollTimer = setInterval(function () {
        if (currentPopupView === "chat") loadPhoneMessages(g.jid);
      }, POPUP_POLL_MS);
    }
  }

  function closePhonePopup() {
    currentPopupJID = null;
    currentPopupGroup = null;
    stopPopupPolling();
    document.getElementById("phonepopup").classList.add("hidden");
  }
  document.getElementById("phone_close").addEventListener("click", closePhonePopup);

  // ── Edit modal (rules/memory/context) ───────────────────────────────────
  var editingJID = null;
  function openEditModal(c) {
    editingJID = c.jid;
    document.getElementById("edittitle").textContent = t("action.edit") + " — " + (maskedName(c.name) || maskNumber(jidNumber(c.jid)));
    document.getElementById("edit_contactname").value = c.contact_name || "";
    document.getElementById("edit_rules").value = c.rules || "";
    document.getElementById("edit_memory").value = c.memory || "";
    document.getElementById("edit_context").value = c.context || "";
    document.getElementById("editmodal").classList.remove("hidden");
  }
  document.getElementById("edit_cancel").addEventListener("click", function () {
    document.getElementById("editmodal").classList.add("hidden");
  });
  document.getElementById("edit_save").addEventListener("click", function () {
    if (!editingJID) return;
    Promise.all([
      post("/api/admin/contact-name", { chat_id: editingJID, name: document.getElementById("edit_contactname").value }),
      post("/api/admin/chat-rules", { chat_id: editingJID, rules: document.getElementById("edit_rules").value }),
      post("/api/admin/memory", { chat_id: editingJID, memory: document.getElementById("edit_memory").value }),
      post("/api/admin/context", { chat_id: editingJID, context: document.getElementById("edit_context").value }),
    ]).then(function () {
      document.getElementById("editmodal").classList.add("hidden");
      loadChats();
    });
  });

  // ── Pestaña Drafts (Tramo C, ct-2026-07-22-1235; pestaña propia + editar/
  // rechazar T16, ct-2026-08-05-123257) — borradores retenidos por
  // confirmation_mode, esperando acción del boss (GET
  // /api/admin/pending-drafts, Store.PendingDrafts). El nombre del chat se
  // resuelve contra state.chats/groups ya cargados — no hace falta que el
  // backend lo duplique en la respuesta.
  // draftChatLabel: el nombre del chat de origen de cada borrador — un solo
  // lugar cubre las 3 llamadas (lista, editar, rechazar). T93: sin nombre,
  // el fallback es un número real solo si el chat es p2p (un grupo cae a
  // su id, que no es un número de teléfono) — ese número sigue tapado.
  function draftChatLabel(chatJID) {
    var c = state.chats.concat(state.groups).find(function (x) { return x.jid === chatJID; });
    var name = c && (maskedName(c.contact_name) || maskedName(c.name));
    if (name) return name;
    return isGroup(chatJID) ? jidNumber(chatJID) : maskNumber(jidNumber(chatJID));
  }
  function renderPendingDrafts(drafts) {
    var countLabel = t("draft.pending_count", { n: drafts.length, s: drafts.length === 1 ? "" : "s" });
    document.getElementById("draftcount").textContent = countLabel;
    var badge = document.getElementById("draftbadge");
    badge.textContent = drafts.length;
    badge.classList.toggle("hidden", drafts.length === 0);
    var list = document.getElementById("draftlist");
    list.innerHTML = "";
    drafts.forEach(function (d) {
      var row = document.createElement("div");
      row.className = "draftrow";
      var info = document.createElement("div");
      info.className = "draftinfo";
      var who = document.createElement("div");
      who.className = "draftwho";
      who.textContent = draftChatLabel(d.chat_jid) + (d.round > 1 ? t("draft.round_suffix", { n: d.round }) : "");
      var text = document.createElement("div");
      text.className = "drafttext";
      text.textContent = d.text;
      text.title = d.text; // se corta con "…" (T188): el borrador completo, al pasar el cursor
      info.appendChild(who);
      info.appendChild(text);
      var actions = document.createElement("div");
      actions.className = "draftactions";
      var approveBtn = document.createElement("button");
      approveBtn.className = "smbtn";
      approveBtn.textContent = t("draft.approve_button");
      approveBtn.onclick = function () {
        post("/api/admin/approve-draft", { id: d.id }).then(loadPendingDrafts);
      };
      var editBtn = document.createElement("button");
      editBtn.className = "smbtn";
      editBtn.textContent = t("draft.edit_button");
      editBtn.onclick = function () { openDraftEditModal(d); };
      var rejectBtn = document.createElement("button");
      rejectBtn.className = "smbtn";
      rejectBtn.textContent = t("draft.reject_button");
      rejectBtn.onclick = function () { openDraftRejectModal(d); };
      var discardBtn = document.createElement("button");
      discardBtn.className = "smbtn";
      discardBtn.textContent = t("draft.discard_button");
      discardBtn.onclick = function () {
        post("/api/admin/discard-draft", { id: d.id }).then(loadPendingDrafts);
      };
      actions.appendChild(approveBtn);
      actions.appendChild(editBtn);
      actions.appendChild(rejectBtn);
      actions.appendChild(discardBtn);
      row.appendChild(info);
      row.appendChild(actions);
      list.appendChild(row);
    });
  }
  function loadPendingDrafts() {
    return api("/api/admin/pending-drafts").then(renderPendingDrafts);
  }

  // openDraftEditModal/openDraftRejectModal (T16, ct-2026-08-05-123257) —
  // mismo esqueleto de modal que #agentdeletemodal/#approvermodal arriba:
  // nada de window.prompt para el motivo de rechazo (mismo criterio anti-
  // window.confirm del resto del tablero).
  var draftEditingId = null;
  function openDraftEditModal(d) {
    draftEditingId = d.id;
    document.getElementById("draftEditTitle").textContent = t("action.edit") + " — " + draftChatLabel(d.chat_jid);
    document.getElementById("draftEdit_text").value = d.text;
    document.getElementById("draftEdit_result").textContent = "";
    document.getElementById("draftEditModal").classList.remove("hidden");
  }
  function closeDraftEditModal() {
    draftEditingId = null;
    document.getElementById("draftEditModal").classList.add("hidden");
  }
  document.getElementById("draftEdit_cancel").addEventListener("click", closeDraftEditModal);
  document.getElementById("draftEdit_cancel2").addEventListener("click", closeDraftEditModal);
  document.getElementById("draftEdit_save").addEventListener("click", function () {
    if (!draftEditingId) return;
    var result = document.getElementById("draftEdit_result");
    result.textContent = t("action.saving");
    post("/api/admin/edit-draft", { id: draftEditingId, text: document.getElementById("draftEdit_text").value }).then(function () {
      closeDraftEditModal();
      loadPendingDrafts();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });

  var draftRejectingId = null;
  function openDraftRejectModal(d) {
    draftRejectingId = d.id;
    document.getElementById("draftRejectTitle").textContent = t("action.reject") + " — " + draftChatLabel(d.chat_jid);
    document.getElementById("draftReject_reason").value = "";
    document.getElementById("draftReject_result").textContent = "";
    document.getElementById("draftRejectModal").classList.remove("hidden");
  }
  function closeDraftRejectModal() {
    draftRejectingId = null;
    document.getElementById("draftRejectModal").classList.add("hidden");
  }
  document.getElementById("draftReject_cancel").addEventListener("click", closeDraftRejectModal);
  document.getElementById("draftReject_cancel2").addEventListener("click", closeDraftRejectModal);
  document.getElementById("draftReject_confirm").addEventListener("click", function () {
    if (!draftRejectingId) return;
    var result = document.getElementById("draftReject_result");
    var reason = document.getElementById("draftReject_reason").value.trim();
    if (!reason) {
      result.textContent = t("validation.reject_reason_required");
      return;
    }
    result.textContent = t("action.rejecting");
    post("/api/admin/reject-draft", { id: draftRejectingId, reason: reason }).then(function () {
      closeDraftRejectModal();
      loadPendingDrafts();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });

  // ── QR ───────────────────────────────────────────────────────────────────
  // startReconnectFlow (ct-2026-07-24-0527, dashboard auto-refresh): abre
  // el overlay QR y dispara el re-pareo en caliente (POST
  // /api/admin/reconnect) — el overlay se abre INMEDIATAMENTE (puede
  // mostrar "Conectando…" primero); el polling de loadStatus trae
  // show_qr=true cuando los códigos llegan. Compartido por "Conectar QR"
  // (P1, ct-2026-07-24-0015) y por el flujo post-Desconectar de abajo —
  // antes ese segundo camino no llamaba a nada de esto y le decía al boss
  // "reiniciá el gateway" (texto de antes de que P1 existiera).
  function startReconnectFlow() {
    document.getElementById("qrimg").src = withKey("/api/qr/image");
    setQROverlay(true);
    post("/api/admin/reconnect").then(function () {
      loadStatus();
    }).catch(function (e) {
      loadStatus();
    });
  }
  document.getElementById("qrbtn").addEventListener("click", startReconnectFlow);
  document.getElementById("qrclose").addEventListener("click", function () {
    stopQRRefreshTimer();
    setQROverlay(false);
  });

  // P2 (ct-2026-07-24-0015): QR countdown + expired state + refresh.
  // While the QR overlay is open, update the timer from /api/status
  // every second. When expired, show greyed QR + "Refrescar" button.
  var qrTimerInterval = null;
  function stopQRRefreshTimer() {
    if (qrTimerInterval) { clearInterval(qrTimerInterval); qrTimerInterval = null; }
  }
  function startQRRefreshTimer() {
    stopQRRefreshTimer();
    qrTimerInterval = setInterval(function () {
      if (document.getElementById("qroverlay").classList.contains("hidden")) {
        stopQRRefreshTimer();
        return;
      }
      loadStatus();
    }, 1000);
  }

  // qrUpdateTimer is called from loadStatus to update the QR overlay
  // state (countdown, expired, refresh button) based on status fields.
  // Called every status poll; only does work if the QR overlay is open.
  function qrUpdateTimer(s) {
    if (document.getElementById("qroverlay").classList.contains("hidden")) return;
    var expiryEl = document.getElementById("qrexpiry");
    var refreshBtn = document.getElementById("qrrefresh");
    var img = document.getElementById("qrimg");

    if (s.qr_expired) {
      expiryEl.textContent = t("qr.expired");
      expiryEl.style.color = "#ff6b6b";
      refreshBtn.classList.remove("hidden");
      img.style.opacity = "0.3";
      stopQRRefreshTimer();
      return;
    }

    var expiresAt = s.qr_expires_at;
    if (expiresAt && expiresAt > 0) {
      var remaining = expiresAt - Math.floor(Date.now() / 1000);
      if (remaining <= 0) {
        expiryEl.textContent = t("qr.expired");
        expiryEl.style.color = "#ff6b6b";
        refreshBtn.classList.remove("hidden");
        img.style.opacity = "0.3";
        stopQRRefreshTimer();
      } else {
        expiryEl.textContent = t("qr.expires_in", { n: remaining });
        expiryEl.style.color = "";
        refreshBtn.classList.add("hidden");
        img.style.opacity = "1";
        startQRRefreshTimer();
      }
    } else if (s.qr_error) {
      // T188/T187: en vez de quedarse en "Generando QR…" sin decir por qué no
      // llega, lo dice. qr_error es un CÓDIGO (hoy uno solo, "qr_unavailable":
      // Piumy no pudo abrir la ronda de QR), no el texto de la librería, que
      // está en inglés, cambia entre versiones y se queda en el log. Un código
      // futuro cae en este mismo mensaje hasta que tenga el suyo. El botón de
      // refrescar queda a mano para reintentar.
      expiryEl.textContent = t("qr.error_unavailable");
      expiryEl.style.color = "#ff6b6b";
      refreshBtn.classList.remove("hidden");
    } else {
      expiryEl.textContent = s.qr_code ? t("qr.waiting_for_code") : t("qr.generating");
      expiryEl.style.color = "";
      refreshBtn.classList.add("hidden");
      img.style.opacity = "1";
    }
  }

  document.getElementById("qrrefresh").addEventListener("click", function () {
    // Refresh expired QR: reset overlay state, call reconnect.
    var img = document.getElementById("qrimg");
    img.style.opacity = "1";
    img.src = withKey("/api/qr/image") + "&_=" + Date.now();
    document.getElementById("qrexpiry").textContent = t("action.renewing");
    document.getElementById("qrrefresh").classList.add("hidden");
    post("/api/admin/reconnect").then(function () {
      loadStatus();
    }).catch(function () {
      loadStatus();
    });
  });

  // ── Desconectar (M3, ct-2026-07-22-2342) ────────────────────────────────
  // Modal propio (no window.confirm()). Tras confirmar, Logout() borra la
  // sesión local. Después el boss usa "Conectar QR" para re-parear sin
  // reiniciar (Fix 2, ct-2026-07-23-0047).
  document.getElementById("disconnectbtn").addEventListener("click", function () {
    document.getElementById("disconnect_result").textContent = "";
    document.getElementById("disconnectmodal").classList.remove("hidden");
  });
  document.getElementById("disconnect_cancel").addEventListener("click", function () {
    document.getElementById("disconnectmodal").classList.add("hidden");
  });
  document.getElementById("disconnect_confirm").addEventListener("click", function () {
    var result = document.getElementById("disconnect_result");
    result.textContent = t("action.disconnecting");
    post("/api/admin/disconnect", {}).then(function () {
      // ct-2026-07-24-0527 (reemplaza Fix 1, ct-2026-07-23-0047): antes
      // decía "reiniciá el gateway para ver el QR nuevo" — texto de ANTES
      // de que P1 (ct-2026-07-24-0015) agregara el re-pareo en caliente.
      // Logout() no publica ningún evento al eventbus (doc propia del
      // método, whatsmeow.Adapter.Logout) — nada le avisa solo al
      // dashboard que hay que re-parear, así que lo disparamos acá mismo:
      // cerrar el modal y abrir el QR de una, mismo camino que "Conectar
      // QR" (startReconnectFlow).
      document.getElementById("disconnectmodal").classList.add("hidden");
      startReconnectFlow();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });

  // ── Live nudge + auto-refresco (F4b eventbus, ct-2026-07-24-0527) ───────
  // El boss: "que el dashboard tenga mecanismos de auto refresco... nada de
  // andar presionando ctrl f5, eso es producto de mala calidad." Diseño
  // (decisión de Citrino, no inventado acá): (1) tabla declarativa
  // evento→loaders — agregar un evento nuevo obliga a decir qué refresca,
  // no hay rama if/return donde "olvidarse" de un refresh; (2) debounce en
  // los LOADERS, no coalescing en el backend — cubre ráfagas de cualquier
  // MEZCLA de tipos de evento, no solo uno; (3) red de seguridad de
  // reconexión con backoff — una conexión medio-caída (sleep del equipo,
  // gateway reiniciado sin cerrar limpio el socket) nunca dispara
  // `onerror`, silencio es la única señal, así que un watchdog por
  // inactividad es la única forma de notarla. Nada de setInterval
  // recargando todo a fuerza bruta — cada pieza dispara solo ante una señal
  // real (un evento, o la inactividad prolongada de la propia conexión).

  // debounce: agrupa llamadas seguidas en UNA, tras ~300-500ms de calma
  // (ventana aleatoria — mismo estilo anti-fingerprint que los delays
  // random del backend Go). Corto a propósito: se siente "al toque", no
  // como polling.
  function debounce(fn) {
    var timer = null;
    return function () {
      if (timer) clearTimeout(timer);
      timer = setTimeout(function () {
        timer = null;
        fn();
      }, 300 + Math.random() * 200);
    };
  }
  // UNA instancia por loader subyacente, compartida entre filas de
  // REFRESH_ON — así una ráfaga que mezcla tipos de evento (ej: varios
  // "history_batch" seguidos de un "message" en vivo) coalesce en una sola
  // recarga de cada vista, no una por tipo de evento.
  var debouncedLoadStatus = debounce(function () { loadStatus().catch(function () {}); });
  var debouncedLoadChats = debounce(loadChats);
  // T16 (ct-2026-08-05-123257, Citrino: "que el contador se actualice
  // solo... un borrador que aparece y no se ve hasta recargar es una
  // respuesta que salió tarde") — mismo colgado del ciclo existente que
  // debouncedLoadStatus/debouncedLoadChats, no un polling aparte.
  var debouncedLoadPendingDrafts = debounce(function () { loadPendingDrafts().catch(function () {}); });

  // REFRESH_ON: único lugar de decisión "este evento, estas vistas". Sin
  // entrada para un tipo → cae al default (status únicamente) — nunca "no
  // hace nada", pero si necesita más hay que declararlo acá, no hay dónde
  // esconder el olvido.
  var REFRESH_ON = {
    wa_connected: [debouncedLoadStatus, debouncedLoadChats],
    wa_disconnected: [debouncedLoadStatus],
    history_batch: [debouncedLoadStatus, debouncedLoadChats],
    message: [debouncedLoadStatus, debouncedLoadChats],
    draft: [debouncedLoadPendingDrafts],
  };
  var DEFAULT_REFRESH = [debouncedLoadStatus];

  // refreshEverything: la red de seguridad al (re)conectar — "no confiar en
  // qué se perdió mientras la conexión estuvo caída" — corre TODOS los
  // loaders que el dashboard ya conoce, no solo status+chats.
  function refreshEverything() {
    debouncedLoadStatus();
    debouncedLoadChats();
    loadPendingDrafts().catch(function () {});
    loadAgents().catch(function () {});
    // T172d: Clientes entra a la red de seguridad por el MISMO argumento
    // que el resto ("no confiar en qué se perdió mientras la conexión
    // estuvo caída"), pero solo si está a la vista — ver
    // refreshClientsIfVisible.
    refreshClientsIfVisible();
  }

  var eventSource = null;
  var lastSSEActivityAt = 0;
  // lastSSEMessageAt (T172a, ct-2026-11-10-2000): el reloj del RENGLÓN de
  // frescura, separado del de arriba a propósito. lastSSEActivityAt es "lo
  // último que hicimos con la conexión" y se pone también al CREAR el
  // EventSource y en onopen — útil para el watchdog (que pregunta "¿hay que
  // reintentar?"), inservible para la pantalla: en una caída de verdad el
  // watchdog reconecta cada ~46s, y un renglón alimentado de ahí volvería a
  // decir "actualizado ahora" sin que hubiera llegado un solo byte de
  // datos. Esto se mueve SÓLO en onmessage, que es el único momento en que
  // el servidor habló de verdad (el latido de 20s cuenta: es el servidor
  // diciendo "sigo acá"). Comparten SSE_STALE_MS, eso sí — el umbral del
  // aviso y el de la reconexión tienen que ser el mismo número o la
  // pantalla y el mecanismo cuentan historias distintas.
  var lastSSEMessageAt = 0;
  var sseReconnectDelayMs = 1000;
  var SSE_RECONNECT_DELAY_MAX_MS = 30000;
  // SSE_STALE_MS: más del doble del heartbeat del backend (20s) — un
  // heartbeat perdido ocasional no dispara nada, dos seguidos sí.
  var SSE_STALE_MS = 45000;
  var sseReconnectTimer = null;

  function scheduleSSEReconnect() {
    if (eventSource) { eventSource.close(); eventSource = null; }
    if (sseReconnectTimer) return; // ya hay una reconexión programada
    sseReconnectTimer = setTimeout(function () {
      sseReconnectTimer = null;
      connectEvents();
    }, sseReconnectDelayMs);
    sseReconnectDelayMs = Math.min(sseReconnectDelayMs * 2, SSE_RECONNECT_DELAY_MAX_MS);
  }

  // sseWatchdog: la pieza que hoy no existe (Citrino, punto 4) — sin esto,
  // una conexión medio-caída (nunca dispara onerror, el navegador no tiene
  // cómo saber que el socket murió) deja al dashboard mudo para siempre.
  // Corre una sola vez para toda la vida de la página (armado más abajo,
  // junto a los demás setInterval del arranque) — no se re-arma por
  // conexión, un solo chequeo periódico alcanza.
  function armSSEWatchdog() {
    setInterval(function () {
      if (lastSSEActivityAt && Date.now() - lastSSEActivityAt > SSE_STALE_MS) {
        scheduleSSEReconnect();
      }
    }, 10000);
  }

  // renderFreshness (T172a, ct-2026-11-10-2000): el renglón que dice hace
  // cuánto llegó el último dato — el dueño, verbatim vía Citrino: "quiero
  // que piumy sea preciso". El auto-refresco por SSE ya existía desde
  // ct-2026-07-24-0527; lo que faltaba era MOSTRARLO: hasta acá
  // lastSSEActivityAt sólo servía para que el watchdog reconectara, nunca
  // llegaba a la pantalla, así que un tablero vivo y uno congelado se veían
  // exactamente igual.
  //
  // Usa el MISMO SSE_STALE_MS que el watchdog (ver lastSSEMessageAt arriba
  // para por qué el reloj sí es otro): el umbral del aviso y el de la
  // reconexión tienen que coincidir. No toca nada, sólo pinta lo que el SSE
  // ya sabe — cuando la reconexión del watchdog prende y el servidor vuelve
  // a hablar, onmessage mueve el reloj y el aviso se apaga solo, sin F5
  // (regla de producto del dueño, 2026-07-29).
  //
  // Vacío mientras no haya llegado el primer mensaje: antes de eso (los ≤20s
  // que tarda el primer latido del backend, o siempre si el navegador no
  // soporta EventSource y connectEvents lo traga) no hay nada que contar, y
  // un "hace 0s" sería una mentira cómoda.
  function renderFreshness() {
    var el = document.getElementById("fresh");
    if (!el) return;
    if (!lastSSEMessageAt) { el.textContent = ""; el.classList.remove("stale"); return; }
    var ms = Date.now() - lastSSEMessageAt;
    if (ms > SSE_STALE_MS) {
      el.textContent = t("live.signal_lost");
      el.classList.add("stale");
      return;
    }
    // Mismos tres tramos que #badgesync ya usa para su "ago" — una sola
    // forma de decir un tiempo en todo el tablero.
    var sec = Math.round(ms / 1000);
    var ago = sec < 5 ? t("time.now")
      : sec < 60 ? t("time.ago_seconds", { n: sec })
      : t("time.ago_minutes", { n: Math.round(sec / 60) });
    el.textContent = t("live.updated_ago", { ago: ago });
    el.classList.remove("stale");
  }

  function connectEvents() {
    if (eventSource) { eventSource.close(); }
    try {
      eventSource = new EventSource(withKey("/api/events"));
      lastSSEActivityAt = Date.now();
      eventSource.onopen = function () {
        sseReconnectDelayMs = 1000; // conexión sana: resetea el backoff
        lastSSEActivityAt = Date.now();
        // Red de seguridad: nunca confiar en qué se perdió mientras la
        // conexión estuvo caída (o en el primer load, da lo mismo — el
        // costo de una recarga de más al abrir la página es nada).
        refreshEverything();
      };
      eventSource.onmessage = function (ev) {
        lastSSEActivityAt = Date.now();
        lastSSEMessageAt = lastSSEActivityAt; // T172a: llegó un dato de verdad
        renderFreshness();                    // sin esperar al tick del segundo
        var data = null;
        try { data = JSON.parse(ev.data); } catch (e) {}
        // heartbeat (ct-2026-07-24-0527): solo sostiene lastSSEActivityAt
        // — nunca dispara refresh, o sería un polling de 20s disfrazado.
        if (!data || data.type === "heartbeat") return;
        (REFRESH_ON[data.type] || DEFAULT_REFRESH).forEach(function (fn) { fn(); });
        // D3 (ct-2026-07-22-2100): un popup de grupo en "Lista de miembros"
        // no debe pisarse con burbujas de chat al llegar un mensaje nuevo —
        // solo refresca en vivo si es un 1:1 (currentPopupGroup null) o si
        // el grupo está en la vista "chat".
        if (data.jid && data.jid === currentPopupJID && (!currentPopupGroup || currentPopupView === "chat")) {
          loadPhoneMessages(currentPopupJID);
        }
      };
      // onerror cubre la caída "ruidosa" (el navegador SÍ nota el socket
      // cerrado) — closeamos nosotros mismos en vez de dejar que
      // EventSource reintente solo, para tener backoff exponencial real
      // (su reintento nativo es un delay fijo, no lo que pidió Citrino).
      eventSource.onerror = function () {
        scheduleSSEReconnect();
      };
    } catch (e) {
      // EventSource unsupported/blocked — the dashboard still works, just
      // without live refresh; a manual reload picks up changes.
    }
  }

  // ── login (ct-2026-07-19-1616, S1d) ─────────────────────────────────────
  function submitLogin() {
    var result = document.getElementById("login_result");
    result.textContent = t("auth.signing_in");
    post("/api/auth/login", {
      username: document.getElementById("login_user").value,
      password: document.getElementById("login_pass").value,
    }).then(function () {
      document.getElementById("login_pass").value = "";
      result.textContent = "";
      loadStatus().then(function () {
        loadChats();
        loadPendingDrafts();
        loadAgents();
        loadOriginDefaults();
        connectEvents();
      }).catch(function () {}); // already surfaced via showLogin() inside loadStatus
    }).catch(function () {
      result.textContent = t("auth.invalid_credentials");
    });
  }
  document.getElementById("login_submit").addEventListener("click", submitLogin);
  document.getElementById("login_pass").addEventListener("keydown", function (ev) {
    if (ev.key === "Enter") submitLogin();
  });

  // ── recuperar contraseña — WhatsApp (ct-2026-07-19-1652, S1e-1) o correo
  // (ct-2026-07-19-1716, S1e-2) ────────────────────────────────────────────
  // POST /api/auth/recover responds identically whether or not anything was
  // actually sent (server-side, on purpose — no state leak) — the UI mirrors
  // that: same message on .then and .catch, never a distinct error, for
  // EITHER method.
  function requestRecoveryCode(method) {
    var result = document.getElementById("recover_send_result");
    result.textContent = t("auth.sending_code");
    var sameMessage = function () { result.textContent = t("auth.code_sent_generic"); };
    post("/api/auth/recover", { method: method }).then(sameMessage).catch(sameMessage);
  }
  document.getElementById("recover_link").addEventListener("click", function (ev) {
    ev.preventDefault();
    document.getElementById("recover_send_result").textContent = "";
    document.getElementById("recover_code").value = "";
    document.getElementById("recover_new").value = "";
    document.getElementById("recover_repeat").value = "";
    document.getElementById("recover_result").textContent = "";
    hideLogin();
    document.getElementById("recovermodal").classList.remove("hidden");
  });
  document.getElementById("recover_send_whatsapp").addEventListener("click", function () { requestRecoveryCode("whatsapp"); });
  document.getElementById("recover_send_email").addEventListener("click", function () { requestRecoveryCode("email"); });
  document.getElementById("recover_cancel").addEventListener("click", function () {
    document.getElementById("recovermodal").classList.add("hidden");
    showLogin();
  });
  document.getElementById("recover_submit").addEventListener("click", function () {
    var result = document.getElementById("recover_result");
    var newPass = document.getElementById("recover_new").value;
    if (newPass !== document.getElementById("recover_repeat").value) {
      result.textContent = t("validation.passwords_mismatch");
      return;
    }
    post("/api/auth/recover/verify", {
      code: document.getElementById("recover_code").value,
      new_password: newPass,
    }).then(function () {
      document.getElementById("recovermodal").classList.add("hidden");
      document.getElementById("login_result").textContent = t("auth.password_reset_success");
      showLogin();
    }).catch(function () {
      result.textContent = t("auth.invalid_or_expired_code");
    });
  });

  // refreshGovernorInfo (T165, ct-2026-09-16-2016): separada de
  // openConfigModal para que applyAllI18n() la pueda re-correr — este
  // renglón lo arma JS (el hueco {n}), así que el paso declarativo de
  // applyI18n() nunca lo alcanza. Segura de llamar con el modal cerrado:
  // solo reescribe un textContent que en ese caso está fuera de pantalla.
  function refreshGovernorInfo() {
    var rate = (state.lastStatus && state.lastStatus.governor_rate_per_min) || 0;
    document.getElementById("config_governor_info").textContent = t("governor.rate_info", { n: rate });
  }

  // ── auto-actualización (T182, ct-2026-11-11-0152) ──────────────────────
  // updateStatusText: LA frase de estado, armada con claves del catálogo y
  // nunca pegada a mano (el orden de las palabras cambia entre idiomas). El
  // servidor manda códigos y datos, no texto: u.blocked llega como
  // "busy" / "other_instance" / "no_key" / "not_installed" / "other_account"
  // y la frase la arma esta función. Los dos últimos apagan u.supported, así
  // que entran por la primera rama. u.failure (el motivo, del registro) NO se
  // muestra: la frase solo dice que falló y dónde mirar.
  //
  // El orden de las ramas es el de la realidad, no el del JSON: lo que ya
  // está en vuelo (pending) gana sobre lo que podría pasar, y el interruptor
  // apagado gana sobre "se instala sola" -- prometer una instalación
  // automática con la auto-actualización apagada sería mentirle al dueño.
  // Esa rama (modal.update_state_off) es el único agregado al fragmento que
  // dejó Tourmaline en docs/T176-AUTOUPDATE.md.
  function updateStatusText(u) {
    var parts = [];
    if (!u.supported) {
      parts.push(t("modal.update_state_unsupported"));
    } else if (u.pending) {
      parts.push(t("modal.update_state_installing", { pending: u.pending }));
    } else if (u.available && u.given_up) {
      parts.push(t("modal.update_state_failed", { latest: u.latest }));
    } else if (u.available && u.blocked === "no_key") {
      parts.push(t("modal.update_state_nokey", { latest: u.latest }));
    } else if (u.available && !u.enabled) {
      parts.push(t("modal.update_state_off", { latest: u.latest }));
    } else if (u.available && u.ready) {
      parts.push(t("modal.update_state_ready", { latest: u.latest }));
      if (u.blocked === "busy") parts.push(t("modal.update_blocked_busy"));
      if (u.blocked === "other_instance") parts.push(t("modal.update_blocked_other_instance"));
    } else if (u.available) {
      parts.push(t("modal.update_state_found", { latest: u.latest }));
    } else {
      parts.push(t("modal.update_state_current", { current: u.current }));
    }
    if (u.installed_version && u.installed_at) {
      var when = new Date(u.installed_at * 1000).toLocaleString(clientLang(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
      parts.push(t("modal.update_installed", { version: u.installed_version, when: when }));
    }
    return parts.join(" ");
  }

  // refreshUpdateInfo: repinta la frase desde state.lastUpdate, sin red --
  // exactamente el mismo caso que refreshGovernorInfo, y por eso entra por
  // la misma puerta (applyAllI18n). Sin esto el dueño cambia el idioma en
  // ESTE mismo modal y la frase se queda en el anterior. Segura con el modal
  // cerrado, igual que refreshGovernorInfo.
  //
  // Los TRES estados del renglón son distintos y ninguno se puede confundir
  // con otro: hay un Status → la frase; se preguntó y el servidor no
  // contestó → se dice; todavía no se preguntó → vacío. El del medio lo
  // encontró la mirada visual de T182: con /api/update caído, el interruptor
  // sale apagado y sin una palabra, y el dueño lee "la actualización
  // automática está apagada" cuando lo cierto es "no se pudo preguntar". Un
  // silencio también afirma. Y decir "al día" sin saberlo sería mentir.
  function refreshUpdateInfo() {
    var el = document.getElementById("config_update_status");
    if (state.lastUpdate) {
      el.textContent = updateStatusText(state.lastUpdate);
      return;
    }
    el.textContent = state.updateUnreachable ? t("modal.update_state_unknown") : "";
  }

  // loadUpdateStatus: el estado REAL, leído del servidor -- nunca se asume.
  // withSwitch solo es true al abrir Opciones: el refresco periódico NO toca
  // el interruptor, o le pisaría al dueño el que acaba de mover mientras
  // todavía no apretó Guardar.
  function loadUpdateStatus(withSwitch) {
    return api("/api/update").then(function (u) {
      state.lastUpdate = u;
      state.updateUnreachable = false;
      if (withSwitch) document.getElementById("config_update_enabled").checked = !!u.enabled;
      refreshUpdateInfo();
    }).catch(function () {
      state.lastUpdate = null;
      state.updateUnreachable = true;
      if (withSwitch) document.getElementById("config_update_enabled").checked = false;
      refreshUpdateInfo();
    });
  }

  // refreshUpdateIfVisible: el estado se mueve SOLO (se verifica una
  // versión, arranca la instalación, se libera el bloqueo) y el dueño tiene
  // que verlo cambiar sin F5. Mismo molde y mismo ritmo que
  // refreshClientsIfVisible: solo mientras Opciones está abierto, cero
  // tráfico con el modal cerrado.
  function refreshUpdateIfVisible() {
    var modal = document.getElementById("configmodal");
    if (!modal || modal.classList.contains("hidden")) return;
    loadUpdateStatus(false);
  }

  // applyAllI18n (T165, ct-2026-09-16-2016) — LA puerta única para todo el
  // texto de interfaz: lo declarativo ([data-i18n], vía applyI18n()) MÁS lo
  // que arma JS y ningún poll periódico vuelve a dibujar por su cuenta
  // (loadChats/loadAgents/loadPendingDrafts SÍ redibujan sus propios t()
  // cada 15s solos — esta puerta no hace falta para ésos). Nació de la
  // mirada visual de T164: dos textos — el placeholder de búsqueda de
  // Chats y el renglón de Governor en Opciones — se armaban una sola vez y
  // nadie les avisaba que el catálogo o el idioma habían cambiado. Mismo
  // defecto, dos caras. Llamar SIEMPRE esta función, nunca applyI18n() ni
  // los refrescos de abajo por separado, en los dos momentos que importan:
  // apenas loadI18n() resuelve (arranque) y después de guardar un idioma
  // nuevo en Opciones. Un tercer texto de esta clase solo necesita una
  // línea más acá adentro para quedar cubierto para siempre.
  function applyAllI18n() {
    applyI18n();
    renderMoodLabel();
    applySearchPlaceholder();
    refreshGovernorInfo();
    refreshUpdateInfo();
  }

  // ── config (cambiar contraseña + correo de recuperación) ────────────────
  // openConfigModal factorizada (T9, ct-2026-08-05-1137): la alarma de
  // contraseña de fábrica lleva al MISMO modal que ya existe, no arma uno
  // nuevo — reusa exactamente lo que configbtn ya hacía.
  function openConfigModal(focusPassword) {
    document.getElementById("config_current").value = "";
    document.getElementById("config_new").value = "";
    document.getElementById("config_repeat").value = "";
    document.getElementById("config_result").textContent = "";
    document.getElementById("config_email_result").textContent = "";
    refreshGovernorInfo();
    api("/api/admin/recovery-email").then(function (d) {
      document.getElementById("config_email").value = d.email || "";
    }).catch(function () {
      document.getElementById("config_email").value = "";
    });
    // T90 (ct-2026-08-28-1350): valores EFECTIVOS (override guardado, o el
    // default de arranque si el dueño nunca lo tocó desde acá) — mismo
    // patrón que el email de arriba.
    document.getElementById("config_debounce_result").textContent = "";
    api("/api/admin/dispatch-debounce").then(function (d) {
      document.getElementById("config_debounce").value = d.debounce_seconds;
      document.getElementById("config_max_debounce").value = d.max_debounce_seconds;
    }).catch(function () {
      document.getElementById("config_debounce").value = "";
      document.getElementById("config_max_debounce").value = "";
    });
    // T174a: estado real de los reportes de drift, leído del servidor cada
    // vez que se abre Opciones — nunca se asume. Si la lectura falla, el
    // interruptor queda APAGADO: mostrarlo encendido sin saberlo sería
    // decirle al dueño que está mandando algo que quizá no manda.
    document.getElementById("config_drift_result").textContent = "";
    api("/api/admin/drift-reports").then(function (d) {
      document.getElementById("config_drift_enabled").checked = !!d.enabled;
      if (d.url) document.getElementById("config_drift_url").textContent = d.url;
    }).catch(function () {
      document.getElementById("config_drift_enabled").checked = false;
    });
    // T182: estado real de la auto-actualización al abrir, interruptor
    // incluido -- el refresco periódico de más abajo ya no lo toca.
    document.getElementById("config_update_result").textContent = "";
    loadUpdateStatus(true);
    // T153 etapa 1: elección manual guardada (vacío = "nunca elegido",
    // el backend autodetecta) — /api/i18n ya trae el crudo, sin endpoint
    // aparte que duplique effectiveLang.
    document.getElementById("config_language_result").textContent = "";
    loadI18n().then(function () {
      document.getElementById("config_language").value = state.i18n.language || "";
    }).catch(function () {
      document.getElementById("config_language").value = "";
    });
    document.getElementById("configmodal").classList.remove("hidden");
    if (focusPassword) document.getElementById("config_current").focus();
  }
  // Modo privacidad (T85, ct-2026-08-28-0059) — un solo botón, sin modal:
  // el dueño lo prende/apaga en el momento, en vivo, sin fricción antes de
  // salir al aire. updatePrivacyButton() pinta el estado del botón mismo
  // (además de state.privacyMode, ya leído arriba de todo el archivo);
  // llamada una vez acá para reflejar lo que localStorage ya trajo al
  // cargar, y de nuevo en cada click.
  function updatePrivacyButton() {
    document.getElementById("privacybtn").classList.toggle("active", state.privacyMode);
    document.getElementById("privacyeye").classList.toggle("blink", state.privacyMode);
  }
  document.getElementById("privacybtn").addEventListener("click", function () {
    state.privacyMode = !state.privacyMode;
    localStorage.setItem(PRIVACY_KEY, state.privacyMode ? "1" : "0");
    updatePrivacyButton();
    // Repinta todo lo que ya está en pantalla con el estado nuevo — sin
    // esto, prender/apagar el ojito solo afectaría la PRÓXIMA carga, no la
    // vista actual. render() ya encadena renderGroups()/renderContacts()
    // (ver su propio comentario); loadStatus() repinta el hero/status-bar
    // (nombre/número/avatar propios) — mismo patrón "sin F5" que el resto
    // de los toggles del tablero.
    render();
    loadStatus();
    // Agentes y Drafts NO están en la cadena de render() — se pintan una
    // vez al cargar y se refrescan solos cada 15s (setInterval, más abajo),
    // no al togglear. Sin este repintado, un nombre real pintado ANTES del
    // click queda visible hasta el próximo poll — hasta 15s con el botón ya
    // en verde, el mismo "accidente completo" que un parpadeo pero más
    // largo. state.agents ya está cargado — renderAgents() repinta sin ir a
    // red; loadPendingDrafts() sí pide de nuevo, igual que el poll.
    renderAgents();
    loadPendingDrafts().catch(function () {});
    // Un popup de teléfono/grupo ya abierto tiene su propio header pintado
    // en el momento de abrirlo (openPhonePopup/openGroupDetail) — reabrirlo
    // con la misma función de siempre es más simple que duplicar esas 2
    // líneas acá, y ya sabe recalcular todo (mensajes incluidos).
    if (currentPopupJID) {
      if (currentPopupGroup) openGroupDetail(currentPopupGroup);
      else openPhonePopup(currentPopupJID);
    }
  });
  updatePrivacyButton();
  document.getElementById("configbtn").addEventListener("click", function () { openConfigModal(false); });
  document.getElementById("factorypwalert_btn").addEventListener("click", function () { openConfigModal(true); });
  document.getElementById("config_cancel").addEventListener("click", function () {
    document.getElementById("configmodal").classList.add("hidden");
  });
  document.getElementById("config_save").addEventListener("click", function () {
    var result = document.getElementById("config_result");
    var newPass = document.getElementById("config_new").value;
    if (newPass !== document.getElementById("config_repeat").value) {
      result.textContent = t("validation.passwords_mismatch");
      return;
    }
    post("/api/admin/password", {
      current_password: document.getElementById("config_current").value,
      new_password: newPass,
    }).then(function () {
      document.getElementById("configmodal").classList.add("hidden");
      // The server rotated the session secret — every cookie, including
      // this one, is dead now. Show the login overlay right away instead
      // of waiting for the next 15s poll to discover it.
      showLogin();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  document.getElementById("config_email_save").addEventListener("click", function () {
    var result = document.getElementById("config_email_result");
    post("/api/admin/recovery-email", { email: document.getElementById("config_email").value }).then(function () {
      result.textContent = t("action.saved");
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  // T153 2b-i, corregido en T165 (ct-2026-09-16-2016): reaplica TODO el
  // texto de interfaz vía applyAllI18n() — no solo applyI18n(), que se
  // queda corto con lo que arma JS (ver el comentario de applyAllI18n) —
  // más loadStatus()/loadProfileStatus() para el resto del status/badges
  // del hero. Fire-and-forget (catch silencioso): un refresh que falla no
  // debe tapar el "✓ Guardado." que ya se mostró.
  document.getElementById("config_language_save").addEventListener("click", function () {
    var result = document.getElementById("config_language_result");
    post("/api/admin/language", { language: document.getElementById("config_language").value }).then(function () {
      result.textContent = t("action.saved");
      return loadI18n().then(applyAllI18n);
    }).then(function () {
      loadStatus().catch(function () {});
      loadProfileStatus().catch(function () {});
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  // T90 (ct-2026-08-28-1350) — en caliente: el próximo sweep de capipush ya
  // lee el valor nuevo directo de la DB (capipush.isDebounced), sin
  // reiniciar nada del lado del tablero tampoco — post y listo, mismo
  // patrón que el correo de arriba.
  document.getElementById("config_debounce_save").addEventListener("click", function () {
    var result = document.getElementById("config_debounce_result");
    post("/api/admin/dispatch-debounce", {
      debounce_seconds: parseInt(document.getElementById("config_debounce").value, 10) || 0,
      max_debounce_seconds: parseInt(document.getElementById("config_max_debounce").value, 10) || 0,
    }).then(function () {
      result.textContent = t("action.saved");
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  // T174a: encender o apagar los reportes de drift. Aplica en el acto del
  // lado del servidor, sin reiniciar; acá sólo se manda la decisión y se
  // dice si quedó guardada.
  document.getElementById("config_drift_save").addEventListener("click", function () {
    var result = document.getElementById("config_drift_result");
    post("/api/admin/drift-reports", {
      enabled: document.getElementById("config_drift_enabled").checked,
    }).then(function () {
      result.textContent = t("action.saved");
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  // T182: encender o apagar la auto-actualización. Aplica en el acto del
  // lado del servidor, sin reiniciar; acá se manda la decisión, se dice si
  // quedó guardada y se repinta la frase con el estado que contestó -- el
  // POST devuelve el mismo Status que el GET, así que no hace falta pedirlo
  // de nuevo.
  document.getElementById("config_update_save").addEventListener("click", function () {
    var result = document.getElementById("config_update_result");
    post("/api/admin/auto-update", {
      enabled: document.getElementById("config_update_enabled").checked,
    }).then(function (u) {
      state.lastUpdate = u;
      refreshUpdateInfo();
      result.textContent = t("action.saved");
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    });
  });
  // openProfileModal (T112c, ct-2026-09-01-145755): foto + estado de
  // WhatsApp, editables desde el hero en vez de escondidos en Config. El
  // preview de foto reusa buildAvatar contra state.lastStatus — el mismo
  // dato que ya pintó el hero, sin ninguna llamada de red nueva para
  // dibujarse. El estado se pre-llena igual que T96 hacía en config
  // (GET-al-abrir, para no borrar el valor puesto si se guarda sin tocarlo).
  var profilePhotoDataURL = null;
  function openProfileModal() {
    document.getElementById("profile_photo_result").textContent = "";
    document.getElementById("profile_photo_input").value = "";
    profilePhotoDataURL = null;
    var s = state.lastStatus || {};
    document.getElementById("profile_photo_preview").replaceChildren(buildAvatar(s.own_number, s.name, "avatar-lg"));
    document.getElementById("profile_status_result").textContent = "";
    api("/api/admin/profile-status").then(function (d) {
      document.getElementById("profile_status_input").value = d.status || "";
    }).catch(function () {
      document.getElementById("profile_status_input").value = "";
    });
    document.getElementById("profilemodal").classList.remove("hidden");
  }
  document.getElementById("editprofilebtn").addEventListener("click", openProfileModal);
  document.getElementById("profile_cancel").addEventListener("click", function () {
    document.getElementById("profilemodal").classList.add("hidden");
  });
  // profile_photo_input: lee el archivo elegido a un data URL (FileReader,
  // sin subir nada todavía — "guardar solo al confirmar") y lo muestra en
  // el preview. Guardar (abajo) recién ahí lo manda al servidor.
  document.getElementById("profile_photo_input").addEventListener("change", function (e) {
    var file = e.target.files && e.target.files[0];
    var result = document.getElementById("profile_photo_result");
    result.textContent = "";
    if (!file) { profilePhotoDataURL = null; return; }
    var reader = new FileReader();
    reader.onload = function () {
      profilePhotoDataURL = reader.result;
      var preview = document.getElementById("profile_photo_preview");
      preview.className = "avatar avatar-lg";
      var img = document.createElement("img");
      img.alt = "";
      img.src = profilePhotoDataURL;
      preview.replaceChildren(img);
    };
    reader.onerror = function () {
      result.textContent = t("profile.file_read_error");
    };
    reader.readAsDataURL(file);
  });
  // profile_photo_save: sube profilePhotoDataURL (ya en memoria, ningún
  // fetch de red para dibujarse) y, si el servidor confirma, repinta el
  // avatar del HERO con esos mismos bytes — no vuelve a pedir /api/avatar,
  // que sirve desde un caché en disco poblado por un re-check paceado en
  // background (avatar_read.go) y podría devolver todavía la foto vieja.
  // Un fallo deja el preview y el avatar del hero intactos (el valor
  // anterior sigue visible), con el motivo en profile_photo_result.
  document.getElementById("profile_photo_save").addEventListener("click", function () {
    var result = document.getElementById("profile_photo_result");
    var btn = this;
    if (!profilePhotoDataURL) {
      result.textContent = t("profile.choose_image_first");
      return;
    }
    // Subir la foto habla con WhatsApp y puede tardar: MEDIDO, 75 segundos
    // exactos hasta el error cuando el servidor no contesta (es el techo de
    // IQ de whatsmeow). Sin esta señal el dueño aprieta, no pasa nada
    // visible durante ese minuto y cuarto, y concluye que el botón no
    // funciona (reportado así, 2026-09-01). El botón queda deshabilitado
    // mientras tanto para que no se dispare dos veces.
    btn.disabled = true;
    result.textContent = t("profile.saving_photo_notice");
    post("/api/admin/profile-photo", { data_url: profilePhotoDataURL }).then(function () {
      result.textContent = t("action.saved");
      var img = document.createElement("img");
      img.alt = "";
      img.src = profilePhotoDataURL;
      document.getElementById("heroavatar").replaceChildren(img);
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    }).then(function () {
      btn.disabled = false;
    });
  });
  // T92 (ct-2026-08-28) + T96 (ct-2026-08-28-1743), movido al modal de
  // perfil en T112c: guardar refresca el hero con loadProfileStatus() para
  // que se vea el valor nuevo sin recargar la página (el único lugar donde
  // el dueño comprueba "que se vea debajo del nombre" no puede quedar
  // desactualizado hasta el próximo F5 — hallazgo de Citrino).
  document.getElementById("profile_status_save").addEventListener("click", function () {
    var result = document.getElementById("profile_status_result");
    var btn = this;
    // Mismo motivo que el botón de la foto: esto también habla con WhatsApp
    // y puede tardar. Sin señal, se lee como que no hizo nada.
    btn.disabled = true;
    result.textContent = t("action.saving");
    post("/api/admin/profile-status", { status: document.getElementById("profile_status_input").value }).then(function () {
      result.textContent = t("action.saved");
      loadProfileStatus();
    }).catch(function (e) {
      result.textContent = t("error.prefix") + e.message;
    }).then(function () {
      btn.disabled = false;
    });
  });

  // ── Clientes (T172d, ct-2026-11-10-225415) ────────────────────────────
  // Un cliente es UN grupo de WhatsApp: la clave del cliente ES el JID del
  // grupo, y eso es todo el modelo. Los tres demos de T172 dibujaban "un
  // cliente con VARIOS chats, con estado de pago" — el boss descartó ese
  // modelo, así que esta vista se dibujó de nuevo desde el esquema real, no
  // se conectó la de los demos.
  // De dónde sale cada cosa, que son dos dueños distintos:
  //   · las cuotas, el estado y el historial los calcula CleverCoder y
  //     llegan por GET /api/clients (backend T172c) — el tablero NO los
  //     deriva ni los recalcula, solo los muestra;
  //   · el agente que atiende el grupo es ruteo de PIUMY, y se cambia con
  //     el agent-assign que ya existía (T56), con chat_id = la clave del
  //     cliente. Mismo endpoint que la tabla de Conversaciones: no hay
  //     concepto nuevo acá, solo un lugar nuevo desde donde hacerlo.
  var CLIENTS_POLL_MS = 15000;
  // T182: el ritmo del estado de la auto-actualización, con su propio nombre
  // porque es otro renglón y puede cambiar sin arrastrar al de Clientes.
  var UPDATE_POLL_MS = 15000;
  var SVG_NS = "http://www.w3.org/2000/svg";
  var clientsData = { status: "", generatedAt: "", list: [] };

  function loadClients() {
    return api("/api/clients").then(function (d) {
      clientsData = {
        status: (d && d.status) || "",
        generatedAt: (d && d.generated_at) || "",
        list: d && Array.isArray(d.clients) ? d.clients : [],
      };
      renderClients();
    });
  }

  // fmtClientNum / fmtClientMoney: el separador de miles y la coma decimal
  // los pone el navegador con el idioma de la página. No se arman a mano:
  // así es como un tablero en español termina mostrando "1,200" por mil
  // doscientos. Un campo ausente sale "—" y no 0 — la misma distinción que
  // statNumber() ganó en T172a, por la misma razón: un cero real y un dato
  // que no vino no son lo mismo.
  function clientLang() { return state.i18n.lang || "es"; }
  function fmtClientNum(n) {
    if (typeof n !== "number" || !isFinite(n)) return "—";
    return n.toLocaleString(clientLang());
  }
  function fmtClientMoney(n) {
    if (typeof n !== "number" || !isFinite(n)) return "—";
    return n.toLocaleString(clientLang(), { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }
  // fmtClientStamp: hora local legible. Un ISO crudo en pantalla
  // ("2026-11-10T22:45:00Z") es un dato de máquina, no una hora que alguien
  // lea. Una fecha que no se puede interpretar se muestra TAL CUAL vino:
  // el texto raro del archivo dice más que un "Invalid Date".
  function fmtClientStamp(iso) {
    if (!iso) return "—";
    var d = new Date(iso);
    if (isNaN(d.getTime())) return String(iso);
    return d.toLocaleString(clientLang(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
  }
  function fmtClientTime(iso) {
    if (!iso) return "—";
    var d = new Date(iso);
    if (isNaN(d.getTime())) return String(iso);
    return d.toLocaleTimeString(clientLang(), { hour: "2-digit", minute: "2-digit" });
  }
  function clientNum(v) { return typeof v === "number" && isFinite(v) ? v : 0; }

  // clientStateInfo: el estado del cliente, en UN solo lugar — el color del
  // punto y el texto del renglón salen juntos de acá, así no hay forma de
  // que el punto diga una cosa y la letra otra. Quién decide el estado es
  // CleverCoder (el "aviso" es su 80 % del tope), no el tablero.
  function clientStateInfo(cl) {
    if (cl.state === "pausado") {
      return {
        cls: "paused",
        text: cl.paused_until
          ? t("clients.paused_until", { when: fmtClientStamp(cl.paused_until) })
          : t("clients.state_paused"),
      };
    }
    if (cl.state === "aviso") return { cls: "warn", text: t("clients.state_warn") };
    return { cls: "open", text: t("clients.state_open") };
  }

  function clientSection(titleText) {
    var sec = document.createElement("div");
    sec.className = "client-sec";
    if (titleText) {
      var h = document.createElement("div");
      h.className = "client-sec-title";
      h.textContent = titleText;
      sec.appendChild(h);
    }
    return sec;
  }

  // clientQuotaRow: etiqueta, barra y cifra. La barra se pinta por
  // porcentaje del tope con los MISMOS tres colores del estado (verde,
  // ámbar al 80 %, rojo al pasarse), para que la barra y el punto de arriba
  // nunca se contradigan.
  // Sin tope (0, nulo o ausente) NO se dibuja barra: una barra necesita un
  // máximo, y si nadie lo dio habría que inventarlo — un invento en una
  // escala es una mentira con forma de dato. Se muestra solo el consumo.
  function clientQuotaRow(label, used, limit, money) {
    var fmt = money ? fmtClientMoney : fmtClientNum;
    var row = document.createElement("div");
    row.className = "q-row";

    var lab = document.createElement("span");
    lab.className = "q-label";
    lab.textContent = label;
    row.appendChild(lab);

    // Sin tope NO se dibuja ni el riel vacío, solo el hueco de la grilla.
    // Medido en pantalla: un riel gris sin relleno se lee como "0 %" — es
    // decir, lo contrario exacto de "sin tope", que es lo que dice la cifra
    // de al lado. Un elemento vacío mantiene las tres columnas alineadas
    // entre tarjetas sin afirmar nada.
    var hasLimit = typeof limit === "number" && limit > 0;
    var track = document.createElement("div");
    if (hasLimit) {
      track.className = "q-track";
      var pct = Math.max(0, Math.min(100, (clientNum(used) / limit) * 100));
      var fill = document.createElement("div");
      fill.className = "q-fill" + (clientNum(used) >= limit ? " over" : pct >= 80 ? " warn" : "");
      fill.style.width = pct + "%";
      track.appendChild(fill);
    }
    row.appendChild(track);

    var val = document.createElement("span");
    val.className = "q-value";
    val.textContent = hasLimit
      ? t("clients.quota_value", { used: fmt(used), limit: fmt(limit) })
      : fmt(used) + " · " + t("clients.no_limit");
    row.appendChild(val);
    return row;
  }

  // clientMonthRow: los mínimos del mes NO llevan barra a propósito. Son un
  // mínimo a alcanzar, no un tope que se agota: una barra que se llena
  // cuenta esa historia al revés (parecería que se está quedando sin algo).
  // Cumplido se marca con color, que es lo único que hace falta decir.
  function clientMonthRow(label, bucket) {
    var row = document.createElement("div");
    row.className = "m-row";
    var lab = document.createElement("span");
    lab.textContent = label;
    var val = document.createElement("b");
    var used = clientNum(bucket && bucket.used);
    var limit = clientNum(bucket && bucket.limit);
    val.textContent = limit > 0
      ? t("clients.quota_value", { used: fmtClientNum(used), limit: fmtClientNum(limit) })
      : fmtClientNum(bucket && bucket.used);
    if (limit > 0 && used >= limit) val.className = "done";
    row.appendChild(lab);
    row.appendChild(val);
    return row;
  }

  // buildClientChart: las 12 semanas de history en barras, con la línea de
  // tope de la semana de HOY (week.lines.limit — el esquema lo dice
  // explícito: el tope del gráfico es el vigente, no el que hubo en cada
  // semana vieja). SVG a mano: son doce rectángulos y una línea, y una
  // librería de gráficos para eso sería un peso muerto en el binario.
  // La escala es el máximo entre el consumo más alto y el tope, no solo el
  // consumo: si fuera solo el consumo, un cliente muy por debajo de su tope
  // dejaría la línea de tope FUERA del lienzo y el gráfico mentiría por
  // omisión — se vería igual que uno al borde de pasarse.
  // Los colores salen de los tokens por CSS (clases .bar/.limit), nunca de
  // un atributo fill= acá: un fill escrito en el marcado se queda con el
  // color del tema claro cuando el sistema pasa a oscuro.
  var CHART_W = 240;
  var CHART_H = 54;
  function buildClientChart(cl) {
    var hist = Array.isArray(cl.history) ? cl.history : [];
    if (!hist.length) {
      var none = document.createElement("p");
      none.className = "q-note";
      none.textContent = t("clients.chart_empty");
      return none;
    }
    var limit = clientNum(cl.week && cl.week.lines && cl.week.lines.limit);
    var peak = 0;
    hist.forEach(function (w) {
      var v = clientNum(w && w.lines);
      if (v > peak) peak = v;
    });
    var top = Math.max(peak, limit) || 1;

    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "chart");
    svg.setAttribute("viewBox", "0 0 " + CHART_W + " " + CHART_H);
    // preserveAspectRatio=none: el alto lo fija el CSS y el ancho es el de
    // la tarjeta — las barras se estiran a lo ancho, que es exactamente lo
    // que se quiere de un gráfico de barras que llena su caja.
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("role", "img");

    var slot = CHART_W / hist.length;
    var barW = Math.max(2, slot - 3);
    hist.forEach(function (w, i) {
      var lines = clientNum(w && w.lines);
      var h = Math.max(1, (lines / top) * CHART_H);
      var r = document.createElementNS(SVG_NS, "rect");
      r.setAttribute("x", (i * slot).toFixed(2));
      r.setAttribute("y", (CHART_H - h).toFixed(2));
      r.setAttribute("width", barW.toFixed(2));
      r.setAttribute("height", h.toFixed(2));
      var over = limit > 0 && lines >= limit;
      var warn = limit > 0 && !over && lines >= limit * 0.8;
      r.setAttribute("class", "bar" + (over ? " over" : warn ? " warn" : " on"));
      // <title> dentro del <rect>: el navegador lo muestra al pasar el
      // mouse y el lector de pantalla lo lee. Sin esto, doce barras sin
      // cifra son un adorno.
      var tip = document.createElementNS(SVG_NS, "title");
      tip.textContent = t("clients.chart_bar_title", {
        when: fmtClientStamp(w && w.starts_at),
        lines: fmtClientNum(w && w.lines),
        cost: fmtClientMoney(w && w.cost_usd),
      });
      r.appendChild(tip);
      svg.appendChild(r);
    });

    if (limit > 0) {
      var y = (CHART_H - (limit / top) * CHART_H).toFixed(2);
      var ln = document.createElementNS(SVG_NS, "line");
      ln.setAttribute("x1", "0");
      ln.setAttribute("x2", String(CHART_W));
      ln.setAttribute("y1", y);
      ln.setAttribute("y2", y);
      ln.setAttribute("class", "limit");
      svg.appendChild(ln);
    }
    return svg;
  }

  // buildClientAgentControl: el selector del agente principal del grupo.
  // Reusa buildAgentSelect (la MISMA lista de agentes que la tabla de
  // Conversaciones) y el mismo POST /api/admin/agent-assign; chat_id es la
  // clave del cliente, que es el JID del grupo. Sin recargar la página —
  // la vara de siempre del dueño: post y volver a leer en el .then.
  // Si el backend rechaza, el select vuelve a lo que estaba: dejarlo
  // mostrando la opción nueva diría que el cambio se aplicó.
  function buildClientAgentControl(cl) {
    var wrap = document.createElement("div");
    wrap.className = "client-agent";
    var lab = document.createElement("span");
    lab.className = "q-label";
    lab.textContent = t("clients.agent_label");
    wrap.appendChild(lab);

    // chat_jid vacío = Piumy no conoce ese grupo (la clave del cliente no
    // cruzó con ningún chat del store, o no es un grupo). El backend lo dice
    // explícito: asignar un chat que Piumy no tiene NO hace nada. Entonces
    // no se ofrece el control — un desplegable que acepta un cambio que el
    // servidor va a ignorar miente más que no estar.
    if (!cl.chat_jid) {
      var none = document.createElement("span");
      none.className = "client-agent-result";
      none.textContent = t("clients.no_group");
      wrap.appendChild(none);
      return wrap;
    }

    var current = cl.agent_id || "";
    var sel = buildAgentSelect(current);
    var result = document.createElement("span");
    result.className = "client-agent-result";
    sel.onchange = function () {
      result.textContent = t("action.saving");
      post("/api/admin/agent-assign", { chat_id: cl.chat_jid, agent_id: sel.value }).then(function () {
        result.textContent = "";
        loadClients().catch(function () {});
      }).catch(function (e) {
        result.textContent = t("error.prefix") + e.message;
        sel.value = current;
      });
    };
    wrap.appendChild(sel);
    wrap.appendChild(result);
    return wrap;
  }

  function buildClientCard(cl) {
    var card = document.createElement("div");
    card.className = "client";
    var info = clientStateInfo(cl);

    var head = document.createElement("div");
    head.className = "client-head";
    var dot = document.createElement("span");
    dot.className = "client-dot " + info.cls;
    head.appendChild(dot);
    var name = document.createElement("span");
    name.className = "client-name";
    // El nombre del grupo y el nivel son DATO: no pasan por el catálogo
    // (misma línea que los nombres de chat de la etapa 2b-iv del i18n).
    // Van por textContent, nunca innerHTML — un nombre de grupo lo escribe
    // cualquiera de sus miembros y puede traer un "<" adentro.
    name.textContent = cl.name || cl.key || "";
    name.title = cl.name || cl.key || "";
    head.appendChild(name);
    if (cl.tier) {
      var tier = document.createElement("span");
      tier.className = "client-tier";
      tier.textContent = t("clients.tier", { tier: cl.tier });
      head.appendChild(tier);
    }
    card.appendChild(head);

    var st = document.createElement("p");
    st.className = "client-state " + info.cls;
    st.textContent = info.text;
    card.appendChild(st);

    if (cl.reopened) {
      var re = document.createElement("p");
      re.className = "client-state";
      re.textContent = t("clients.reopened_note");
      card.appendChild(re);
    }

    var week = cl.week || {};
    var wsec = clientSection(t("clients.week_title"));
    wsec.appendChild(clientQuotaRow(
      t("clients.lines_label"),
      week.lines && week.lines.used,
      week.lines && week.lines.limit,
      false));
    wsec.appendChild(clientQuotaRow(
      t("clients.cost_label"),
      week.cost_usd && week.cost_usd.used,
      week.cost_usd && week.cost_usd.limit,
      true));
    if (cl.resets_at) {
      var rst = document.createElement("p");
      rst.className = "q-note";
      rst.textContent = t("clients.resets_at", { when: fmtClientStamp(cl.resets_at) });
      wsec.appendChild(rst);
    }
    if (cl.previous_week) {
      var prev = document.createElement("p");
      prev.className = "q-note";
      prev.textContent = t("clients.previous_week", {
        lines: fmtClientNum(cl.previous_week.lines),
        cost: fmtClientMoney(cl.previous_week.cost_usd),
      });
      wsec.appendChild(prev);
    }
    card.appendChild(wsec);

    var month = cl.month || {};
    var msec = clientSection(t("clients.month_title"));
    msec.appendChild(clientMonthRow(t("clients.month_style_global"), month.style_global));
    msec.appendChild(clientMonthRow(t("clients.month_text"), month.text));
    msec.appendChild(clientMonthRow(t("clients.month_style_simple"), month.style_simple));
    card.appendChild(msec);

    var csec = clientSection(t("clients.chart_title"));
    csec.appendChild(buildClientChart(cl));
    // La leyenda del tope acompaña al GRÁFICO, no a la tarjeta: sin
    // historial no hay dibujo, y una leyenda suelta debajo de "sin
    // historial todavía" describe algo que no está en pantalla.
    var hasHistory = Array.isArray(cl.history) && cl.history.length > 0;
    if (hasHistory && clientNum(cl.week && cl.week.lines && cl.week.lines.limit) > 0) {
      var leg = document.createElement("div");
      leg.className = "chart-legend";
      var legLimit = document.createElement("span");
      legLimit.textContent = t("clients.chart_limit", { lines: fmtClientNum(cl.week.lines.limit) });
      leg.appendChild(legLimit);
      csec.appendChild(leg);
    }
    card.appendChild(csec);

    var asec = clientSection("");
    asec.appendChild(buildClientAgentControl(cl));
    card.appendChild(asec);

    return card;
  }

  // renderClients: los CUATRO estados del archivo de CleverCoder, con los
  // textos que fijó el contrato letra por letra. El orden importa:
  //   · missing / unknown_version → no hay nada que interpretar, solo el
  //     aviso. En unknown_version NO se dibuja ni un cliente a propósito:
  //     un schema_version que no conocemos puede haber cambiado el
  //     significado de los campos, y dibujarlos sería inventar;
  //   · stale → el aviso Y los clientes de la última foto, que siguen
  //     siendo el mejor dato que hay;
  //   · empty → "sin clientes todavía", que no es un error.
  function renderClients() {
    var meta = document.getElementById("clientsmeta");
    var list = document.getElementById("clientslist");
    if (!meta || !list) return;
    list.textContent = "";
    meta.className = "clients-meta";

    var st = clientsData.status;
    if (st === "missing" || st === "unknown_version") {
      meta.textContent = st === "missing"
        ? t("clients.status_missing")
        : t("clients.status_unknown_version");
      meta.classList.add("bad");
      return;
    }
    if (st === "stale") {
      meta.textContent = t("clients.status_stale", { when: fmtClientStamp(clientsData.generatedAt) });
      meta.classList.add("warn");
    } else if (st === "empty" || !clientsData.list.length) {
      meta.textContent = t("clients.status_empty");
      return;
    } else {
      meta.textContent = t("clients.meta_fresh", {
        count: clientsData.list.length,
        time: fmtClientTime(clientsData.generatedAt),
      });
    }
    clientsData.list.forEach(function (cl) {
      if (cl && typeof cl === "object") list.appendChild(buildClientCard(cl));
    });
  }

  // refreshClientsIfVisible: el archivo se pide SOLO con la vista a la
  // vista. Refrescar cada 15s una pantalla que nadie está mirando es gasto
  // sin lector; al volver a entrar, el click de la navegación ya pide de
  // nuevo. "Sin F5" se cumple igual: parado en Clientes, la pantalla se
  // actualiza sola.
  function refreshClientsIfVisible() {
    var view = document.getElementById("view-clientes");
    if (!view || view.classList.contains("hidden")) return;
    loadClients().catch(function () {});
  }

  // ── navegación de vistas (T172d) ──────────────────────────────────────
  // Dos vistas: el tablero de siempre (#view-resumen) y Clientes. El
  // switch es el MISMO patrón que las pestañas de Conversaciones de arriba
  // (toggle de .active en el botón, toggle de .hidden en el contenido), para
  // que este tablero tenga un solo modo de cambiar de pantalla y no dos.
  var viewBtns = document.querySelectorAll(".nav-btn");
  viewBtns.forEach(function (btn) {
    btn.addEventListener("click", function () {
      viewBtns.forEach(function (b) { b.classList.remove("active"); });
      btn.classList.add("active");
      var name = btn.getAttribute("data-view");
      document.getElementById("view-resumen").classList.toggle("hidden", name !== "resumen");
      document.getElementById("view-clientes").classList.toggle("hidden", name !== "clientes");
      // Al ENTRAR a Clientes se pide el archivo de nuevo en vez de mostrar
      // la foto que quedó en memoria: el dueño acaba de hacer click, es el
      // momento en que más le importa que el dato sea de ahora.
      if (name === "clientes") loadClients().catch(function () {});
    });
  });

  // T153 2b-i: loadI18n() PRIMERO, no en paralelo — loadStatus() ya usa
  // t() para armar el status/badges (ver su propio código), y si corriera
  // antes de que el catálogo cargara, el primer pintado saldría con
  // claves entre corchetes en vez de texto. Un fallo acá no bloquea nada
  // (catch silencioso): loadStatus() sigue siendo la sonda de auth real,
  // showLogin() sale de ahí si hace falta.
  // T165 (ct-2026-09-16-2016): applyAllI18n(), no applyI18n() a secas — el
  // buscador de Chats (activo por default, ver applySearchForTab(false)
  // más arriba) también arma su placeholder por JS, y sin esto nacía
  // mostrando "[placeholder.search_chats]" literal en CUALQUIER idioma,
  // porque ese primer render corría antes de que este mismo loadI18n()
  // resolviera. Bug real, ya en producción — lo encontró T164 mirando la
  // pantalla, ningún test lo veía.
  loadI18n().then(applyAllI18n).catch(function () {}).then(function () {
    return loadStatus();
  }).then(function () {
    loadChats();
    loadPendingDrafts();
    loadAgents();
    loadOriginDefaults();
    loadProfileStatus();
    connectEvents();
  }).catch(function () {}); // already surfaced via showLogin() inside loadStatus
  setInterval(function () { loadStatus().catch(function () {}); }, 15000);
  setInterval(function () { loadPendingDrafts().catch(function () {}); }, 15000);
  setInterval(function () { loadAgents().catch(function () {}); }, 15000);
  // T172d: el mismo ritmo de 15s que los demás loaders, pero solo cuando la
  // vista Clientes está abierta (refreshClientsIfVisible).
  setInterval(refreshClientsIfVisible, CLIENTS_POLL_MS);
  // T182: el mismo ritmo, solo con Opciones abierto (refreshUpdateIfVisible).
  setInterval(refreshUpdateIfVisible, UPDATE_POLL_MS);
  armSSEWatchdog();
  // T172a: un tick por segundo para el renglón de frescura. Es sólo pintar
  // texto que ya está en memoria — ni un fetch más que antes.
  setInterval(renderFreshness, 1000);
})();
