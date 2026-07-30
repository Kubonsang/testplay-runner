# Windows ReFS Block Clone Probe

## Status

`IMPLEMENTED / AWAITING HARDWARE VALIDATION`

현재 작업 장비는 macOS이며 Windows 또는 ReFS volume이 없으므로 실제
Block Clone 성공이나 물리 공간 절감을 주장하지 않는다.

## Scope

```text
deterministic cluster-aligned file
→ FSCTL_DUPLICATE_EXTENTS_TO_FILE
→ full byte parity
→ destination cluster mutation
→ source cluster mutation
→ allocation and volume free-space deltas
→ same-volume physical-copy comparison
→ cleanup
```

Unity, GNF_, ImageStore, LibraryMaterializer, Worker와 Sharding에는 연결하지
않았다.

## Official API basis

- Microsoft ReFS Block Cloning:
  <https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning>
- `FSCTL_DUPLICATE_EXTENTS_TO_FILE`:
  <https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_duplicate_extents_to_file>
- `DUPLICATE_EXTENTS_DATA`:
  <https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/41d997f1-9437-416f-88ed-8a70c02613ee>
- `FILE_STANDARD_INFO`:
  <https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_standard_info>
- `GetVolumeInformationW`:
  <https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumeinformationw>

초기 Probe는 구조체와 ReFS 지원 계약이 공개적으로 명확한 기존 Control만
사용한다. `_EX` 자동 fallback은 없으며 시도하지 않은 Control을 결과에
기록하지 않는다.

## Required environment

| Field | Current evidence |
|---|---|
| Windows edition/build | Not run |
| Go version on Windows | Not run |
| CPU/RAM | Not run |
| Volume letter/root | Not provided |
| Filesystem | Not measured |
| Volume serial | Not measured |
| Cluster size | Not measured |
| Free space | Not measured |
| Dev Drive | Not measured |

Probe는 사용자가 준비한 기존 root만 사용한다. Format, Partition, VHDX
생성과 관리자 권한 획득을 자동 수행하지 않는다.

## Exact commands

```powershell
Get-ComputerInfo |
  Select-Object WindowsProductName, WindowsVersion, OsBuildNumber

Get-Volume |
  Select-Object DriveLetter, FileSystem, FileSystemLabel,
                AllocationUnitSize, SizeRemaining, Size

fsutil fsinfo volumeinfo X:
fsutil fsinfo refsinfo X:
go version

$env:TESTPLAY_REFS_PROBE_ROOT = "X:\testplay-refs-test"
go test -tags=refs_integration ./internal/refsclone `
  -run '^TestReFSBlockCloneProbe$' -v -count=1

go test -tags=refs_integration ./internal/refsclone `
  -run '^TestReFSBlockCloneProbe$' -v -count=5
```

환경변수가 없으면 명확한 이유와 함께 Skip한다. 환경변수가 설정됐는데
ReFS가 아니거나 실제 `DeviceIoControl`이 실패하면 테스트는 실패한다.

## Fixture and alignment

- 기본 크기: 최소 1 MiB
- 실제 크기: `ceil(1 MiB / clusterSize) * clusterSize`
- offset과 length: cluster aligned
- clone length: strictly less than 4 GiB
- 이름: `source.bin`, `clone.bin`, `physical-copy.bin`
- 데이터: cluster index를 포함하는 결정적 pattern

Destination은 새 파일만 허용하고 clone 전에 필요한 logical length로
확장한다. 기존 파일을 덮어쓰지 않는다.

## Evidence contract

- 실제 Control 이름과 cloned bytes
- Source/Clone 전체 SHA-256 parity
- Destination 쓰기 후 Source 불변
- Source 쓰기 후 Destination 불변
- `FILE_STANDARD_INFO.AllocationSize`
- `GetCompressedFileSizeW`
- clone/mutation/physical-copy 단계별 volume free bytes
- same-volume physical-copy delta 비교
- temporary fixture cleanup 결과

개별 Allocation Size는 공유 block 전체를 표시할 수 있으므로 단독 증거로
사용하지 않는다. volume free-space delta와 physical-copy control을 함께
비교한다.

## Results

| Check | Result |
|---|---|
| Actual Windows ReFS | NOT RUN |
| Same-volume evidence | NOT RUN |
| DeviceIoControl success | NOT RUN |
| Byte parity | NOT RUN |
| Destination → Source isolation | NOT RUN |
| Source → Destination isolation | NOT RUN |
| Allocation metrics | NOT RUN |
| Volume delta | NOT RUN |
| Physical-copy comparison | NOT RUN |
| Cleanup | NOT RUN |
| Five repetitions | 0/5 |

## Verdict

`IMPLEMENTED / AWAITING HARDWARE VALIDATION`

T4를 시작할 증거는 아직 없다. 실제 ReFS에서 5/5 정확성·격리·cleanup이
통과하고 Block Clone volume delta가 동일 volume physical copy보다 작다는
Evidence를 확보한 뒤에만 `PROVEN`과 T4 진행을 검토한다.
