//go:build windows

package refsworkspace

import (
	"os"

	"golang.org/x/sys/windows"
)

func createSyntheticSparseFile(path string, clusterSize int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(file.Fd()), fsctlSetSparse, nil, 0, nil, 0, &returned, nil); err != nil {
		return err
	}
	size := clusterSize*16 + 137
	if err := file.Truncate(size); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte("allocated-leading-extent"), clusterSize); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte("allocated-middle-extent"), clusterSize*9+31); err != nil {
		return err
	}
	_, err = file.WriteAt([]byte("allocated-tail"), size-14)
	return err
}
