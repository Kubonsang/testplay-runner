//go:build windows

package vhdxworkspace

import "golang.org/x/sys/windows"

func CurrentUserSID() (string, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
