#Requires -Version 5.1

[CmdletBinding()]
param(
    [string]$Root = 'C:\ProgramData\vsystemd',
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

$SystemSid = 'S-1-5-18'
$AdministratorsSid = 'S-1-5-32-544'
$ServicesSid = 'S-1-5-80-0'

function Test-Elevated {
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [System.Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
}

function New-DirectoryRule {
    param(
        [string]$Sid,
        [System.Security.AccessControl.FileSystemRights]$Rights
    )

    $identity = [System.Security.Principal.SecurityIdentifier]::new($Sid)
    $inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor `
                   [System.Security.AccessControl.InheritanceFlags]::ObjectInherit

    return [System.Security.AccessControl.FileSystemAccessRule]::new(
        $identity,
        $Rights,
        $inheritance,
        [System.Security.AccessControl.PropagationFlags]::None,
        [System.Security.AccessControl.AccessControlType]::Allow
    )
}

function Set-DirectoryAcl {
    param(
        [string]$Path,
        [hashtable[]]$Rules
    )

    $acl = [System.Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($rule in $Rules) {
        $acl.AddAccessRule((New-DirectoryRule -Sid $rule.Sid -Rights $rule.Rights))
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Get-RuleSid {
    param([System.Security.Principal.IdentityReference]$Identity)

    try {
        return $Identity.Translate([System.Security.Principal.SecurityIdentifier]).Value
    }
    catch {
        return $Identity.Value
    }
}

function Test-DirectoryAcl {
    param(
        [string]$Path,
        [hashtable[]]$Rules
    )

    $acl = Get-Acl -LiteralPath $Path
    $issues = @()
    if (-not $acl.AreAccessRulesProtected) {
        $issues += 'ACL inheritance is not disabled'
    }

    foreach ($rule in $Rules) {
        $expected = New-DirectoryRule -Sid $rule.Sid -Rights $rule.Rights
        $found = $false
        foreach ($ace in $acl.Access) {
            if ($ace.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow) {
                continue
            }
            if ((Get-RuleSid $ace.IdentityReference) -ne $rule.Sid) {
                continue
            }
            if ($ace.FileSystemRights -ne $expected.FileSystemRights) {
                continue
            }
            if ($ace.InheritanceFlags -ne $expected.InheritanceFlags) {
                continue
            }
            if ($ace.PropagationFlags -ne $expected.PropagationFlags) {
                continue
            }
            $found = $true
            break
        }
        if (-not $found) {
            $issues += "missing $($rule.Rights) for $($rule.Sid)"
        }
    }
    return $issues
}

function Reset-ChildAcls {
    param(
        [string]$Path,
        [switch]$Recurse
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        return
    }

    $items = if ($Recurse) {
        Get-ChildItem -LiteralPath $Path -Force -Recurse -File
    }
    else {
        Get-ChildItem -LiteralPath $Path -Force -File
    }

    foreach ($item in $items) {
        & icacls.exe $item.FullName /reset /C /Q | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "failed to reset ACL: $($item.FullName)"
        }
    }
}

$rootRules = @(
    @{ Sid = $SystemSid; Rights = 'FullControl' },
    @{ Sid = $AdministratorsSid; Rights = 'FullControl' },
    @{ Sid = $ServicesSid; Rights = 'ReadAndExecute' }
)

$logsRules = @(
    @{ Sid = $SystemSid; Rights = 'FullControl' },
    @{ Sid = $AdministratorsSid; Rights = 'FullControl' },
    @{ Sid = $ServicesSid; Rights = 'Modify' }
)

$specs = @(
    [pscustomobject]@{ Path = $Root; Rules = $rootRules },
    [pscustomobject]@{ Path = (Join-Path $Root 'system'); Rules = $rootRules },
    [pscustomobject]@{ Path = (Join-Path $Root 'logs'); Rules = $logsRules }
)

$failed = $false
$elevated = Test-Elevated
if (-not $Check -and -not $elevated) {
    Write-Host 'Run this script as Administrator to apply permissions.'
    exit 1
}

foreach ($spec in $specs) {
    if (Test-Path -LiteralPath $spec.Path -PathType Container) {
        Write-Host "[exists] $($spec.Path)"
        continue
    }
    if ($Check) {
        Write-Host "[missing] $($spec.Path)"
        $failed = $true
        continue
    }
    New-Item -ItemType Directory -Path $spec.Path -Force | Out-Null
    Write-Host "[create] $($spec.Path)"
}

foreach ($spec in $specs) {
    if (-not (Test-Path -LiteralPath $spec.Path -PathType Container)) {
        continue
    }

    $issues = @(Test-DirectoryAcl -Path $spec.Path -Rules $spec.Rules)
    if ($issues.Count -eq 0) {
        Write-Host "[ok] $($spec.Path) ACL"
        continue
    }

    foreach ($issue in $issues) {
        Write-Host "[mismatch] $($spec.Path): $issue"
    }

    if ($Check) {
        $failed = $true
        continue
    }

    Set-DirectoryAcl -Path $spec.Path -Rules $spec.Rules
    $issues = @(Test-DirectoryAcl -Path $spec.Path -Rules $spec.Rules)
    if ($issues.Count -eq 0) {
        Write-Host "[fixed] $($spec.Path) ACL"
    }
    else {
        foreach ($issue in $issues) {
            Write-Host "[error] $($spec.Path): $issue"
        }
        $failed = $true
    }
}

if (-not $Check) {
    Reset-ChildAcls -Path $Root
    Reset-ChildAcls -Path (Join-Path $Root 'system') -Recurse
    Reset-ChildAcls -Path (Join-Path $Root 'logs') -Recurse
}

if ($failed) {
    Write-Host 'Permissions check failed.'
    exit 1
}

Write-Host 'Permissions check passed.'
exit 0