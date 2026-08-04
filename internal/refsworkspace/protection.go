package refsworkspace

// ProtectionEvidence is persisted with the baseline. Content integrity and
// protection integrity are separate gates: a byte-identical but writable
// canonical payload is corrupt.
type ProtectionEvidence struct {
	SchemaVersion           int    `json:"schemaVersion"`
	RootDescriptorSHA256    string `json:"rootDescriptorSha256"`
	FilePolicy              string `json:"filePolicy"`
	DirectoryPolicy         string `json:"directoryPolicy"`
	RegularFileCount        int64  `json:"regularFileCount"`
	ReadOnlyFileCount       int64  `json:"readOnlyFileCount"`
	ProtectedDirectoryCount int64  `json:"protectedDirectoryCount"`
}

const protectionSchemaVersion = 1
