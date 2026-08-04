package refsworkspace

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

const managedReFSArchitecture = "Managed ReFS Library Pool"

// BuildVerifiedPoolPolicy is the sole production boundary for worker storage
// policy. Both metadata copies and the live mounted volume must agree.
func BuildVerifiedPoolPolicy(host, pool PoolMetadata, volume VolumeInfo) (PoolPolicy, error) {
	if host.SchemaVersion != PoolSchemaVersion || pool.SchemaVersion != PoolSchemaVersion ||
		host.Architecture != managedReFSArchitecture || pool.Architecture != managedReFSArchitecture ||
		host.OwnershipToken == "" || host.OwnershipToken != pool.OwnershipToken ||
		host.VHDXPath == "" || pool.VHDXPath == "" ||
		filepath.Clean(host.VHDXPath) != filepath.Clean(pool.VHDXPath) ||
		host.VHDXIdentity == "" || host.VHDXIdentity != pool.VHDXIdentity ||
		host.VolumeGUIDPath == "" || pool.VolumeGUIDPath == "" || volume.VolumeGUIDPath == "" ||
		!strings.EqualFold(host.VolumeGUIDPath, pool.VolumeGUIDPath) ||
		!strings.EqualFold(host.Filesystem, pool.Filesystem) ||
		host.ClusterSize != pool.ClusterSize ||
		host.MaximumBytes != pool.MaximumBytes ||
		host.SoftBudgetBytes != pool.SoftBudgetBytes ||
		host.WorkerReserveBytes != pool.WorkerReserveBytes ||
		host.MinimumHostFreeBytes != pool.MinimumHostFreeBytes ||
		host.VHDXOverheadReserveBytes != pool.VHDXOverheadReserveBytes ||
		!strings.EqualFold(pool.VolumeGUIDPath, volume.VolumeGUIDPath) ||
		!strings.EqualFold(pool.Filesystem, volume.Filesystem) ||
		pool.ClusterSize != volume.ClusterSize {
		return PoolPolicy{}, newError(CodePoolCorrupt, "verify-pool-policy", pool.VHDXPath, fmt.Errorf("host, pool, and mounted volume identity or policy mismatch"))
	}
	if !strings.EqualFold(volume.Filesystem, "ReFS") || !volume.SupportsBlockCloning ||
		pool.ClusterSize <= 0 || pool.ClusterSize&(pool.ClusterSize-1) != 0 ||
		pool.MaximumBytes < 8<<30 || pool.MaximumBytes%512 != 0 || pool.SoftBudgetBytes <= 0 || pool.WorkerReserveBytes <= 0 ||
		pool.MinimumHostFreeBytes <= 0 || pool.VHDXOverheadReserveBytes < 0 {
		return PoolPolicy{}, newError(CodePoolCorrupt, "verify-pool-policy", pool.VHDXPath, fmt.Errorf("invalid verified pool capability or policy"))
	}
	budgetAndReserve, ok := checkedAddInt64(pool.SoftBudgetBytes, pool.WorkerReserveBytes)
	if !ok || budgetAndReserve > pool.MaximumBytes {
		return PoolPolicy{}, newError(CodePoolCorrupt, "verify-pool-policy", pool.VHDXPath, fmt.Errorf("soft budget plus worker reserve exceeds maximum"))
	}
	return PoolPolicy{
		MaximumBytes: pool.MaximumBytes, SoftBudgetBytes: pool.SoftBudgetBytes,
		WorkerReserveBytes: pool.WorkerReserveBytes, MinimumHostFreeBytes: pool.MinimumHostFreeBytes,
		VHDXOverheadReserveBytes: pool.VHDXOverheadReserveBytes, ClusterSize: pool.ClusterSize,
	}, nil
}

func checkedAddInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func checkedSumInt64(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		var ok bool
		total, ok = checkedAddInt64(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}
