# Firma una release de Piumy (T183, ct-2026-11-11-0201): escribe dist/SHA256SUMS y
# dist/SHA256SUMS.sig sobre los paquetes de UNA versión. Piumy no se actualiza
# solo desde una release sin esa firma (docs/T176-AUTOUPDATE.md).
#
# Se corre con vault_run (pwsh 7), con la clave privada inyectada en
# PIUMY_RELEASE_SIGNING_KEY: la clave llega SOLO por esa variable de entorno,
# nunca como argumento ni a disco, y este script no la imprime ni la reenvía.
#
#   argumento: la versión, x.y.z (p. ej. 0.13.0)
#
# Al terminar queda el par SHA256SUMS + SHA256SUMS.sig firmado con la clave cuya
# pública está en internal/autoupdate/pubkey.go, o ninguno de los dos: cualquier
# fallo borra lo que haya, para que no se suba un par viejo o a medias.
param([string]$Version)

$ErrorActionPreference = 'Stop'

try {
    if (-not $PSScriptRoot) {
        throw 'no sé dónde está coderoot: el script tiene que correr desde su archivo, no pegado en una consola'
    }
    $coderoot = Split-Path -Parent $PSScriptRoot
    if (-not (Test-Path -LiteralPath (Join-Path $coderoot 'cmd/signrelease'))) {
        throw "no encuentro cmd/signrelease en ${coderoot}: este script va en coderoot/scripts/"
    }
    # Desde acá, CUALQUIER fallo borra el par de dist/. Antes no: si coderoot no es
    # coderoot, ese dist/ no es nuestro.
    $script:dist = Join-Path $coderoot 'dist'
    if ($Version -notmatch '^\d+\.\d+\.\d+$') {
        throw "falta la versión o no es x.y.z (recibí '$Version'): uso: sign-release <versión>, p. ej. 0.13.0"
    }

    if (-not $env:PIUMY_RELEASE_SIGNING_KEY) {
        throw 'falta la variable PIUMY_RELEASE_SIGNING_KEY con la clave privada: córrelo con vault_run'
    }

    # La pública que Piumy lleva incorporada: es lo que la firma tiene que respetar.
    $pubkeyGo = Join-Path $coderoot 'internal/autoupdate/pubkey.go'
    $found = [regex]::Match((Get-Content -Raw -LiteralPath $pubkeyGo), '(?m)^\s*(?:const|var)\s+releasePublicKey\s*=\s*"([^"]*)"')
    if (-not $found.Success) {
        throw "no encuentro la constante releasePublicKey en $pubkeyGo"
    }
    $builtInKey = $found.Groups[1].Value
    if (-not $builtInKey) {
        throw 'releasePublicKey en internal/autoupdate/pubkey.go está vacía: pon ahí la pública (go run ./cmd/signrelease keygen), commitéala y vuelve a armar dist/ ANTES de firmar'
    }

    # Qué paquetes lleva una release sale de build-all.sh (las llamadas `package`),
    # no de una lista copiada acá: una plataforma nueva se firma sin tocar este script.
    $buildAll = Get-Content -Raw -LiteralPath (Join-Path $coderoot 'build-all.sh')
    $platforms = @([regex]::Matches($buildAll, '(?m)^package\s+\S+\s+(\S+)\s*$') | ForEach-Object { $_.Groups[1].Value })
    if ($platforms.Count -eq 0) {
        throw 'no encontré ninguna llamada `package <binario> <plataforma>` en build-all.sh: ¿cambió su forma?'
    }
    # Con los tar.gz va el Setup de Windows; su nombre lo fija installer/windows/piumy.iss.
    $names = @("Piumy-Setup-$Version.exe") + @($platforms | ForEach-Object { "Piumy-$Version-$_.tar.gz" })
    $missing = @($names | Where-Object { -not (Test-Path -LiteralPath (Join-Path $script:dist $_)) })
    if ($missing.Count -gt 0) {
        throw "faltan en dist/ paquetes de la versión ${Version}: $($missing -join ', ') (¿corrió build-all.sh con VERSION=${Version}?)"
    }

    # Los binarios se armaron con la pública que había en pubkey.go ENTONCES. El exe
    # de Windows que build-all.sh deja en dist/ es el que empaqueta el Setup: si no
    # lleva la pública de hoy, la release saldría firmada pero sin poder verificarse.
    $windowsExe = Join-Path $script:dist 'piumy-gateway-windows-amd64.exe'
    $exeText = [System.Text.Encoding]::Latin1.GetString([System.IO.File]::ReadAllBytes($windowsExe))
    if (-not $exeText.Contains($builtInKey)) {
        throw 'el binario de Windows de dist/ no lleva la clave pública de pubkey.go: dist/ se armó antes de ponerla; vuelve a correr build-all.sh'
    }

    $files = $names | ForEach-Object { Join-Path $script:dist $_ }
    $output = & go -C $coderoot run ./cmd/signrelease sign $script:dist @files
    if ($LASTEXITCODE -ne 0) {
        throw "signrelease falló (código $LASTEXITCODE)"
    }
    $output

    # signrelease imprime la pública de la clave con que firmó: tiene que ser la
    # incorporada (vacía si no la imprimió: tampoco pasa). "p\S+" y no "pública":
    # la tilde no sobrevive a la consola.
    $signedWith = [regex]::Match(($output -join "`n"), '(?m)^clave p\S+ (\S+)\s*$').Groups[1].Value
    if ($signedWith -cne $builtInKey) {
        throw "la clave del vault NO es la de Piumy: firmó con la pública '$signedWith' y pubkey.go dice '$builtInKey'. No se sube nada firmado así"
    }
    Write-Output "OK: $($names.Count) paquetes firmados con la clave de pubkey.go. Subir con ellos: dist/SHA256SUMS y dist/SHA256SUMS.sig"
}
catch {
    # Sea cual sea el fallo, no queda un par viejo o a medias que se pueda subir.
    if ($script:dist) {
        Remove-Item -LiteralPath (Join-Path $script:dist 'SHA256SUMS'), (Join-Path $script:dist 'SHA256SUMS.sig') -ErrorAction SilentlyContinue
    }
    [Console]::Error.WriteLine("sign-release: $($_.Exception.Message)")
    exit 1
}
