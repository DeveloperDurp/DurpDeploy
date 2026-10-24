$ErrorActionPreference = 'Stop'

try {
    $service = $env:SERVICE_NAME
    if (-not $service -or $service -cnotmatch '^[a-zA-Z0-9_][a-zA-Z0-9_@.:-]*\z') {
        throw 'Invalid service unit name'
    }
    & systemctl --no-ask-password restart -- $service
    if ($LASTEXITCODE -ne 0) { throw 'Service restart failed' }
    & systemctl --no-ask-password is-active --quiet -- $service
    if ($LASTEXITCODE -ne 0) { throw 'Service is inactive' }
    Write-Output 'Service is active'
} catch {
    [Console]::Error.WriteLine('Service restart failed. Check the unit and permissions.')
    exit 1
}

# Keep the blank line above for PowerShell's stdin parser.
