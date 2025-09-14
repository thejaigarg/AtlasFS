// services/common/config/config.go
package config

import (
	"os"
	"strconv"
)

type Config struct {
	// Redis
	RedisAddr string

	// Kafka
	KafkaBrokers []string

	// PostgreSQL - Add all the fields gateway expects
	PostgresHost     string
	PostgresPort     int
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string

	// MinIO
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioUseSSL    bool

	// Service
	Port        string
	Environment string
}

func Load() *Config {
	// Detect if running in Kubernetes
	isK8s := os.Getenv("KUBERNETES_SERVICE_HOST") != ""

	cfg := &Config{
		Environment: getEnv("ENVIRONMENT", "local"),
		Port:        getEnv("PORT", "8080"),
	}

	// Redis configuration
	if isK8s {
		cfg.RedisAddr = "redis:6379"
	} else {
		cfg.RedisAddr = getEnv("REDIS_ADDR", "localhost:6379")
	}

	// Kafka configuration
	if isK8s {
		cfg.KafkaBrokers = []string{"kafka-headless:9092"}
	} else {
		cfg.KafkaBrokers = []string{getEnv("KAFKA_BROKERS", "localhost:9092")}
	}

	// PostgreSQL configuration - Add all fields
	cfg.PostgresHost = getEnv("POSTGRES_HOST", "104.197.234.56")
	cfg.PostgresPort = getEnvInt("POSTGRES_PORT", 5432)
	cfg.PostgresUser = getEnv("POSTGRES_USER", "postgres")
	cfg.PostgresPassword = getEnv("POSTGRES_PASSWORD", "LawdaYaadNahiRahega")
	cfg.PostgresDB = getEnv("POSTGRES_DB", "atlasfs")

	// MinIO configuration
	if isK8s {
		cfg.MinioEndpoint = "minio:9000"
	} else {
		cfg.MinioEndpoint = getEnv("MINIO_ENDPOINT", "localhost:9000")
	}
	cfg.MinioAccessKey = getEnv("MINIO_ACCESS_KEY", "minioadmin")
	cfg.MinioSecretKey = getEnv("MINIO_SECRET_KEY", "minioadmin123")
	cfg.MinioUseSSL = getEnvBool("MINIO_USE_SSL", false)

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}
