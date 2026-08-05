function Compare-IDs($Before, $After, [string]$Property) {
  $beforeValues = @(
    $Before |
      Where-Object { $null -ne $_ -and $_.PSObject.Properties.Name -contains $Property } |
      ForEach-Object { [string]$_.$Property }
  )
  @(
    $After |
      Where-Object {
        $null -ne $_ -and
        $_.PSObject.Properties.Name -contains $Property -and
        $beforeValues -notcontains [string]$_.$Property
      }
  )
}
