// services/gateway/main.go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"
    "os"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/go-redis/redis/v8"
)

type GatewayService struct {
    redisClient *redis.Client
    router      *gin.Engine
}

func NewGatewayService() *GatewayService {
    // Get Redis address from environment or use default
    redisAddr := os.Getenv("REDIS_ADDR")
    if redisAddr == "" {
        redisAddr = "localhost:6379" // For local development
    }

    // For GKE deployment, use service name
    if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
        redisAddr = "redis:6379"
    }

    log.Printf("Connecting to Redis at: %s", redisAddr)

    redisClient := redis.NewClient(&redis.Options{
        Addr:     redisAddr,
        Password: "",
        DB:       0,
    })

    // Test Redis connection
    ctx := context.Background()
    pong, err := redisClient.Ping(ctx).Result()
    if err != nil {
        log.Printf("Warning: Could not connect to Redis: %v", err)
    } else {
        log.Printf("Redis connected successfully: %s", pong)
    }

    return &GatewayService{
        redisClient: redisClient,
        router:      gin.Default(),
    }
}

func (g *GatewayService) setupRoutes() {
    // Health check endpoint
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
    g.router.GET("/metrics", g.getMetrics)
}

func (g *GatewayService) home(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "service": "AtlasFS Gateway",
        "version": "1.0.0",
        "status": "running",
        "endpoints": []string{
            "GET /health",
            "GET /test/redis", 
            "POST /api/v1/files",
            "GET /api/v1/files",
            "GET /metrics",
        },
    })
}

func (g *GatewayService) healthCheck(c *gin.Context) {
    health := gin.H{
        "status":    "healthy",
        "service":   "gateway",
        "timestamp": time.Now().Unix(),
        "version":   os.Getenv("VERSION"),
    }

    // Check Redis health
    ctx := context.Background()
    _, err := g.redisClient.Ping(ctx).Result()
    if err != nil {
        health["redis"] = "unhealthy"
        health["error"] = err.Error()
        c.JSON(http.StatusServiceUnavailable, health)
        return
    }
    health["redis"] = "healthy"

    c.JSON(http.StatusOK, health)
}

func (g *GatewayService) uploadFile(c *gin.Context) {
    file, header, err := c.Request.FormFile("file")
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "No file provided",
        })
        return
    }
    defer file.Close()

    // Generate file ID
    fileID := fmt.Sprintf("file_%d", time.Now().UnixNano())
    
    // Store in Redis
    ctx := context.Background()
    sessionKey := fmt.Sprintf("upload:%s", fileID)
    
    g.redisClient.HSet(ctx, sessionKey, map[string]interface{}{
        "filename": header.Filename,
        "size":     header.Size,
        "status":   "uploading",
        "uploaded": time.Now().Unix(),
    })
    g.redisClient.Expire(ctx, sessionKey, 2*time.Hour)

    c.JSON(http.StatusAccepted, gin.H{
        "file_id":  fileID,
        "filename": header.Filename,
        "size":     header.Size,
        "status":   "accepted",
        "message":  "File upload initiated",
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
    
    // Get all upload keys
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
        "status": "Redis working!",
        "test_key": testKey,
        "test_value": value,
        "message": "Successfully wrote and read from Redis",
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
    log.Println("Starting AtlasFS Gateway Service...")
    
    service := NewGatewayService()
    service.setupRoutes()
    
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    
    log.Printf("Gateway Service listening on port %s", port)
    log.Printf("Visit http://localhost:%s for API info", port)
    
    if err := service.router.Run(":" + port); err != nil {
        log.Fatal("Failed to start server:", err)
    }
}