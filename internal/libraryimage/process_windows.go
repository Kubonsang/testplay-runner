//go:build windows

package libraryimage

// Windows has no stdlib equivalent of kill(pid, 0). Be conservative: never
// auto-remove a lock based only on age, because the owning process may be live.
func processIsAlive(pid int) bool {
	return pid > 0
}
