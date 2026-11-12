#!/bin/sh
# Prueba del freno local de identificadores reales (T207): sh .githooks/test-personal-denylist.sh
# Repo temporal; llama a pre-commit y commit-msg de ESTE directorio. Los valores son inventados.
# Sale 1 si algun caso no da lo esperado. El caso 2 es el control: si el freno no frena, este
# test tiene que fallar, no pasar en silencio.
here="$(cd "$(dirname "$0")" && pwd)"
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
cd "$tmp" && git init -q . && git config user.email t@example.invalid && git config user.name t \
  && git config core.autocrlf false || exit 1
unset PIUMY_PERSONAL_DENYLIST
fails=0

# caso <nombre> <rc esperado> <rc real> [salida que NO debe contener el valor]
caso() {
  if [ "$2" -ne "$3" ]; then echo "FALLA: $1 (esperaba rc=$2, dio rc=$3)"; fails=$((fails + 1))
  else echo "ok:    $1"; fi
}
precommit() { sh "$here/pre-commit" >"$tmp/out" 2>&1; echo $?; }
mensaje()   { printf '%s\n' "$1" >"$tmp/msg"; sh "$here/commit-msg" "$tmp/msg" >"$tmp/out" 2>&1; echo $?; }
stage()     { mkdir -p "$tmp/r" && printf '%s\n' "$1" >"$tmp/r/f.txt" && git add -f "$tmp/r/f.txt"; }

DENY="$tmp/.git/info/personal-denylist"
mkdir -p "$tmp/.git/info"

# 1. sin lista no frena nada (un clon nuevo no tiene lista)
stage "hola Fulano Inventado"
caso "1. sin lista, con el nombre en el cambio" 0 "$(precommit)"

# 2. CONTROL: con la lista y la entrada en una linea agregada, frena -- y no dice cual
printf 'Fulano Inventado\n' >"$DENY"
caso "2. con lista, la entrada en el cambio" 1 "$(precommit)"
if grep -qi "fulano" "$tmp/out"; then echo "FALLA: 2b. el hook imprimio el valor"; fails=$((fails + 1)); else echo "ok:    2b. no imprime el valor"; fi
grep -q "f.txt" "$tmp/out" || { echo "FALLA: 2c. no nombra el archivo"; fails=$((fails + 1)); }

# 3. sin la entrada en el cambio, pasa
stage "hola Mengano"
caso "3. con lista, cambio sin la entrada" 0 "$(precommit)"

# 4. sin distinguir mayusculas
stage "HOLA FULANO INVENTADO"
caso "4. mayusculas" 1 "$(precommit)"

# 5. comentarios y entradas cortas se ignoran (una de 2 letras bloquearia todo)
printf '# Fulano Inventado\nab\n\n' >"$DENY"
stage "ab # Fulano Inventado"   # contiene la linea comentada y la entrada corta, literales
caso "5. comentario y entrada corta ignorados" 0 "$(precommit)"

# 6. solo lineas AGREGADAS: lo que ya estaba en el archivo no vuelve a frenar
printf 'Fulano Inventado\n' >"$DENY"
printf 'hola Fulano Inventado\n' >"$tmp/r/f.txt"; git add -f "$tmp/r/f.txt"
git -c core.hooksPath=/dev/null commit -q -m base || exit 1
printf 'hola Fulano Inventado\notra linea\n' >"$tmp/r/f.txt"; git add -f "$tmp/r/f.txt"
caso "6. lo viejo no se vuelve a frenar" 0 "$(precommit)"

# 7. la lista con CRLF (editada en Windows) tambien frena
printf 'Fulano Inventado\r\n' >"$DENY"
stage "una linea nueva con Fulano Inventado"   # distinta de la ya commiteada en el caso 6
caso "7. lista con CRLF" 1 "$(precommit)"

# 8. PIUMY_PERSONAL_DENYLIST manda sobre .git/info
rm -f "$DENY"; printf 'Zutano Inventado\n' >"$tmp/otra"
stage "hola Zutano Inventado"
caso "8a. sin lista en .git/info, no frena" 0 "$(precommit)"
PIUMY_PERSONAL_DENYLIST="$tmp/otra"; export PIUMY_PERSONAL_DENYLIST
caso "8b. con PIUMY_PERSONAL_DENYLIST, frena" 1 "$(precommit)"
unset PIUMY_PERSONAL_DENYLIST

# 9. el mensaje del commit
printf 'Fulano Inventado\n' >"$DENY"
CLEVERCODER_PARTY_MY_ID= ; export CLEVERCODER_PARTY_MY_ID
caso "9a. mensaje con la entrada" 1 "$(mensaje 'arregla a Fulano Inventado')"
grep -qi "fulano" "$tmp/out" && { echo "FALLA: 9b. commit-msg imprimio el valor"; fails=$((fails + 1)); }
caso "9c. mensaje sin la entrada" 0 "$(mensaje 'arregla un test')"

[ "$fails" -eq 0 ] && echo "TODO OK" || echo "$fails caso(s) fallaron"
[ "$fails" -eq 0 ]
