$ErrorActionPreference = 'Stop'

try {
    $manifest = $env:COMPOSE_FILE
    $projectName = $env:COMPOSE_PROJECT
    if (-not $manifest -or -not $manifest.StartsWith('/') -or
        -not [System.IO.File]::Exists($manifest) -or
        -not $projectName -or $projectName -cnotmatch '^[a-z0-9][a-z0-9_-]*\z' -or
        -not $env:APP_IMAGE) {
        throw 'Invalid Compose configuration'
    }
    & docker compose --file $manifest --project-name $projectName pull
    if ($LASTEXITCODE -ne 0) { throw 'Compose pull failed' }
} catch {
    [Console]::Error.WriteLine('Compose pull failed. Check configuration and permissions.')
    exit 1
}

# Keep the blank line above for PowerShell's stdin parser.
