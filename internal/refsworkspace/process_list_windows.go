//go:build windows

package refsworkspace

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func namedRunningProcesses(names []string) ([]string, error) {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(strings.TrimSuffix(name, ".exe"))] = true
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var found []string
	for {
		name := strings.ToLower(strings.TrimSuffix(windows.UTF16ToString(entry.ExeFile[:]), ".exe"))
		if entry.ProcessID != uint32(os.Getpid()) && wanted[name] {
			found = append(found, name)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, err
		}
	}
	return found, nil
}
