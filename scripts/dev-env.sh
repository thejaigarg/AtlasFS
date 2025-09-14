#!/bin/bash

echo "🔧 AtlasFS Development Environment"
echo "=================================="
echo "1) Local Development (Docker services)"
echo "2) Cloud Development (Port-forward to GKE)"
echo "3) Stop all environments"
echo "4) Clean all (Force stop everything)"
echo ""
read -p "Choose environment (1-4): " choice

case $choice in
  1)
    echo "🐳 Starting local Docker services..."
    
    # Clean up existing services
    echo "Cleaning up existing services..."
    pkill -f "kubectl port-forward" 2>/dev/null
    docker stop redis-local kafka-local minio-local 2>/dev/null
    docker rm redis-local kafka-local minio-local 2>/dev/null
    
    # Wait for ports to be released
    sleep 2
    
    # Start Redis
    echo "Starting Redis..."
    docker run -d --name redis-local -p 6379:6379 redis:7-alpine
    
    # Start Kafka with correct advertised listeners
    echo "Starting Kafka..."
    docker run -d --name kafka-local \
      -p 9092:9092 \
      -p 9093:9093 \
      -e KAFKA_CFG_NODE_ID=0 \
      -e KAFKA_CFG_PROCESS_ROLES=controller,broker \
      -e KAFKA_CFG_LISTENERS=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093 \
      -e KAFKA_CFG_ADVERTISED_LISTENERS=PLAINTEXT://localhost:9092 \
      -e KAFKA_CFG_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
      -e KAFKA_CFG_CONTROLLER_QUORUM_VOTERS=0@localhost:9093 \
      -e KAFKA_CFG_CONTROLLER_LISTENER_NAMES=CONTROLLER \
      -e KAFKA_CFG_INTER_BROKER_LISTENER_NAME=PLAINTEXT \
      -e KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE=true \
      bitnami/kafka:3.5
    
    # Start MinIO
    echo "Starting MinIO..."
    docker run -d --name minio-local \
      -p 9000:9000 -p 9001:9001 \
      -e MINIO_ROOT_USER=minioadmin \
      -e MINIO_ROOT_PASSWORD=minioadmin123 \
      minio/minio server /data --console-address ":9001"
    
    # Wait for services to start
    sleep 3
    
    # Check services
    echo ""
    echo "Checking services..."
    docker ps | grep -E "redis-local|kafka-local|minio-local"
    
    echo ""
    echo "✅ Local services started!"
    echo ""
    echo "Services available at:"
    echo "  Redis:  localhost:6379"
    echo "  Kafka:  localhost:9092"
    echo "  MinIO:  localhost:9000 (Console: localhost:9001)"
    echo ""
    echo "Test with:"
    echo "  redis-cli ping"
    echo "  curl http://localhost:9001"
    ;;
    
  2)
    echo "☁️  Setting up port-forwarding to GKE..."
    
    # Clean up first
    pkill -f "kubectl port-forward" 2>/dev/null
    docker stop redis-local kafka-local minio-local 2>/dev/null
    
    # Authenticate
    gcloud config set project atlas-fs-472018
    gcloud container clusters get-credentials atlas-fs-472018 --region us-central1
    
    # Start port forwards
    echo "Starting port forwards..."
    kubectl port-forward -n atlasfs svc/redis 6379:6379 > /dev/null 2>&1 &
    kubectl port-forward -n atlasfs svc/kafka-headless 9092:9092 > /dev/null 2>&1 &
    kubectl port-forward -n atlasfs svc/minio 9000:9000 > /dev/null 2>&1 &
    kubectl port-forward -n atlasfs svc/minio 9001:9001 > /dev/null 2>&1 &
    
    sleep 3
    
    echo "✅ Port forwarding established!"
    echo ""
    echo "Services forwarded from GKE:"
    echo "  Redis:  localhost:6379 -> GKE Redis"
    echo "  Kafka:  localhost:9092 -> GKE Kafka"
    echo "  MinIO:  localhost:9000 -> GKE MinIO"
    ;;
    
  3)
    echo "🛑 Stopping all environments..."
    docker stop redis-local kafka-local minio-local 2>/dev/null
    docker rm redis-local kafka-local minio-local 2>/dev/null
    pkill -f "kubectl port-forward" 2>/dev/null
    echo "✅ All environments stopped"
    ;;
    
  4)
    echo "🧹 Force cleaning everything..."
    pkill -f "kubectl port-forward" 2>/dev/null
    docker stop $(docker ps -aq) 2>/dev/null
    docker rm $(docker ps -aq) 2>/dev/null
    lsof -ti:6379 | xargs kill -9 2>/dev/null
    lsof -ti:9092 | xargs kill -9 2>/dev/null
    lsof -ti:9000 | xargs kill -9 2>/dev/null
    lsof -ti:9001 | xargs kill -9 2>/dev/null
    echo "✅ Everything force cleaned!"
    ;;
    
  *)
    echo "Invalid choice"
    exit 1
    ;;
esac
