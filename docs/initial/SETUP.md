# Go-LLama Setup & Deployment Guide

---

## 1. Local Development Setup

### **Prerequisites**
- Go >=1.20
- Docker & Docker Compose
- git

### **Clone the Repository**
```sh
git clone https://github.com/YOUR_USERNAME/go-llama.git
cd go-llama
```

### **Configuration**
1. Copy sample config:
   ```sh
   cp config.sample.json config.json
   ```
2. Edit `config.json`:
   - DB/Redis info (matches Docker Compose by default)
   - JWT secret (set securely for production)
   - LLM/SearxNG endpoints

### **Start Services**
```sh
docker compose up -d
go run ./cmd/server/main.go
# Or build:
go build -o go-llama-backend ./cmd/server
./go-llama-backend
```
Server runs on `0.0.0.0:8070` under `/go-llama`.

### **Run Tests**
```sh
go test ./internal/... -v
```

---

## 2. Remote Deployment (Debian 12)

### **System Requirements**
- Debian 12 server/VM
- Open ports: 8070 (backend), 5432 (Postgres), 6379 (Redis)
- Go >=1.20
- Docker & Docker Compose

### **Deployment Steps**
1. **Install dependencies**
   ```sh
   sudo apt update
   sudo apt install -y git docker.io docker-compose
   # Install Go
   wget https://go.dev/dl/go1.20.12.linux-amd64.tar.gz
   sudo tar -C /usr/local -xzf go1.20.12.linux-amd64.tar.gz
   export PATH=$PATH:/usr/local/go/bin
   ```
2. **Clone & Configure**
   ```sh
   git clone https://github.com/YOUR_USERNAME/go-llama.git
   cd go-llama
   cp config.sample.json config.json
   # Edit config.json as needed
   ```
3. **Start DB & Redis**
   ```sh
   docker compose up -d
   ```
4. **Run Backend**
   ```sh
   go build -o go-llama-backend ./cmd/server
   ./go-llama-backend
   ```

### **Production Tips**
- Set a secure JWT secret in `config.json`
- Use process supervisor (`systemd`, etc) for backend
- Use Docker volumes for DB/Redis persistence
- Use reverse proxy (nginx, Caddy) for TLS/subpath

### **Backup/Restore**
- Use `docker exec` for `pg_dump`/`pg_restore` and `redis-cli SAVE`

---

## 3. Additional Notes

- Update API endpoints/config as project evolves.
- Reference `openapi/openapi.yaml` for API contract.
- Ensure LLM/SearxNG are reachable from backend.

---

## 4. Troubleshooting

- **Ports in use:** Check nothing else uses 8070, 5432, 6379
- **DB/Redis connection:** Confirm containers and config match
- **JWT/auth:** Double-check secrets, restart after changes

---

## 5. Verify Setup

After setup, confirm by running:

```bash
go test -cover ./...
```

Keep DB/Redis running for full integration tests.


## 6. Streaming & WebSocket Proxying

- If deploying behind nginx or another proxy, ensure `Connection: upgrade` and WebSocket headers are passed correctly.
- Example nginx config:
  ```
  location /go-llama/ws/ {
      proxy_pass http://localhost:8070;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection "upgrade";
      proxy_set_header Host $host;
  }
  ```
- If streaming fails or bubbles show error after stop, check frontend/backend WS disconnect handling.
- References/tokens/sec may require backend/frontend sync.


**For hardening/deployment, see `PROJECT_STATE.md` TODOs.**
