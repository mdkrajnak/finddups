package model

// FileRecord represents a file discovered during the filesystem walk.
type FileRecord struct {
	ID          int64  `json:"id"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	MtimeSec    int64  `json:"mtime_sec"`
	MtimeNsec   int64  `json:"-"`
	Inode       uint64 `json:"-"`
	Dev         uint64 `json:"-"`
	Phase       int    `json:"-"`
	PartialHash string `json:"-"`
	FullHash    string `json:"-"`
	Error       string `json:"error,omitempty"`
	DupGroup    int64  `json:"-"`
}

// DuplicateGroup represents a group of files with identical content.
type DuplicateGroup struct {
	ID             int64        `json:"id"`
	Size           int64        `json:"size"`
	FullHash       string       `json:"hash"`
	FileCount      int          `json:"file_count"`
	WastedBytes    int64        `json:"wasted_bytes"`
	HasMarkedFiles bool         `json:"has_marked_files"`
	Files          []FileRecord `json:"files"`
}

// ScanState tracks overall scan progress.
type ScanState struct {
	RootPath   string
	StartedAt  string
	Phase      int
	WalkDone   bool
	TotalFiles int64
	TotalBytes int64
	UpdatedAt  string
}

// Deletion tracks a file marked for deletion.
type Deletion struct {
	ID        int64
	FileID    int64
	Path      string
	MarkedAt  string
	DeletedAt string
	Status    string // pending, deleted, failed, cancelled
}

// HashResult is returned by hash workers.
type HashResult struct {
	FileID int64
	Path   string
	Hash   string
	Err    error
	IsFull bool // true if this is a full hash (file <= 8KB)
}
