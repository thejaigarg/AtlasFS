// services/upload/main.go
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/segmentio/kafka-go"

	"atlasfs/services/common/config"
	"atlasfs/services/common/events"
	"atlasfs/services/common/models"
)

const (
	ChunkSize  = 4 * 1024 * 1024 // 4MB chunks
	BucketName = "atlasfs-chunks"
)

type UploadService struct {
	config      *config.Config
	minioClient *minio.Client
	kafkaWriter *kafka.Writer
	db          *sql.DB
	router      *gin.Engine
}

func NewUploadService() *UploadService {
	cfg := config.Load()

	// Initialize MinIO client
	minioClient, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		log.Printf("Warning: Could not connect to MinIO: %v", err)
	} else {
		log.Printf("✅ MinIO client initialized for %s", cfg.MinioEndpoint)

		// Create bucket if it doesn't exist
		ctx := context.Background()
		exists, _ := minioClient.BucketExists(ctx, BucketName)
		if !exists {
			err = minioClient.MakeBucket(ctx, BucketName, minio.MakeBucketOptions{})
			if err != nil {
				log.Printf("Failed to create bucket: %v", err)
			} else {
				log.Printf("✅ Created bucket: %s", BucketName)
			}
		}
	}

	// Initialize Kafka writer
	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(cfg.KafkaBrokers...),
		Topic:    "file.events",
		Balancer: &kafka.LeastBytes{},
	}

	// Initialize PostgreSQL
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPassword, cfg.PostgresDB)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Printf("Warning: Could not connect to PostgreSQL: %v", err)
	} else {
		if err = db.Ping(); err == nil {
			log.Printf("✅ PostgreSQL connected")
		}
	}

	return &UploadService{
		config:      cfg,
		minioClient: minioClient,
		kafkaWriter: kafkaWriter,
		db:          db,
		router:      gin.Default(),
	}
}

func (u *UploadService) setupRoutes() {
	u.router.GET("/health", u.healthCheck)
	u.router.POST("/upload", u.handleUpload)
	u.router.POST("/chunk", u.handleChunk)
	u.router.GET("/status/:id", u.getUploadStatus)
}

func (u *UploadService) handleUpload(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file provided"})
		return
	}
	defer file.Close()

	fileID := fmt.Sprintf("file_%d", time.Now().UnixNano())
	log.Printf("📥 Processing upload for file: %s (size: %d)", header.Filename, header.Size)

	// Process file in chunks
	chunkIndex := 0
	chunks := []models.Chunk{}

	for {
		// Read chunk
		buffer := make([]byte, ChunkSize)
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
			return
		}
		if n == 0 {
			break
		}

		// Calculate checksum
		hash := sha256.Sum256(buffer[:n])
		checksum := hex.EncodeToString(hash[:])

		// Create chunk ID
		chunkID := fmt.Sprintf("%s_chunk_%d", fileID, chunkIndex)

		// Store chunk in MinIO
		ctx := context.Background()
		_, err = u.minioClient.PutObject(ctx, BucketName, chunkID,
			bytes.NewReader(buffer[:n]), int64(n),
			minio.PutObjectOptions{ContentType: "application/octet-stream"})

		if err != nil {
			log.Printf("Failed to store chunk %s: %v", chunkID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store chunk"})
			return
		}

		// Record chunk info
		chunk := models.Chunk{
			ID:        chunkID,
			FileID:    fileID,
			Index:     chunkIndex,
			Size:      int64(n),
			Checksum:  checksum,
			CreatedAt: time.Now(),
		}
		chunks = append(chunks, chunk)

		// Store chunk metadata in PostgreSQL
		if u.db != nil {
			query := `
                INSERT INTO chunks (chunk_id, file_id, chunk_index, chunk_size, checksum, created_at)
                VALUES ($1, $2, $3, $4, $5, $6)
            `
			_, dbErr := u.db.Exec(query, chunk.ID, chunk.FileID, chunk.Index,
				chunk.Size, chunk.Checksum, chunk.CreatedAt)
			if dbErr != nil {
				log.Printf("Failed to store chunk metadata: %v", dbErr)
			}
		}

		// Publish chunk created event
		event := events.NewEvent(
			events.FileChunkCreated,
			"upload",
			map[string]interface{}{
				"file_id":  fileID,
				"chunk_id": chunkID,
				"index":    chunkIndex,
				"size":     n,
				"checksum": checksum,
			},
		)

		if eventData, _ := event.ToJSON(); eventData != nil {
			u.kafkaWriter.WriteMessages(context.Background(),
				kafka.Message{
					Key:   []byte(chunkID),
					Value: eventData,
				},
			)
		}

		log.Printf("✅ Stored chunk %d for file %s (size: %d bytes)", chunkIndex, fileID, n)
		chunkIndex++
	}

	// Update file status in PostgreSQL
	if u.db != nil {
		query := `
            UPDATE files 
            SET chunk_count = $1, status = $2, updated_at = $3
            WHERE file_id = $4
        `
		_, err = u.db.Exec(query, len(chunks), "completed", time.Now(), fileID)
		if err != nil {
			log.Printf("Failed to update file status: %v", err)
		}
	}

	// Publish upload completed event
	event := events.NewEvent(
		events.FileUploadCompleted,
		"upload",
		map[string]interface{}{
			"file_id":     fileID,
			"filename":    header.Filename,
			"size":        header.Size,
			"chunk_count": len(chunks),
		},
	)

	if eventData, _ := event.ToJSON(); eventData != nil {
		u.kafkaWriter.WriteMessages(context.Background(),
			kafka.Message{
				Key:   []byte(fileID),
				Value: eventData,
			},
		)
	}

	log.Printf("✅ Upload completed for %s: %d chunks stored", fileID, len(chunks))

	c.JSON(http.StatusOK, gin.H{
		"file_id":     fileID,
		"filename":    header.Filename,
		"size":        header.Size,
		"chunk_count": len(chunks),
		"status":      "completed",
		"chunks":      chunks,
	})
}

func (u *UploadService) handleChunk(c *gin.Context) {
	// For handling individual chunk uploads (future enhancement)
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Individual chunk upload not yet implemented"})
}

func (u *UploadService) getUploadStatus(c *gin.Context) {
	fileID := c.Param("id")

	if u.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not available"})
		return
	}

	var file models.File
	err := u.db.QueryRow(`
        SELECT file_id, file_name, file_size, chunk_count, status, created_at
        FROM files WHERE file_id = $1
    `, fileID).Scan(&file.ID, &file.Name, &file.Size, &file.ChunkCount, &file.Status, &file.CreatedAt)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	c.JSON(http.StatusOK, file)
}

func (u *UploadService) healthCheck(c *gin.Context) {
	health := gin.H{
		"status":    "healthy",
		"service":   "upload",
		"timestamp": time.Now().Unix(),
	}

	// Check MinIO
	if u.minioClient != nil {
		ctx := context.Background()
		_, err := u.minioClient.ListBuckets(ctx)
		if err != nil {
			health["minio"] = "unhealthy"
		} else {
			health["minio"] = "healthy"
		}
	}

	// Check PostgreSQL
	if u.db != nil && u.db.Ping() == nil {
		health["postgres"] = "healthy"
	} else {
		health["postgres"] = "unhealthy"
	}

	c.JSON(http.StatusOK, health)
}

func main() {
	log.Println("🚀 Starting AtlasFS Upload Service...")

	service := NewUploadService()
	defer service.kafkaWriter.Close()
	defer service.db.Close()

	service.setupRoutes()

	port := "8081"
	log.Printf("✅ Upload Service listening on port %s", port)

	if err := service.router.Run(":" + port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
