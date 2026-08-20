//go:build windows

package refsworkspace

// testJunctioner uses an NTFS junction so worker tests exercise the production
// reparse boundary without requiring SeCreateSymbolicLinkPrivilege.
type testJunctioner struct{}

func (testJunctioner) Create(target, junction string) error {
	return (nativeJunctioner{}).Create(target, junction)
}

func (testJunctioner) Remove(target, junction string) error {
	return (nativeJunctioner{}).Remove(target, junction)
}
