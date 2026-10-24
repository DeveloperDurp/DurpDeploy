$ErrorActionPreference = 'Stop'
$temporaryFile = $null
$client = $null
$cancellation = $null

try {
    if (-not $env:DURPDEPLOY_STAGE_DIR) {
        throw 'Managed staging is required'
    }
    $original = [uri]$env:ARTIFACT_URL
    $token = $env:ARTIFACT_TOKEN
    if ($token -and $token -cnotmatch '^[a-zA-Z0-9._~+/-]*=*\z') {
        throw 'Invalid bearer token'
    }
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $client = [System.Net.Http.HttpClient]::new($handler)
    $cancellation = [System.Threading.CancellationTokenSource]::new(240000)
    $target = $original

    for ($redirect = 0; $redirect -le 5; $redirect++) {
        if (-not $target.IsAbsoluteUri -or $target.Scheme -ne 'https' -or
            $target.UserInfo -or $target.Fragment) {
            throw 'Use an HTTPS artifact URL without credentials or a fragment'
        }
        $request = [System.Net.Http.HttpRequestMessage]::new(
            [System.Net.Http.HttpMethod]::Get, $target)
        if ($token -and $target.Authority -eq $original.Authority) {
            $request.Headers.Authorization =
                [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $token)
        }
        $response = $null
        try {
            $response = $client.SendAsync($request,
                [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead,
                $cancellation.Token).GetAwaiter().GetResult()
            $status = [int]$response.StatusCode
            if ($status -in 301, 302, 303, 307, 308) {
                if (-not $response.Headers.Location -or $redirect -eq 5) {
                    throw 'Invalid redirect'
                }
                $target = [uri]::new($target, $response.Headers.Location)
                continue
            }
            if ($status -lt 200 -or $status -ge 300) {
                throw 'Artifact download requires a 2xx response'
            }
            $temporaryFile = Join-Path $env:DURPDEPLOY_STAGE_DIR (
                'artifact.' + [guid]::NewGuid().ToString('N'))
            $output = [System.IO.File]::Open($temporaryFile,
                [System.IO.FileMode]::CreateNew)
            try {
                $response.Content.CopyToAsync($output,
                    $cancellation.Token).GetAwaiter().GetResult()
            } finally {
                $output.Dispose()
            }
            [System.IO.File]::Move($temporaryFile,
                (Join-Path $env:DURPDEPLOY_STAGE_DIR 'artifact'), $true)
            $temporaryFile = $null
            Write-Output 'Artifact downloaded to managed staging'
            break
        } finally {
            if ($response) { $response.Dispose() }
            $request.Dispose()
        }
    }
} catch {
    # HTTP exception text can include URLs or credentials.
    [Console]::Error.WriteLine('Artifact download failed. Check HTTPS, credentials, and staging.')
    exit 1
} finally {
    if ($temporaryFile -and [System.IO.File]::Exists($temporaryFile)) {
        [System.IO.File]::Delete($temporaryFile)
    }
    if ($client) { $client.Dispose() }
    if ($cancellation) { $cancellation.Dispose() }
}

# Keep the blank line above for PowerShell's stdin parser.
