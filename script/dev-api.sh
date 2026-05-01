#!/bin/bash

# Build binary every time
echo "Building market-services..."
go build -o market-services ./cmd/market-services

if [ $? -ne 0 ]; then
    echo "Build failed"
    exit 1
fi

# Load env
if [ -f .env ]; then
    echo "Loading .env..."
    source .env
fi

# Run API
echo "Starting market-services api..."
./market-services api
