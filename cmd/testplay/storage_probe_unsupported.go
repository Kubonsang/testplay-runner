//go:build !windows

package main

func brokerProbeAccessDenied(error) bool { return false }
