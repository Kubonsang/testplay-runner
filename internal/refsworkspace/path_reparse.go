package refsworkspace

// inspectPathReparse is replaceable by same-package tests so Windows reparse
// handling can be exercised without manufacturing a privileged junction.
var inspectPathReparse = platformPathIsReparse
