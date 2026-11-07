# Installs the latest release of tmux-tabbed-terminal for this user. Run it with:
#
#   irm https://github.com/wrouesnel/tmux-tabbed-terminal/releases/latest/download/install.ps1 | iex
#
# It downloads the release's installer, checks it against the release's SHA-256 checksum,
# and runs it. The installer isn't signed yet, so SmartScreen stops it when a browser
# downloads it; downloaded here, it isn't marked as from the internet, and runs.

& {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 shows download progress so slowly it holds up the download.
    $ProgressPreference = 'SilentlyContinue'
    # Older Windows PowerShell doesn't offer TLS 1.2, which GitHub needs, by itself.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'wrouesnel/tmux-tabbed-terminal'
    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $asset = $release.assets | Where-Object name -Like '*-windows-amd64-setup.exe' | Select-Object -First 1
    if (-not $asset) { throw "The latest release, $($release.tag_name), has no Windows installer." }

    $installer = Join-Path $env:TEMP $asset.name
    Write-Host "Downloading tmux-tabbed-terminal $($release.tag_name)..."
    Invoke-WebRequest -UseBasicParsing $asset.browser_download_url -OutFile $installer
    Invoke-WebRequest -UseBasicParsing "$($asset.browser_download_url).sha256" -OutFile "$installer.sha256"

    $want = (Get-Content "$installer.sha256" -Raw).Split(' ')[0].Trim()
    $got = (Get-FileHash $installer -Algorithm SHA256).Hash
    if ($got -ne $want) {
        Remove-Item $installer, "$installer.sha256" -ErrorAction SilentlyContinue
        throw "The installer's checksum is $got, not the release's $want, so it wasn't run."
    }

    Write-Host "Running the installer..."
    Start-Process -Wait $installer
    Remove-Item $installer, "$installer.sha256" -ErrorAction SilentlyContinue
}
