# Go-LLama

Local, private, fast LLM chat interface and orchestrator with optional web search via SearxNG.  
Designed for self-hosting on everything from low-power mini-PCs to servers.  
Outperforms open-webUI due to being lighter on system resources.  

---

## Overview

Go-LLama is a modern, secure, and high-performance chat interface for local LLMs such as vLLM, llama.cpp and llamafile.  
It provides user accounts, persistent chats, streaming responses, and automatic web search for real-time answers.  

This project prioritizes:  

* Low resource usage (~50mb ram)
* Fast response time even on low-end hardware
* Privacy and self-hosting
* Simplicity and stability

---

## Key Features

### LLM Interaction

* Connects to local vLLM, llama.cpp, llamafile, or compatible REST LLM endpoints  
* True streaming token output, with tokens-per-second display  
* Session reuse when supported by the model backend  
* Automatic context window management  

### Web Search (SearxNG)

* Search triggers automatically when relevant to the question  
* User can override via natural language  
* Search results feed into LLM before answering  
* Sources appended to responses  
* Can be disabled in config.json by setting results to 0  

### Authentication & User System

* JWT-based login
* Admin and standard users
* User management UI
* Private chat history

### UI & UX

* Bootstrap UI, mobile friendly
* Streaming response bubbles
* Stop generation button

### Deployment

* Works under a subpath
* Docker-ready: PostgreSQL + Redis + Backend
* OpenAPI spec included

---

## Performance Focus

Optimized to run on low-power hardware with:

* Efficient streaming
* Minimal JS
* Very low RAM use (~50MB)
* On-demand SearxNG

---

## Screenshots

Available in /screenshots

---

## Installation (Docker)

Install without cloning the repository:

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
[http://localhost:8070/go-llama](http://localhost:8070/go-llama)

## Updating

To update to the latest version:

```
docker compose pull
docker compose up -d
```

Your data stored in Docker volumes will be preserved.
