// services/common/models/models.go
package models

import "time"

// Add the FileStatus type that gateway uses
type FileStatus string

const (
	StatusUploading  FileStatus = "uploading"
	StatusProcessing FileStatus = "processing"
	StatusCompleted  FileStatus = "completed"
	StatusFailed     FileStatus = "failed"
)

type File struct {
	ID         string     `json:"id" db:"file_id"`
	Name       string     `json:"name" db:"file_name"`
	Size       int64      `json:"size" db:"file_size"`
	ChunkCount int        `json:"chunk_count" db:"chunk_count"`
	Status     FileStatus `json:"status" db:"status"`
	UserID     string     `json:"user_id" db:"user_id"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

type Chunk struct {
	ID        string    `json:"id" db:"chunk_id"`
	FileID    string    `json:"file_id" db:"file_id"`
	Index     int       `json:"index" db:"chunk_index"`
	Size      int64     `json:"size" db:"chunk_size"`
	Checksum  string    `json:"checksum" db:"checksum"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
