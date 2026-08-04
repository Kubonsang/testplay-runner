package refsworkspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
)

// ProtectionEvidence is persisted with the baseline. Content integrity and
// protection integrity are separate gates: a byte-identical but writable
// canonical payload is corrupt.
type ProtectionEvidence struct {
	SchemaVersion           int    `json:"schemaVersion"`
	RootDescriptorSHA256    string `json:"rootDescriptorSha256"`
	TreeDescriptorSHA256    string `json:"treeDescriptorSha256"`
	FilePolicy              string `json:"filePolicy"`
	DirectoryPolicy         string `json:"directoryPolicy"`
	RegularFileCount        int64  `json:"regularFileCount"`
	ReadOnlyFileCount       int64  `json:"readOnlyFileCount"`
	DirectoryCount          int64  `json:"directoryCount"`
	NonInheritingEntryCount int64  `json:"nonInheritingEntryCount"`
	ProtectedDirectoryCount int64  `json:"protectedDirectoryCount"`
}

const protectionSchemaVersion = 2

func protectionRecordsSHA256(records []string) string {
	sort.Strings(records)
	var canonical bytes.Buffer
	for _, record := range records {
		_ = binary.Write(&canonical, binary.LittleEndian, uint64(len(record)))
		canonical.WriteString(record)
	}
	digest := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(digest[:])
}

func protectionStringSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
