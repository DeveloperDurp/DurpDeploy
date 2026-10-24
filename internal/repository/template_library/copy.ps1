$ErrorActionPreference = 'Stop'

try {
    $sourceFile = $env:FILE_SOURCE
    if (-not $sourceFile) {
        if (-not $env:DURPDEPLOY_STAGE_DIR) {
            throw 'Managed staging is required'
        }
        $sourceFile = Join-Path $env:DURPDEPLOY_STAGE_DIR 'artifact'
    }
    $destination = $env:INSTALL_PATH
    if (-not $sourceFile.StartsWith('/') -or
        -not $destination -or -not $destination.StartsWith('/') -or
        $destination -eq '/' -or -not [System.IO.File]::Exists($sourceFile)) {
        throw 'Use absolute Linux file paths and an existing source'
    }
    & install -T -m 0755 -- $sourceFile $destination
    if ($LASTEXITCODE -ne 0) { throw 'File installation failed' }
    Write-Output 'File installed'
} catch {
    [Console]::Error.WriteLine('File installation failed. Check paths and permissions.')
    exit 1
}

# Keep the blank line above for PowerShell's stdin parser.
