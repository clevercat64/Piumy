#!/bin/sh
# Pruebas de install.sh (T113, ct-2026-09-01-1820). Correr a mano:
#   sh installer/posix/install_test.sh
# `go test` NO lo corre: es shell, no Go.
#
# Qué prueba: las ramas de install.sh que cuidan lo irreversible — instalación
# nueva, actualización sin pisar claves ni sesión, abortar ante claves dudosas,
# la clave del tablero que nunca llega a disco — en una carpeta HOME
# temporal, con systemctl/launchctl/uname/curl simulados (llevan un registro
# de cómo los llamó el instalador).
# Qué NO prueba, porque necesita una máquina real: que systemd o launchd
# acepten de verdad el archivo de servicio, las preguntas interactivas (EULA y
# clave: piden una terminal) y el arranque del binario real.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
INSTALL="$HERE/install.sh"
# Qué shell ejecuta install.sh (por defecto, el `sh` del sistema). Probar con
# dash —el `sh` de Debian, Ubuntu y Raspberry Pi OS— y con bash:
#   SHELL_UNDER_TEST=dash sh install_test.sh
SHELL_UNDER_TEST=${SHELL_UNDER_TEST:-sh}
WORK=$(mktemp -d)
FINISHED=0
# Si el arnés muere antes del resumen (set -e), que lo diga: un arnés mudo
# parece "no hubo fallas".
trap 'rm -rf "$WORK"; [ "$FINISHED" -eq 1 ] || echo "FALLA el arnés terminó antes del resumen: mira el último error de arriba" >&2' EXIT
PASS=0
FAIL=0

ok()   { PASS=$((PASS + 1)); printf '  ok   %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  FALLA %s\n' "$1"; }
check() { # check "descripción" comando...  (pasa si el comando sale 0)
	desc=$1; shift
	if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi
}

# Una "máquina" nueva: HOME vacío, paquete con un programa falso, y stubs.
# $1 = linux|darwin.  Deja HOME, PKG, STUBS y CALLS.
new_machine() {
	M="$WORK/$1-$2"; rm -rf "$M"
	HOME_DIR="$M/home"; PKG="$M/pkg"; STUBS="$M/stubs"; CALLS="$M/calls.log"
	mkdir -p "$HOME_DIR" "$PKG" "$STUBS"
	: > "$CALLS"
	printf 'programa v1\n' > "$PKG/piumy-gateway"
	for tool in systemctl launchctl; do
		printf '#!/bin/sh\nprintf "%s %%s\\n" "$*" >> "%s"\nexit 0\n' "$tool" "$CALLS" > "$STUBS/$tool"
	done
	printf '#!/bin/sh\necho %s\n' "$([ "$1" = linux ] && echo Linux || echo Darwin)" > "$STUBS/uname"
	printf '#!/bin/sh\nexit 0\n' > "$STUBS/curl"
	printf '#!/bin/sh\nexit 0\n' > "$STUBS/xattr"
	chmod +x "$STUBS"/*
	cp "$INSTALL" "$PKG/install.sh"
}

# run_install <env...> — corre install.sh de $PKG sin terminal; deja la salida en OUT y el código en RC.
run_install() {
	set +e
	OUT=$(env HOME="$HOME_DIR" PATH="$STUBS:$PATH" XDG_CONFIG_HOME= "$@" "$SHELL_UNDER_TEST" "$PKG/install.sh" </dev/null 2>&1)
	RC=$?
	set -e
}

data_dir() { if [ "$1" = linux ]; then echo "$HOME_DIR/.local/share/piumy"; else echo "$HOME_DIR/Library/Application Support/Piumy"; fi; }
hexkeys_ok() { # las 3 claves existen con los largos de Windows (32/48/64 hex)
	grep -Eq '"PIUMY_MCP_KEY": "[0-9a-f]{32}"' "$1" &&
	grep -Eq '"PIUMY_REST_KEY": "[0-9a-f]{48}"' "$1" &&
	grep -Eq '"PIUMY_BACKUP_KEY": "[0-9a-f]{64}"' "$1"
}
# ¿este sistema de archivos respeta chmod? (Windows/NTFS no: sin esto el
# chequeo de permisos mediría el sistema de archivos, no al instalador)
canary="$WORK/canary"; : > "$canary"; chmod 600 "$canary"
if [ "$(stat -c %a "$canary" 2>/dev/null || stat -f %Lp "$canary")" = 600 ]; then MODES=1; else MODES=0; fi

# Instrumento: un caso cuyo resultado ya se sabe. Un paquete SIN el programa
# tiene que fallar, y decir por qué; si esto sale "ok" sin que el instalador
# haya corrido de verdad, el arnés está roto y nada de lo de abajo vale.
echo "== instrumento =="
new_machine linux known-bad
rm "$PKG/piumy-gateway"
run_install PIUMY_DASHBOARD_PASSWORD=abcd
check "paquete sin programa: falla (código distinto de 0)"  [ "$RC" -ne 0 ]
check "paquete sin programa: dice que falta piumy-gateway"  sh -c "printf '%s' \"\$1\" | grep -q 'no encuentro piumy-gateway'" _ "$OUT"
check "paquete sin programa: no instaló nada"               [ ! -e "$(data_dir linux)" ]

echo "== Linux, instalación nueva =="
new_machine linux fresh
run_install PIUMY_DASHBOARD_PASSWORD=pw-de-prueba-555
D=$(data_dir linux)
check "sale con código 0"                              [ "$RC" -eq 0 ]
check "deja el programa en la carpeta de datos"        [ -f "$D/piumy-gateway" ]
check "es el mismo programa del paquete"               cmp -s "$PKG/piumy-gateway" "$D/piumy-gateway"
check "crea la carpeta de logs (launchd no la crea)"   [ -d "$D/logs" ]
check "no crea ni toca secrets/ (es del programa)"     [ ! -e "$D/secrets" ]
check "genera las 3 claves con el largo de Windows"    hexkeys_ok "$D/piumy-config.json"
check "no escribe rutas en piumy-config.json"          sh -c "! grep -q _PATH '$D/piumy-config.json'"
check "escribe la unidad systemd con el programa"      grep -q "^ExecStart=\"$D/piumy-gateway\"" "$HOME_DIR/.config/systemd/user/piumy.service"
if [ "$MODES" -eq 1 ]; then
	check "piumy-config.json queda 600"                [ "$(stat -c %a "$D/piumy-config.json")" = 600 ]
else
	printf '  --   permisos 600: no medible en este sistema de archivos (chmod no se respeta)\n'
fi
# orden: daemon-reload, enable, set-environment, restart, unset-environment
order=$(grep -n 'daemon-reload\|enable \|set-environment PIUMY_DASH\|restart \|unset-environment' "$CALLS" | sed 's/^[0-9]*://' | tr '\n' '|')
check "orden de llamadas: reload, enable, set-env, restart, unset-env" \
	[ "$order" = "systemctl --user daemon-reload|systemctl --user enable piumy|systemctl --user set-environment PIUMY_DASHBOARD_PASSWORD=pw-de-prueba-555|systemctl --user restart piumy|systemctl --user unset-environment PIUMY_DASHBOARD_PASSWORD|" ]
check "la clave del tablero NO queda en ningún archivo" sh -c "! grep -rq 'pw-de-prueba-555' '$HOME_DIR'"
check "imprime la URL del tablero por defecto"         sh -c "printf '%s' \"\$1\" | grep -q 'http://127.0.0.1:8092/dashboard/'" _ "$OUT"

echo "== Linux, actualización (segunda corrida) =="
mkdir -p "$D/secrets" # lo crea el programa real al arrancar, no el instalador
printf 'sesion-whatsapp-555\n' > "$D/secrets/whatsmeow.db"
printf 'historial-555\n'       > "$D/secrets/piumy.db"
printf 'programa v2\n' > "$PKG/piumy-gateway"
before_cfg=$(cksum < "$D/piumy-config.json"); before_wa=$(cksum < "$D/secrets/whatsmeow.db"); before_db=$(cksum < "$D/secrets/piumy.db")
: > "$CALLS"
run_install
check "sale con código 0"                              [ "$RC" -eq 0 ]
check "las claves quedan idénticas (byte a byte)"      [ "$before_cfg" = "$(cksum < "$D/piumy-config.json")" ]
check "la sesión de WhatsApp queda idéntica"           [ "$before_wa" = "$(cksum < "$D/secrets/whatsmeow.db")" ]
check "la base de datos queda idéntica"                [ "$before_db" = "$(cksum < "$D/secrets/piumy.db")" ]
check "el programa se reemplazó por el nuevo"          cmp -s "$PKG/piumy-gateway" "$D/piumy-gateway"
check "detuvo el servicio antes de reemplazar"         grep -q 'systemctl --user stop piumy' "$CALLS"
check "reinició el servicio"                           grep -q 'systemctl --user restart piumy' "$CALLS"
check "no sembró ninguna clave de tablero"             sh -c "! grep -q set-environment '$CALLS'"
check "dice que actualizó"                             sh -c "printf '%s' \"\$1\" | grep -q 'actualizado'" _ "$OUT"
run_install
check "una tercera corrida sigue sin tocar las claves" [ "$before_cfg" = "$(cksum < "$D/piumy-config.json")" ]

echo "== Linux, claves existentes dudosas: se detiene SIN cambiar nada =="
new_machine linux broken
D=$(data_dir linux); mkdir -p "$D"
printf '{\n  "PIUMY_MCP_KEY": "aaa",\n  "PIUMY_REST_KEY": "bbb"\n}\n' > "$D/piumy-config.json"   # falta la de respaldo
printf 'programa viejo\n' > "$D/piumy-gateway"
before_cfg=$(cksum < "$D/piumy-config.json"); before_bin=$(cksum < "$D/piumy-gateway")
run_install
check "sale con error"                                 [ "$RC" -ne 0 ]
check "dice cuál clave no pudo leer"                   sh -c "printf '%s' \"\$1\" | grep -q 'PIUMY_BACKUP_KEY'" _ "$OUT"
check "no toca piumy-config.json"                      [ "$before_cfg" = "$(cksum < "$D/piumy-config.json")" ]
check "no reemplaza el programa"                       [ "$before_bin" = "$(cksum < "$D/piumy-gateway")" ]
check "no detuvo ni arrancó el servicio"               sh -c "! grep -q 'stop\|restart\|enable' '$CALLS'"

echo "== Linux, copia del programa corrupta: se detiene antes de arrancar =="
new_machine linux badcopy
printf '#!/bin/sh
printf "corrupto\n" > "$2"
' > "$STUBS/cp"; chmod +x "$STUBS/cp"
run_install PIUMY_DASHBOARD_PASSWORD=abcd
check "sale con error"                                 [ "$RC" -ne 0 ]
check "dice que el programa no coincide"               sh -c "printf '%s' \"\$1\" | grep -q 'no coincide'" _ "$OUT"
check "no arrancó el servicio"                         sh -c "! grep -q restart '$CALLS'"

echo "== Clave del tablero =="
new_machine linux shortpw
run_install PIUMY_DASHBOARD_PASSWORD=abc
check "una clave de 3 caracteres aborta"               [ "$RC" -ne 0 ]
check "y no instala nada"                              [ ! -e "$(data_dir linux)/piumy-gateway" ]
new_machine linux nopw
run_install
check "sin clave ni terminal: avisa la de fábrica"     sh -c "printf '%s' \"\$1\" | grep -q 'admin/piumy'" _ "$OUT"
check "y no siembra nada en el gestor de servicios"    sh -c "! grep -q set-environment '$CALLS'"
check "igual instala (sale 0)"                         [ "$RC" -eq 0 ]

echo "== Puerto del tablero cambiado a mano =="
new_machine linux port
D=$(data_dir linux); mkdir -p "$D"
printf '{\n  "PIUMY_MCP_KEY": "a",\n  "PIUMY_REST_KEY": "b",\n  "PIUMY_BACKUP_KEY": "c",\n  "PIUMY_REST_ADDR": ":9000"\n}\n' > "$D/piumy-config.json"
run_install
check "usa el puerto de piumy-config.json"             sh -c "printf '%s' \"\$1\" | grep -q 'http://127.0.0.1:9000/dashboard/'" _ "$OUT"

echo "== macOS, instalación nueva (ruta con espacio) =="
new_machine darwin fresh
run_install PIUMY_DASHBOARD_PASSWORD=pw-de-prueba-555
D=$(data_dir darwin)
check "sale con código 0"                              [ "$RC" -eq 0 ]
check "datos en Library/Application Support/Piumy"     [ -f "$D/piumy-gateway" ]
check "genera las 3 claves"                            hexkeys_ok "$D/piumy-config.json"
check "escribe el plist con el programa"               grep -q "<string>$D/piumy-gateway</string>" "$HOME_DIR/Library/LaunchAgents/app.piumy.gateway.plist"
check "el plist arranca al iniciar sesión"             grep -q '<key>RunAtLoad</key><true/>' "$HOME_DIR/Library/LaunchAgents/app.piumy.gateway.plist"
order=$(grep -n 'setenv PIUMY_DASH\|bootstrap\|unsetenv' "$CALLS" | sed 's/^[0-9]*://' | tr '\n' '|')
check "orden: setenv, bootstrap, unsetenv"             [ "$order" = "launchctl setenv PIUMY_DASHBOARD_PASSWORD pw-de-prueba-555|launchctl bootstrap gui/$(id -u) $HOME_DIR/Library/LaunchAgents/app.piumy.gateway.plist|launchctl unsetenv PIUMY_DASHBOARD_PASSWORD|" ]
check "la clave del tablero NO queda en ningún archivo" sh -c "! grep -rq 'pw-de-prueba-555' '$HOME_DIR'"
run_install
check "macOS: la segunda corrida no pisa las claves"   hexkeys_ok "$D/piumy-config.json"

echo "== Sistema no soportado y root =="
new_machine linux other
printf '#!/bin/sh\necho FreeBSD\n' > "$STUBS/uname"
run_install
check "FreeBSD: se niega"                              [ "$RC" -ne 0 ]
check "FreeBSD: con un mensaje claro"                  sh -c "printf '%s' \"\$1\" | grep -q 'no soportado'" _ "$OUT"
new_machine linux root
printf '#!/bin/sh\nif [ "$1" = -u ]; then echo 0; else /usr/bin/id "$@"; fi\n' > "$STUBS/id"; chmod +x "$STUBS/id"
run_install
check "como root se niega"                             [ "$RC" -ne 0 ]
check "y dice por qué"                                  sh -c "printf '%s' \"\$1\" | grep -q 'root'" _ "$OUT"

echo
echo "$PASS ok, $FAIL con falla"
FINISHED=1
[ "$FAIL" -eq 0 ]
