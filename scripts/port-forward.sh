#!/bin/bash

echo "🔗 Setting up port forwarding to GKE services..."
echo "Press Ctrl+C to stop all port forwards"

# Make sure we're logged in to GCP
gcloud auth login
gcloud config set project atlas-fs-472018

# Get cluster credentials
gcloud container clusters get-credentials atlas-fs-472018 --region us-central1

# Port forward all services
echo "Forwarding Redis (6379)..."
kubectl port-forward -n atlasfs svc/redis 6379:6379 &

echo "Forwarding Kafka (9092)..."
kubectl port-forward -n atlasfs svc/kafka-headless 9092:9092 &

echo "Forwarding MinIO API (9000)..."
kubectl port-forward -n atlasfs svc/minio 9000:9000 &

echo "Forwarding MinIO Console (9001)..."
kubectl port-forward -n atlasfs svc/minio 9001:9001 &

echo "✅ All services forwarded!"
echo "Redis:    localhost:6379"
echo "Kafka:    localhost:9092"
echo "MinIO:    localhost:9000"
echo "Console:  localhost:9001"

# Wait for Ctrl+C
wait
