//go:build windows

package refsworkspace

import (
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTokenEvidence struct {
	UserSID                 string
	Elevated                bool
	AdministratorsPresent   bool
	AdministratorsEnabled   bool
	AdministratorsDenyOnly  bool
	SymlinkPrivilegePresent bool
	SymlinkPrivilegeEnabled bool
}

func TestWindowsGoTestProcessTokenEvidence(t *testing.T) {
	evidence, err := currentWindowsTokenEvidence()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"go test token: userSID=%s elevated=%t administratorsPresent=%t administratorsEnabled=%t administratorsDenyOnly=%t SeCreateSymbolicLinkPrivilegePresent=%t SeCreateSymbolicLinkPrivilegeEnabled=%t",
		evidence.UserSID,
		evidence.Elevated,
		evidence.AdministratorsPresent,
		evidence.AdministratorsEnabled,
		evidence.AdministratorsDenyOnly,
		evidence.SymlinkPrivilegePresent,
		evidence.SymlinkPrivilegeEnabled,
	)
}

func currentWindowsTokenEvidence() (windowsTokenEvidence, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	evidence := windowsTokenEvidence{UserSID: user.User.Sid.String(), Elevated: token.IsElevated()}

	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	for _, group := range groups.AllGroups() {
		if !group.Sid.Equals(administrators) {
			continue
		}
		evidence.AdministratorsPresent = true
		evidence.AdministratorsEnabled = group.Attributes&windows.SE_GROUP_ENABLED != 0
		evidence.AdministratorsDenyOnly = group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY != 0
		break
	}

	privileges, err := tokenPrivileges(token)
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	name, err := windows.UTF16PtrFromString("SeCreateSymbolicLinkPrivilege")
	if err != nil {
		return windowsTokenEvidence{}, err
	}
	var symlinkLUID windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &symlinkLUID); err != nil {
		return windowsTokenEvidence{}, err
	}
	for _, privilege := range privileges {
		if privilege.Luid != symlinkLUID {
			continue
		}
		evidence.SymlinkPrivilegePresent = true
		evidence.SymlinkPrivilegeEnabled = privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0
		break
	}
	return evidence, nil
}

func tokenPrivileges(token windows.Token) ([]windows.LUIDAndAttributes, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, fmt.Errorf("query token privilege size: %w", err)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0], size, &size); err != nil {
		return nil, fmt.Errorf("query token privileges: %w", err)
	}
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0])).AllPrivileges()
	return append([]windows.LUIDAndAttributes(nil), privileges...), nil
}
