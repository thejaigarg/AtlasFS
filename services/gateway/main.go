// services/gateway/main.go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"

	// Use relative imports
	"atlasfs/services/common/config"
	"atlasfs/services/common/events"
)

type GatewayService struct {
	config      *config.Config
	redisClient *redis.Client
	kafkaWriter *kafka.Writer
	db          *sql.DB
	router      *gin.Engine
}

func NewGatewayService() *GatewayService {
	cfg := config.Load()

	// Initialize Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: "",
		DB:       0,
	})

	// Test Redis connection
	ctx := context.Background()
	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		log.Printf("Warning: Could not connect to Redis: %v", err)
	} else {
		log.Printf("✅ Redis connected at %s", cfg.RedisAddr)
	}

	// Initialize Kafka writer
	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(cfg.KafkaBrokers...),
		Topic:    "file.events",
		Balancer: &kafka.LeastBytes{},
	}
	log.Printf("✅ Kafka writer initialized for brokers: %v", cfg.KafkaBrokers)

	// Initialize PostgreSQL
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPassword, cfg.PostgresDB)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Printf("Warning: Could not connect to PostgreSQL: %v", err)
	} else {
		if err = db.Ping(); err != nil {
			log.Printf("Warning: PostgreSQL ping failed: %v", err)
		} else {
			log.Printf("✅ PostgreSQL connected at %s", cfg.PostgresHost)
		}
	}

	return &GatewayService{
		config:      cfg,
		redisClient: redisClient,
		kafkaWriter: kafkaWriter,
		db:          db,
		router:      gin.Default(),
	}
}

func (g *GatewayService) setupRoutes() {
	// Health and info endpoints
	g.router.GET("/", g.home)
	g.router.GET("/health", g.healthCheck)

	// File operations
	api := g.router.Group("/api/v1")
	{
		api.POST("/files", g.uploadFile)
		api.GET("/files/:id", g.getFile)
		api.GET("/files", g.listFiles)
		api.DELETE("/files/:id", g.deleteFile)
	}

	// Test endpoints
	g.router.GET("/test/redis", g.testRedis)
	g.router.GET("/test/kafka", g.testKafka)
	g.router.GET("/test/postgres", g.testPostgres)
	g.router.GET("/metrics", g.getMetrics)
}

func (g *GatewayService) uploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file provided"})
		return
	}
	defer file.Close()

	fileID := fmt.Sprintf("file_%d", time.Now().UnixNano())

	// Store initial metadata in PostgreSQL
	if g.db != nil {
		query := `
            INSERT INTO files (file_id, file_name, file_size, status, user_id, created_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
        `
		_, dbErr := g.db.Exec(query, fileID, header.Filename, header.Size,
			"uploading", "anonymous", time.Now(), time.Now())
		if dbErr != nil {
			log.Printf("Failed to insert into PostgreSQL: %v", dbErr)
		}
	}

	// Forward to upload service for actual processing
	uploadURL := "http://upload:8081/upload"

	// Create a new multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file
	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create form"})
		return
	}

	// Reset file reader to beginning
	file.Seek(0, 0)

	// Copy file data
	_, err = io.Copy(part, file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to copy file"})
		return
	}

	// ADD THIS: Pass the file_id to upload service
	err = writer.WriteField("file_id", fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add file_id"})
		return
	}

	err = writer.Close()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to close writer"})
		return
	}

	// Create request to upload service
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Forward to upload service
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to forward to upload service: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Upload service unavailable"})
		return
	}
	defer resp.Body.Close()

	// Read response from upload service
	var uploadResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse upload response"})
		return
	}

	// Return the upload service response
	c.JSON(http.StatusAccepted, uploadResponse)
}

func (g *GatewayService) testKafka(c *gin.Context) {
	// Publish test event
	event := events.NewEvent(
		"test.event",
		"gateway",
		map[string]interface{}{
			"test":      true,
			"timestamp": time.Now().Unix(),
		},
	)

	eventData, _ := event.ToJSON()
	err := g.kafkaWriter.WriteMessages(context.Background(),
		kafka.Message{
			Key:   []byte("test"),
			Value: eventData,
		},
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "Kafka working!",
		"message": "Successfully published test event",
		"event":   event,
	})
}

func (g *GatewayService) testPostgres(c *gin.Context) {
	if g.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "PostgreSQL not connected",
		})
		return
	}

	var version string
	err := g.db.QueryRow("SELECT version()").Scan(&version)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "PostgreSQL working!",
		"version":  version,
		"database": g.config.PostgresDB,
	})
}

func (g *GatewayService) healthCheck(c *gin.Context) {
	health := gin.H{
		"status":    "healthy",
		"service":   "gateway",
		"timestamp": time.Now().Unix(),
		"version":   "2.0.0", // Updated version
	}

	// Check all connections
	allHealthy := true

	// Redis health
	ctx := context.Background()
	if _, err := g.redisClient.Ping(ctx).Result(); err != nil {
		health["redis"] = "unhealthy"
		allHealthy = false
	} else {
		health["redis"] = "healthy"
	}

	// PostgreSQL health
	if g.db == nil || g.db.Ping() != nil {
		health["postgres"] = "unhealthy"
		allHealthy = false
	} else {
		health["postgres"] = "healthy"
	}

	// Kafka health (basic check)
	health["kafka"] = "configured"

	if !allHealthy {
		c.JSON(http.StatusServiceUnavailable, health)
		return
	}

	c.JSON(http.StatusOK, health)
}

// Add these methods to your gateway service

func (g *GatewayService) home(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"service": "AtlasFS Gateway",
		"version": "2.0.0",
		"status":  "running",
		"endpoints": []string{
			"GET /health",
			"GET /test/redis",
			"GET /test/kafka",
			"GET /test/postgres",
			"POST /api/v1/files",
			"GET /api/v1/files",
			"GET /api/v1/files/:id",
			"DELETE /api/v1/files/:id",
			"GET /metrics",
		},
	})
}

func (g *GatewayService) getFile(c *gin.Context) {
	fileID := c.Param("id")

	ctx := context.Background()
	sessionKey := fmt.Sprintf("upload:%s", fileID)

	data := g.redisClient.HGetAll(ctx, sessionKey).Val()
	if len(data) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "File not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"file_id": fileID,
		"data":    data,
	})
}

func (g *GatewayService) listFiles(c *gin.Context) {
	ctx := context.Background()

	// Get all upload keys from Redis
	keys := g.redisClient.Keys(ctx, "upload:*").Val()

	files := []gin.H{}
	for _, key := range keys {
		data := g.redisClient.HGetAll(ctx, key).Val()
		if len(data) > 0 {
			fileID := key[7:] // Remove "upload:" prefix
			files = append(files, gin.H{
				"file_id":  fileID,
				"filename": data["filename"],
				"size":     data["size"],
				"status":   data["status"],
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"files": files,
		"count": len(files),
	})
}

func (g *GatewayService) deleteFile(c *gin.Context) {
	fileID := c.Param("id")

	ctx := context.Background()
	sessionKey := fmt.Sprintf("upload:%s", fileID)

	deleted := g.redisClient.Del(ctx, sessionKey).Val()
	if deleted == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "File not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"file_id": fileID,
		"message": "File deleted successfully",
	})
}

func (g *GatewayService) testRedis(c *gin.Context) {
	ctx := context.Background()

	// Test write
	testKey := fmt.Sprintf("test:%d", time.Now().UnixNano())
	err := g.redisClient.Set(ctx, testKey, "test-value", 10*time.Second).Err()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Test read
	value := g.redisClient.Get(ctx, testKey).Val()

	// Clean up
	g.redisClient.Del(ctx, testKey)

	c.JSON(http.StatusOK, gin.H{
		"status":     "Redis working!",
		"test_key":   testKey,
		"test_value": value,
		"message":    "Successfully wrote and read from Redis",
	})
}

func (g *GatewayService) getMetrics(c *gin.Context) {
	ctx := context.Background()

	// Get Redis info
	info := g.redisClient.Info(ctx, "stats").Val()
	dbSize := g.redisClient.DBSize(ctx).Val()

	c.JSON(http.StatusOK, gin.H{
		"service":     "gateway",
		"uptime":      time.Now().Unix(),
		"redis_keys":  dbSize,
		"redis_stats": info,
	})
}

func main() {
	log.Println("🚀 Starting AtlasFS Gateway Service v2.0...")

	service := NewGatewayService()
	defer service.kafkaWriter.Close()
	defer service.db.Close()

	service.setupRoutes()

	port := service.config.Port
	log.Printf("✅ Gateway Service listening on port %s", port)
	log.Printf("📍 Visit http://localhost:%s for API info", port)

	if err := service.router.Run(":" + port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
