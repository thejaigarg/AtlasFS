// services/download/main.go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/segmentio/kafka-go"

	"atlasfs/services/common/config"
	"atlasfs/services/common/events"
)

const BucketName = "atlasfs-chunks"

type DownloadService struct {
	config      *config.Config
	minioClient *minio.Client
	kafkaWriter *kafka.Writer
	db          *sql.DB
	router      *gin.Engine
}

type ChunkInfo struct {
	ChunkID    string
	ChunkIndex int
	ChunkSize  int64
	Checksum   string
}

func NewDownloadService() *DownloadService {
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
	}

	// Initialize Kafka writer
	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(cfg.KafkaBrokers...),
		Topic:    "file.events",
		Balancer: &kafka.LeastBytes{},
	}
	log.Printf("✅ Kafka writer initialized")

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

	return &DownloadService{
		config:      cfg,
		minioClient: minioClient,
		kafkaWriter: kafkaWriter,
		db:          db,
		router:      gin.Default(),
	}
}

func (d *DownloadService) setupRoutes() {
	d.router.GET("/health", d.healthCheck)
	d.router.GET("/download/:id", d.downloadFile)
	d.router.GET("/stream/:id", d.streamFile)
	d.router.GET("/info/:id", d.getFileInfo)
}

func (d *DownloadService) downloadFile(c *gin.Context) {
	fileID := c.Param("id")

	log.Printf("📥 Download request for file: %s", fileID)

	// Get file metadata from PostgreSQL
	var fileName string
	var fileSize int64
	var chunkCount int
	err := d.db.QueryRow(`
        SELECT file_name, file_size, chunk_count 
        FROM files 
        WHERE file_id = $1 AND status = 'completed'
    `, fileID).Scan(&fileName, &fileSize, &chunkCount)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// Get all chunks for this file
	rows, err := d.db.Query(`
        SELECT chunk_id, chunk_index, chunk_size, checksum 
        FROM chunks 
        WHERE file_id = $1 
        ORDER BY chunk_index
    `, fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get chunks"})
		return
	}
	defer rows.Close()

	// Collect chunk information
	var chunks []ChunkInfo
	for rows.Next() {
		var chunk ChunkInfo
		err := rows.Scan(&chunk.ChunkID, &chunk.ChunkIndex, &chunk.ChunkSize, &chunk.Checksum)
		if err != nil {
			log.Printf("Error scanning chunk: %v", err)
			continue
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No chunks found for file"})
		return
	}

	// Sort chunks by index (safety check)
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ChunkIndex < chunks[j].ChunkIndex
	})

	// Set response headers for file download
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", fileSize))

	// Stream chunks to client
	ctx := context.Background()
	bytesWritten := int64(0)

	for _, chunk := range chunks {
		log.Printf("Retrieving chunk %d: %s", chunk.ChunkIndex, chunk.ChunkID)

		// Get chunk from MinIO
		obj, err := d.minioClient.GetObject(ctx, BucketName, chunk.ChunkID, minio.GetObjectOptions{})
		if err != nil {
			log.Printf("Failed to get chunk %s: %v", chunk.ChunkID, err)
			return
		}

		// Stream chunk data to client
		written, err := io.Copy(c.Writer, obj)
		if err != nil {
			log.Printf("Failed to stream chunk %s: %v", chunk.ChunkID, err)
			obj.Close()
			return
		}
		bytesWritten += written
		obj.Close()

		// Flush after each chunk for better streaming
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
	}

	log.Printf("✅ Successfully downloaded file %s (%d bytes)", fileID, bytesWritten)

	// Publish download completed event
	event := events.NewEvent(
		"file.download.completed",
		"download",
		map[string]interface{}{
			"file_id":     fileID,
			"file_name":   fileName,
			"bytes_sent":  bytesWritten,
			"chunk_count": len(chunks),
			"client_ip":   c.ClientIP(),
		},
	)

	if eventData, _ := event.ToJSON(); eventData != nil {
		d.kafkaWriter.WriteMessages(context.Background(),
			kafka.Message{
				Key:   []byte(fileID),
				Value: eventData,
			},
		)
	}
}

func (d *DownloadService) streamFile(c *gin.Context) {
	// Similar to download but optimized for streaming (e.g., video)
	fileID := c.Param("id")

	// Support range requests for video streaming
	rangeHeader := c.GetHeader("Range")
	if rangeHeader != "" {
		// Implement partial content support (206 response)
		c.JSON(http.StatusNotImplemented, gin.H{
			"message": "Range requests not yet implemented",
			"file_id": fileID, // Now fileID is used
		})
		return
	}

	// For now, just redirect to regular download
	d.downloadFile(c)
}

func (d *DownloadService) getFileInfo(c *gin.Context) {
	fileID := c.Param("id")

	var fileName string
	var fileSize int64
	var chunkCount int
	var status string
	var createdAt time.Time

	err := d.db.QueryRow(`
        SELECT file_name, file_size, chunk_count, status, created_at
        FROM files 
        WHERE file_id = $1
    `, fileID).Scan(&fileName, &fileSize, &chunkCount, &status, &createdAt)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"file_id":      fileID,
		"file_name":    fileName,
		"file_size":    fileSize,
		"chunk_count":  chunkCount,
		"status":       status,
		"created_at":   createdAt,
		"download_url": fmt.Sprintf("/download/%s", fileID),
	})
}

func (d *DownloadService) healthCheck(c *gin.Context) {
	health := gin.H{
		"status":    "healthy",
		"service":   "download",
		"timestamp": time.Now().Unix(),
	}

	// Check MinIO
	if d.minioClient != nil {
		ctx := context.Background()
		_, err := d.minioClient.ListBuckets(ctx)
		if err != nil {
			health["minio"] = "unhealthy"
		} else {
			health["minio"] = "healthy"
		}
	}

	// Check PostgreSQL
	if d.db != nil && d.db.Ping() == nil {
		health["postgres"] = "healthy"
	} else {
		health["postgres"] = "unhealthy"
	}

	health["kafka"] = "configured"

	c.JSON(http.StatusOK, health)
}

func main() {
	log.Println("🚀 Starting AtlasFS Download Service...")

	service := NewDownloadService()
	defer func() {
		if service.kafkaWriter != nil {
			service.kafkaWriter.Close()
		}
		if service.db != nil {
			service.db.Close()
		}
	}()

	service.setupRoutes()

	port := getEnv("PORT", "8085")
	log.Printf("✅ Download Service listening on port %s", port)

	if err := service.router.Run(":" + port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
