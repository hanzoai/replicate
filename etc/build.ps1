[CmdletBinding()]
Param (
    [Parameter(Mandatory = $true)]
    [String] $Version
)
$ErrorActionPreference = "Stop"

# Update working directory.
Push-Location $PSScriptRoot
Trap {
    Pop-Location
}

Invoke-Expression "candle.exe -nologo -arch x64 -ext WixUtilExtension -out replicate.wixobj -dVersion=`"$Version`" replicate.wxs"
Invoke-Expression "light.exe  -nologo -spdb -ext WixUtilExtension -out `"replicate-${Version}.msi`" replicate.wixobj"

Pop-Location
