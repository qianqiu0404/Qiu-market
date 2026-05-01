# Local Run Guide

## Prerequisites
- PostgreSQL 14+
- Redis 6+
- Go 1.21+
- Node.js 18+

## Step 1: Database Setup
1. Create database: `createdb s78_market`
2. Run migrations: `psql -d s78_market -f migrations/2026032500001.sql`
3. Seed test data: `psql -d s78_market -f script/seed-dashboard.sql`

## Step 2: Environment Variables
The following environment variables are required for the API service:
```bash
export MARKET_RPC_HOST=127.0.0.1
export MARKET_RPC_PORT=50051
export MARKET_HTTP_HOST=127.0.0.1
export MARKET_HTTP_PORT=8080
export MARKET_MASTER_DB_HOST=127.0.0.1
export MARKET_MASTER_DB_PORT=5432
export MARKET_MASTER_DB_USER=$(whoami)
export MARKET_MASTER_DB_PASSWORD=""
export MARKET_MASTER_DB_NAME=s78_market
export MARKET_REDIS_ADDRESS=127.0.0.1:6379
```

## Step 3: Start API Service
```bash
bash script/dev-api.sh
```

## Step 4: Start Frontend
```bash
cd frontend
npm install
npm run dev
```

## Step 5: Verification
```bash
curl -X POST -H "Content-Type: application/json" \
-d '{"ConsumerToken":"test_token","Page":1,"PageSize":10}' \
http://127.0.0.1:8080/api/v1/get_market_dashboard
```

## Troubleshooting
- **Port 8080 already in use**: `lsof -i :8080` and `kill -9 <PID>`
- **Database connection failed**: Ensure PostgreSQL is running and credentials are correct.
