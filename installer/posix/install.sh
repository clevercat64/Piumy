#!/bin/sh
# Piumy — instalador para Linux y macOS (T113, ct-2026-09-01-1820).
#
# Hace lo que installer/windows/piumy.iss hace en Windows, sin administrador:
# deja el programa y los datos en una sola carpeta (la que ya decide
# config.DataDir), genera las 3 claves en la primera instalación y lo deja
# arrancando al iniciar sesión (systemd --user en Linux, launchd en macOS).
#
# Correrlo otra vez ACTUALIZA: reemplaza el programa y nada más. Las claves
# (piumy-config.json) y los datos (secrets/: historial, reglas y la sesión de
# WhatsApp) no se tocan nunca; ante cualquier duda sobre las claves existentes
# se detiene antes de cambiar algo, igual que el instalador de Windows.
#
# Uso, desde la carpeta del paquete ya extraída:   sh install.sh
# Sin preguntas (ej. un script de despliegue):
#   PIUMY_DASHBOARD_PASSWORD='...' sh install.sh
#
# PROBADO solo con install_test.sh (systemctl, launchctl, uname y curl
# simulados, en Windows con Git Bash). NO probado en un Linux ni un Mac reales;
# se marca "no probado" abajo en cada punto que solo una máquina real confirma.
set -eu
umask 077 # los datos y las claves quedan legibles solo para este usuario

say()  { printf '%s\n' "$*"; }
warn() { printf 'AVISO: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
interactive() { [ -t 0 ]; }

SERVICE=piumy             # unidad de systemd
LABEL=app.piumy.gateway   # etiqueta de launchd

[ "$(id -u)" -ne 0 ] || die "no ejecutes este instalador con sudo ni como root: Piumy se instala para tu usuario, y los datos irían a la carpeta de root."
[ -n "${HOME:-}" ] || die "HOME no está definido: no se puede resolver la carpeta de datos."

# DATA repite config.baseDir (internal/config/datadir.go) y tiene que seguir
# igual: el binario resuelve su carpeta de datos él mismo. Linux NO respeta
# XDG_DATA_HOME porque el binario tampoco (usa ~/.local/share/piumy a secas).
case "$(uname -s)" in
	Linux)  OS=linux;  DATA="$HOME/.local/share/piumy" ;;
	Darwin) OS=darwin; DATA="$HOME/Library/Application Support/Piumy" ;;
	*) die "sistema no soportado: $(uname -s). Este instalador es para Linux y macOS (en Windows se usa Piumy-Setup-<versión>.exe)." ;;
esac

HERE=$(cd "$(dirname "$0")" && pwd)
SRC_BIN="$HERE/piumy-gateway"
BIN="$DATA/piumy-gateway"
# Junto al binario, no en otra carpeta: config.ApplyFileDefaults lo busca ahí.
CONFIG="$DATA/piumy-config.json"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

[ -f "$SRC_BIN" ] || die "no encuentro piumy-gateway junto a install.sh. Ejecuta el instalador desde la carpeta del paquete ya extraído."

DASH_PASSWORD=""
SEEDED=0

# Se limpia al salir por cualquier camino: archivos a medio escribir, la clave
# del tablero en el entorno del gestor de servicios, y el eco de la terminal si
# se cancela con Ctrl-C mientras se pide la clave.
cleanup() {
	rm -f "$BIN.new" "$CONFIG.tmp"
	unseed_password
	if interactive; then stty echo 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM

# ── Entorno ──────────────────────────────────────────────────────────────────

require_systemd_session() {
	[ "$OS" = linux ] || return 0 # macOS siempre trae launchctl
	if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then return 0; fi
	die "no hay una sesión de systemd de usuario (systemctl --user). Entra con tu usuario de forma normal (no con su/sudo, y desde una sesión gráfica o por SSH). Este instalador solo cubre sistemas con systemd: con otro gestor de servicios (OpenRC, runit...) todavía no hay una instalación soportada."
}

# ── Condiciones de uso (espeja LicenseFile=eula.txt del instalador de Windows) ──

accept_eula() {
	interactive || return 0 # desatendido: acepta, igual que /VERYSILENT en Windows
	[ -f "$HERE/eula.txt" ] || die "falta eula.txt junto a install.sh."
	cat "$HERE/eula.txt"
	printf '\n¿Aceptas estas condiciones? Escribe "si" para continuar: '
	read -r answer || answer=""
	case $answer in
		si|sí|Si|Sí|SI|s|S|y|Y|yes) ;;
		*) die "no se aceptaron las condiciones: no se instaló nada." ;;
	esac
}

# ── Claves: se conservan, nunca se regeneran sobre una instalación existente ──

# Valor de un par "CLAVE": "valor" de piumy-config.json. Misma forma fija (un
# par por línea) que escribe write_config y el instalador de Windows; un
# archivo en otro formato no se reconoce y la instalación se detiene.
config_value() {
	sed -n 's/^[[:space:]]*"'"$1"'"[[:space:]]*:[[:space:]]*"\(.*\)"[[:space:]]*,\{0,1\}[[:space:]]*$/\1/p' "$CONFIG" | tail -n 1
}

# Deja FRESH=1 si es una instalación nueva. Si ya hay piumy-config.json exige
# que se lean las 3 claves: generar otras dejaría ilegibles para siempre los
# respaldos cifrados con las de hoy (misma regla que FinalizeKeys en el .iss).
inspect_existing_install() {
	FRESH=1
	[ -e "$CONFIG" ] || return 0
	FRESH=0
	[ -r "$CONFIG" ] || die "ya existe una instalación ($CONFIG) pero no se puede leer. La instalación se detiene para no arriesgar las claves existentes."
	for key in PIUMY_MCP_KEY PIUMY_REST_KEY PIUMY_BACKUP_KEY; do
		[ -n "$(config_value "$key")" ] || die "ya existe una instalación ($CONFIG) pero no se pudo leer $key: el archivo puede estar dañado o en otro formato. La instalación se detiene para no generar claves nuevas que reemplacen las que ya protegen tus respaldos. Revisa el archivo a mano, o si de verdad quieres claves nuevas, bórralo antes de reinstalar."
	done
}

random_hex() { # $1 = bytes
	hex=$(od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n')
	[ "${#hex}" -eq $(($1 * 2)) ] || die "no se pudo generar una clave aleatoria."
	printf '%s' "$hex"
}

# Mismos largos que el instalador de Windows (MCP 16 bytes, REST 24, respaldo
# 32). Sin rutas: el binario ya decide dónde van sus datos bajo $DATA/secrets,
# y escribirlas acá solo duplicaría esa decisión.
write_config() {
	mcp=$(random_hex 16)
	rest=$(random_hex 24)
	backup=$(random_hex 32)
	printf '{\n  "PIUMY_MCP_KEY": "%s",\n  "PIUMY_REST_KEY": "%s",\n  "PIUMY_BACKUP_KEY": "%s"\n}\n' \
		"$mcp" "$rest" "$backup" > "$CONFIG.tmp"
	mv "$CONFIG.tmp" "$CONFIG"
}

# ── Clave del tablero: solo en instalación nueva (passHash es seed-only) ──────

read_secret() { # deja la línea leída, sin eco, en SECRET
	stty -echo 2>/dev/null || true
	read -r SECRET || die "no se pudo leer la clave."
	stty echo 2>/dev/null || true
	printf '\n'
}

choose_dashboard_password() {
	if [ -n "${PIUMY_DASHBOARD_PASSWORD:-}" ]; then
		[ "${#PIUMY_DASHBOARD_PASSWORD}" -ge 4 ] || die "PIUMY_DASHBOARD_PASSWORD tiene que tener al menos 4 caracteres."
		DASH_PASSWORD=$PIUMY_DASHBOARD_PASSWORD
	elif interactive; then
		while :; do
			printf 'Clave para entrar al tablero (mínimo 4 caracteres): '
			read_secret
			first=$SECRET
			printf 'Repite la clave: '
			read_secret
			if [ "${#first}" -lt 4 ]; then
				say "La clave tiene que tener al menos 4 caracteres."
			elif [ "$first" != "$SECRET" ]; then
				say "Las dos claves no coinciden."
			else
				DASH_PASSWORD=$first
				break
			fi
		done
	else
		# Sin clave, el binario siembra la de fábrica (admin/piumy): el tablero
		# lo avisa en pantalla hasta que se cambie.
		warn "instalación sin PIUMY_DASHBOARD_PASSWORD y sin terminal: el tablero queda con la clave de fábrica (admin/piumy). Cámbiala desde el tablero apenas puedas."
	fi
}

# La clave viaja en el entorno del gestor de servicios solo durante el primer
# arranque (el binario la lee una sola vez y solo si aún no hay una guardada);
# nunca se escribe a disco.
seed_password() {
	[ -n "$DASH_PASSWORD" ] || return 0
	SEEDED=1
	case $OS in
		linux)  systemctl --user set-environment "PIUMY_DASHBOARD_PASSWORD=$DASH_PASSWORD" ;;
		darwin) launchctl setenv PIUMY_DASHBOARD_PASSWORD "$DASH_PASSWORD" ;;
	esac
}

unseed_password() {
	[ "$SEEDED" -eq 1 ] || return 0
	SEEDED=0
	case $OS in
		linux)  systemctl --user unset-environment PIUMY_DASHBOARD_PASSWORD 2>/dev/null || true ;;
		darwin) launchctl unsetenv PIUMY_DASHBOARD_PASSWORD 2>/dev/null || true ;;
	esac
}

# ── Programa ─────────────────────────────────────────────────────────────────

# Equivale a CloseRunningInstance del .iss. Un Piumy lanzado a mano (fuera del
# servicio) no se detecta: el programa nuevo queda en disco y ese proceso sigue
# con el viejo hasta que se cierre y se vuelva a abrir.
stop_service() {
	case $OS in
		linux)  systemctl --user stop "$SERVICE" 2>/dev/null || true ;;
		darwin) launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true ;;
	esac
}

install_program() {
	# Solo logs/ (launchd no la crea); el binario crea lo suyo bajo secrets/.
	mkdir -p "$DATA/logs"
	cp "$SRC_BIN" "$BIN.new"
	chmod 755 "$BIN.new"
	mv -f "$BIN.new" "$BIN" # rename: un proceso viejo sigue con su inodo, sin "text file busy"
	# Equivale a ExeWasReplaced del .iss: ¿el que quedó es el del paquete?
	cmp -s "$SRC_BIN" "$BIN" || die "el programa instalado no coincide con el del paquete (¿disco lleno o permisos?). Vuelve a ejecutar este instalador."
	if [ "$OS" = darwin ]; then
		# Un binario sin firmar bajado del navegador queda en cuarentena y
		# Gatekeeper no lo deja correr. No probado en un Mac real.
		xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true
	fi
}

# ── Arranque al iniciar sesión ───────────────────────────────────────────────

write_systemd_unit() {
	mkdir -p "$UNIT_DIR"
	cat > "$UNIT_DIR/$SERVICE.service" <<EOF
[Unit]
Description=Piumy: tu WhatsApp atendido por tus agentes

[Service]
ExecStart="$BIN"
WorkingDirectory=$DATA
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
}

write_launchd_plist() {
	mkdir -p "$(dirname "$PLIST")"
	cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key><array><string>$BIN</string></array>
	<key>WorkingDirectory</key><string>$DATA</string>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>StandardOutPath</key><string>$DATA/logs/launchd.log</string>
	<key>StandardErrorPath</key><string>$DATA/logs/launchd.log</string>
</dict>
</plist>
EOF
}

# El archivo del servicio se reescribe entero en cada corrida (no guarda
# claves ni datos, solo dónde está el programa): es lo que hace al instalador
# idempotente sin comparar versiones.
register_and_start_service() {
	case $OS in
	linux)
		write_systemd_unit
		systemctl --user daemon-reload
		systemctl --user enable "$SERVICE"
		seed_password
		systemctl --user restart "$SERVICE"
		;;
	darwin)
		write_launchd_plist
		seed_password
		launchctl bootstrap "gui/$(id -u)" "$PLIST" # RunAtLoad lo arranca
		;;
	esac
	unseed_password
}

# Default de config.go (restAddrDefault); PIUMY_REST_ADDR de piumy-config.json
# lo cambia. Misma cuenta que LaunchFirstRunAndOpenDashboard del .iss.
dashboard_url() {
	rest_addr=""
	if [ "$FRESH" -eq 0 ]; then rest_addr=$(config_value PIUMY_REST_ADDR); fi
	case $rest_addr in
		"")  hostport=127.0.0.1:8092 ;;
		:*)  hostport=127.0.0.1$rest_addr ;;
		*)   hostport=$rest_addr ;;
	esac
	printf 'http://%s/dashboard/' "$hostport"
}

# Cualquier respuesta HTTP (aun un redirect o un 401) prueba que Piumy escucha;
# es mejor señal que "el proceso existe" y funciona igual en los dos sistemas.
wait_for_dashboard() {
	command -v curl >/dev/null 2>&1 || return 0 # sin curl no hay cómo comprobarlo
	n=0
	while [ "$n" -lt 20 ]; do
		if curl -s -o /dev/null --max-time 2 "$DASH_URL"; then return 0; fi
		n=$((n + 1))
		sleep 1
	done
	return 1
}

# ── Programa principal ───────────────────────────────────────────────────────

require_systemd_session
accept_eula
inspect_existing_install # antes de tocar nada: si las claves no cierran, se corta acá
if [ "$FRESH" -eq 1 ]; then choose_dashboard_password; fi

stop_service
install_program
if [ "$FRESH" -eq 1 ]; then write_config; fi
register_and_start_service

DASH_URL=$(dashboard_url)
if ! wait_for_dashboard; then
	case $OS in
		linux)  extra_log="«journalctl --user -u $SERVICE»" ;;
		darwin) extra_log="$DATA/logs/launchd.log" ;;
	esac
	die "Piumy se instaló pero no responde en $DASH_URL después de 20 segundos. Mira su log: $DATA/logs/piumy.log y $extra_log."
fi

if [ "$FRESH" -eq 1 ]; then say "Piumy quedó instalado y corriendo."; else say "Piumy quedó actualizado y corriendo."; fi
say "  Programa: $BIN"
say "  Datos:    $DATA   (secrets/ guarda el historial, las reglas y la sesión de WhatsApp: se conserva al actualizar)"
say "  Tablero:  $DASH_URL"
say ""
say "Siguiente paso: abre el tablero y usa «Conectar QR» para vincular tu WhatsApp."
if [ "$OS" = linux ]; then
	say "Para que arranque al encender el equipo sin iniciar sesión (ej. un servidor o una Raspberry): loginctl enable-linger $(id -un)"
fi
