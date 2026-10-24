$ErrorActionPreference = 'Stop'
$client = $null

try {
    $target = [uri]$env:HEALTH_URL
    if (-not $target.IsAbsoluteUri -or $target.Scheme -notin 'http', 'https' -or
        $target.UserInfo -or $target.Fragment) {
        throw 'Invalid health URL'
    }
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $client = [System.Net.Http.HttpClient]::new($handler)
    $client.Timeout = [TimeSpan]::FromSeconds(10)
    for ($attempt = 0; $attempt -lt 6; $attempt++) {
        $response = $null
        try {
            $response = $client.GetAsync($target,
                [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
            if ($response.IsSuccessStatusCode) {
                Write-Output 'HTTP health check passed'
                exit 0
            }
        } catch {
            # Retry bounded network failures without logging the URL.
            Write-Verbose 'HTTP health request failed.'
        } finally {
            if ($response) { $response.Dispose() }
        }
        if ($attempt -lt 5) { Start-Sleep -Seconds 2 }
    }
    throw 'HTTP health check requires a 2xx response'
} catch {
    [Console]::Error.WriteLine('HTTP health check failed. Check the endpoint and network.')
    exit 1
} finally {
    if ($client) { $client.Dispose() }
}

# Keep the blank line above for PowerShell's stdin parser.
