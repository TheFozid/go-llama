# Go-LLama

Local, private, fast LLM chat interface and orchestrator with optional web search via SearxNG.  
Designed for self-hosting on everything from low-power mini-PCs to servers.  
Outperforms open-webUI due to being lighter on system resources.  
Multi-architecture prebuilds that are auto pulled via docker compose are AMD64, ARM64 and ARMv7

---

## Overview

Go-LLama is a modern, secure, and high-performance chat interface for local LLMs such as llama.cpp and llamafile.  
It provides user accounts, persistent chats, streaming responses, and automatic web search for real-time answers.

This project prioritizes:

- Low resource usage  
- Fast response time even on low-end hardware  
- Privacy and self-hosting  
- Simplicity and stability  

---

## Key Features

### LLM Interaction
- Connects to local llama.cpp, llamafile, or compatible REST LLM endpoints  
- True streaming token output with tokens-per-second display  
- Session reuse when supported by the model backend  
- Automatic context window management  

### Web Search (SearxNG)
- Search triggers automatically when relevant  
- User can say “search the web…” or “do not search the web”  
- Results injected into context before answering  
- Sources shown at end of answer  

### Authentication & Users
- JWT-based authentication  
- Admin & standard users  
- User management UI  
- Private chat history  

### UI / UX
- Clean Bootstrap interface  
- Mobile-friendly  
- Streaming bubbles + Stop button  

### Deployment
- Works under a subpath (default `/go-llama`)  
- Docker: PostgreSQL + Redis + Backend  
- OpenAPI spec included  

---

## Performance Focus

Runs fast even on low-power hardware:

- Efficient Go WebSocket streaming  
- Minimal JavaScript footprint  
- Very low memory usage (~50MB)  
- SearxNG only when needed  
- No idle background workers  

---

## Screenshots
See `/screenshots` folder.

---

## Installation (Docker)

**You do not need to clone the repo to run Go-LLama.**

```
# Download compose and config templates
curl -L -o docker-compose.yml https://raw.githubusercontent.com/TheFozid/go-llama/main/docker-compose.yml.sample
curl -L -o config.json https://raw.githubusercontent.com/TheFozid/go-llama/main/config.sample.json

# Edit config with your DB, Redis, LLM and SearxNG settings
nano config.json

# Start the services
docker compose up -d
```

Application URL:  
**http://localhost:8070/go-llama**

---

## Updating

To update to the latest version:

```
docker compose pull
docker compose up -d
```

Your data stored in Docker volumes will be preserved.

---

## Reverse Proxy (Nginx)

```
location /go-llama {
    proxy_pass http://localhost:8070/go-llama;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_buffering off;
}
```

---

## Configuration

Edit **config.json** (generated from config.sample.json).  
Configure:

- Server path & security settings  
- PostgreSQL  
- Redis  
- LLM endpoints  
- SearxNG URL & result count  

---

## Troubleshooting

**Frontend not loading**  
Ensure static files available in container

**Web search not working**  
Verify SearxNG URL in config.json

**Streaming not working**  
Check reverse-proxy WebSocket headers

**Logs**
```
docker compose logs -f go-llama-backend
```

---

## License

MIT License — see `LICENSE`.

---

## Contribution

Pull requests welcome.  
Ideas for performance, UX, and routing improvements encouraged.
