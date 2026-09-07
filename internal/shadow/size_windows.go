//go:build windows

package shadow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var allocatedProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")
var standardInfoProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetFileInformationByHandleEx")

func windowsUsagePath(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p, nil
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:], nil
	}
	return `\\?\` + p, nil
}

// Directory enumeration returns allocation for an entire batch. Opening every
// ordinary file just for metrics otherwise dominates warm cache write-back.
func measureDirectoryUsage(root string) (DirectoryUsage, error) {
	var usage DirectoryUsage
	info, err := os.Lstat(root)
	if err != nil {
		return usage, err
	}
	if !info.IsDir() {
		return measureDirectoryUsageByWalk(root)
	}
	queue := []string{root}
	for len(queue) > 0 {
		directory := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		err := enumerateUsage(directory, &usage, &queue)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			return measureDirectoryUsageByWalk(root)
		}
		if err != nil {
			return usage, err
		}
	}
	return usage, nil
}

func enumerateUsage(directory string, usage *DirectoryUsage, queue *[]string) error {
	p, err := windowsUsagePath(directory)
	if err != nil {
		return err
	}
	pointer, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("directory changed to a link: %s", directory)
	}
	buffer := make([]byte, 64*1024)
	for {
		// FILE_ID_BOTH_DIR_INFO: fixed fields through FileId occupy 104 bytes.
		ok, _, callErr := standardInfoProc.Call(uintptr(handle), 10, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if ok == 0 {
			if errors.Is(callErr, windows.ERROR_NO_MORE_FILES) {
				return nil
			}
			return callErr
		}
		for offset := 0; ; {
			if offset < 0 || offset+104 > len(buffer) {
				return errors.New("invalid directory allocation record")
			}
			record := buffer[offset:]
			next := int(binary.LittleEndian.Uint32(record[0:4]))
			nameBytes := int(binary.LittleEndian.Uint32(record[60:64]))
			if nameBytes%2 != 0 || nameBytes > len(record)-104 {
				return errors.New("invalid directory allocation filename")
			}
			nameUnits := make([]uint16, nameBytes/2)
			for i := range nameUnits {
				nameUnits[i] = binary.LittleEndian.Uint16(record[104+i*2 : 106+i*2])
			}
			name := string(utf16.Decode(nameUnits))
			attributes := binary.LittleEndian.Uint32(record[56:60])
			if name != "." && name != ".." && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
				if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
					*queue = append(*queue, filepath.Join(directory, name))
				} else {
					logical := int64(binary.LittleEndian.Uint64(record[40:48]))
					allocated := int64(binary.LittleEndian.Uint64(record[48:56]))
					if attributes&(windows.FILE_ATTRIBUTE_COMPRESSED|windows.FILE_ATTRIBUTE_SPARSE_FILE) != 0 {
						allocated, err = allocatedFileBytes(filepath.Join(directory, name), nil)
						if err != nil {
							return err
						}
					}
					if logical < 0 || allocated < 0 {
						return errors.New("negative directory allocation")
					}
					usage.LogicalBytes += logical
					usage.AllocatedBytes += allocated
				}
			}
			if next == 0 {
				break
			}
			if next < 104+nameBytes || next%8 != 0 {
				return errors.New("invalid next directory allocation record")
			}
			offset += next
		}
	}
}

func allocatedFileBytes(path string, _ fs.FileInfo) (int64, error) {
	p, err := windowsUsagePath(path)
	if err != nil {
		return 0, err
	}
	pointer, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, fmt.Errorf("file changed to a link: %s", path)
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_COMPRESSED|windows.FILE_ATTRIBUTE_SPARSE_FILE) == 0 {
		var standard struct {
			AllocationSize int64
			EndOfFile      int64
			NumberOfLinks  uint32
			DeletePending  uint8
			Directory      uint8
			Padding        [2]byte
		}
		ok, _, callErr := standardInfoProc.Call(uintptr(handle), 1, uintptr(unsafe.Pointer(&standard)), unsafe.Sizeof(standard))
		if ok == 0 {
			return 0, callErr
		}
		return standard.AllocationSize, nil
	}
	var high uint32
	low, _, callErr := allocatedProc.Call(uintptr(unsafe.Pointer(pointer)), uintptr(unsafe.Pointer(&high)))
	runtime.KeepAlive(pointer)
	if uint32(low) == 0xffffffff && callErr != syscall.Errno(0) {
		return 0, callErr
	}
	return int64(uint64(high)<<32 | uint64(uint32(low))), nil
}
