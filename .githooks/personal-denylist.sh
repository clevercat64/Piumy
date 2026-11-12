# Freno local de identificadores reales conocidos (T207, ct-2026-11-11-1948),
# compartido por pre-commit y commit-msg. `. este/archivo.sh` desde sh.
#
# Los 4 patrones de personal-data-patterns.sh reconocen FORMAS (un JID, un
# telefono). No pueden reconocer un nombre de persona, un grupo o un fragmento
# de numero que ya se sabe real: ese dato solo lo conoce quien lo tiene. Para
# eso, una lista LOCAL, fuera del repo y nunca versionada, con esos valores.
#
# Donde esta la lista (la primera que exista; si no hay ninguna, este freno no
# hace nada y el commit sigue su camino):
#   1. $PIUMY_PERSONAL_DENYLIST
#   2. .git/info/personal-denylist del repositorio
#
# Formato: una entrada por linea, texto literal (no regex), sin distinguir
# mayusculas. Se ignoran las lineas vacias, las que empiezan con # y las de
# menos de 4 caracteres (una entrada corta coincidiria con medio repo y
# bloquearia todo commit). Ejemplo: personal-denylist.example.
#
# Deja en $denylist_tmp un archivo temporal con las entradas validas (vacio o
# inexistente si no hay lista) para usarlo con `grep -F -i -f`. Los hooks
# nunca imprimen el valor que coincidio, solo el archivo: el mensaje de un
# hook tambien llega a terminales y registros.
denylist_src="${PIUMY_PERSONAL_DENYLIST:-$(git rev-parse --git-path info/personal-denylist 2>/dev/null)}"
denylist_tmp=""
if [ -n "$denylist_src" ] && [ -f "$denylist_src" ]; then
  denylist_tmp=$(mktemp) && trap 'rm -f "$denylist_tmp"' EXIT
  # tr -d '\r': una lista editada en Windows traeria CRLF y ninguna entrada coincidiria
  tr -d '\r' < "$denylist_src" | awk 'length($0) >= 4 && substr($0, 1, 1) != "#"' > "$denylist_tmp"
fi
