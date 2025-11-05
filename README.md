# Go-LLama

Local, private, fast LLM chat interface and orchestrator with optional web search via SearxNG.  
Designed for self-hosting on everything from low-power mini-PCs to servers.
Outperforms open-webUI due to being lighter on system resources.

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
- True streaming token output, with tokens-per-second display
- Session reuse when supported by the model backend
- Automatic context window management

### Web Search (SearxNG)
- No manual toggle required
- Search triggers automatically when relevant to the question
- User can override via natural language:
  - "search the web for ..."
  - "do not search the web"
- Search results are fed to the LLM before answering
- Sources are appended at the end of responses

### Authentication & User System
- JWT-based login
- Admin and standard user roles
- User management UI
- Private chat history

### UI & UX
- Bootstrap clean UI
- Mobile-friendly
- Streaming response bubbles
- Stop generation button

### Deployment
- Works under a subpath (default /go-llama)
- Docker-ready: PostgreSQL + Redis + Backend
- OpenAPI spec included

---

## Performance Focus

Go-LLama is optimized to run efficiently even on low power hardware such as:

- Intel N97 / N100 mini PCs
- Small ARM SBCs (Raspberry Pi class, with small models)
- Older laptops and low wattage home servers

Performance strategies include:

- Efficient Go WebSocket streaming
- Minimal JavaScript execution
- Very low server memory footprint (Only 50mb of RAM)
- On-demand SearxNG triggering instead of always searching
- No background cron workers or idle overhead

Result: fast UI response and chat flow, even with very small CPUs.

---

## Screenshots

Screenshots are available in the screenshots directory.

---

## Installation (Docker)
```
git clone https://github.com/TheFozid/go-llama.git
cd go-llama

cp config.sample.json config.json
(edit the config.json file with your searxng url, searxng results per query, llm servers and security detail.)
```
# Edit config.json
```
docker compose up --build -d
```
Application URL:
http://localhost:8070/go-llama

---

## Upgrading
```
cd go-llama
git pull
docker compose down
docker compose up --build -d
```
If you maintain chat history or user accounts, do not delete your database volume.  
Check PROJECT_STATE.md for major changes.

---

## Reverse Proxy (Nginx example)

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

Config file: config.json  
Sample file: config.sample.json

Important settings include:

- Subpath
- Database and Redis connection
- Model list and endpoints
- SearxNG settings (URL and max results)

---

## Troubleshooting

Frontend not loading:
Ensure static and frontend files are in the container.

Web search not working:
Confirm SearxNG URL is reachable and referenced in config.json.

Streaming not working:
Check nginx reverse proxy headers.

Logs:
docker compose logs go-llama-backend

---

## License

MIT License — see LICENSE.

---

## Contribution

Pull requests welcome.  
Ideas and improvements encouraged: performance, UI, and model routing.
