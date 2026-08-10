//go:build !windows

package vhdxworkspace

import "context"

const WindowsServiceName = "TestPlayStorageBroker"

func RunWindowsService(string) error                 { return ErrBrokerUnavailable }
func RunBrokerConsole(context.Context, string) error { return ErrBrokerUnavailable }
