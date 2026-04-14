package oss

// Config holds connection parameters for object storage.
type Config struct {
	MCBinary      string // mc binary path, default "mc"
	Alias         string // mc alias name, default "hiclaw"
	Endpoint      string // MinIO endpoint URL, e.g. "http://minio:9000"
	AccessKey     string // MinIO root access key
	SecretKey     string // MinIO root secret key
	StoragePrefix string // full mc prefix, e.g. "hiclaw/hiclaw-storage"
	Bucket        string // bucket name for policy generation, e.g. "hiclaw-storage"
}

// MirrorOptions controls the behavior of Mirror operations.
type MirrorOptions struct {
	Overwrite bool     // overwrite existing files at destination
	Exclude   []string // file patterns to exclude (passed as --exclude flags to mc mirror)
}

// PolicyRequest describes a scoped access policy for a worker.
type PolicyRequest struct {
	WorkerName string // worker name (used as MinIO username and in path scoping)
	Bucket     string // bucket name, e.g. "hiclaw-storage"
	TeamName   string // optional: grants additional access to teams/<teamName>/ prefix
}
