# Go-LLama

Local, private, and fast LLM chat interface and orchestrator — with optional intelligent web search via SearxNG.  
Built for self-hosting on everything from Raspberry Pi and low-power mini-PCs to full servers.  
Lightweight, secure, and purpose-built to outperform bloated open-web UI stacks.

---

## Overview

Go-LLama is a modern, high-performance chat interface for local LLM backends such as **vLLM**, **llama.cpp**, and **llamafile**.  
It provides user accounts, persistent chat history, streaming responses, and context-aware web search for real-time information.  

This project prioritizes:

* **Low resource usage** (~50 MB RAM typical)
* **Fast response times**, even on low-end hardware
* **Privacy**, **simplicity**, and **stability**
* **Complete offline operation** when web search is disabled

---

## Key Features

### 🧠 LLM Interaction
* Connects to **local LLM APIs** (vLLM, llama.cpp, llamafile, etc.)
* **True streaming output** with tokens-per-second metrics  
* **Session reuse** for models that support incremental conversation memory  
* **Automatic context trimming** to stay within model limits  
* Per-user chat isolation and persistent history

---

### 🌐 Intelligent Web Search (SearxNG Integration)

Go-LLama’s search pipeline is more than a simple fetch-and-dump of results:

1. **Smart Auto-Search Trigger**  
   - Searches the web automatically when the user’s question implies a need for current or factual data (dates, tickers, “latest”, etc.)  
   - User can force or block searches naturally via phrases like *“search the web for…”* or *“don’t search online”*.

2. **Result Ranking & Filtering**  
   - Raw SearxNG results are **ranked by semantic relevance** to the query.  
   - Irrelevant or low-content hits are dropped automatically.  
   - Only the **top 50 % of the most relevant** results (respecting your configured limit) are retained.

3. **Content Extraction & Enrichment**  
   - Each remaining result is **visited** and its **full HTML content extracted** (not just the snippet).  
   - Boilerplate and noise are stripped; the core text is identified.  
   - Extracted text is **summarised and compressed** into a short, LLM-optimized snippet.

4. **LLM-Optimised Context Assembly**  
   - Summaries are formatted into a concise, numbered context block fed directly into the model prompt.  
   - The LLM is instructed to answer using those references and cite them inline (`[1]`, `[2]`, …).  
   - The user sees a **clean, cited answer** with expandable **source links** appended.

This process produces higher-quality responses than naive web-injection — fast, relevant, and grounded without overwhelming the model.

---

### 🔐 Authentication & User System

* JWT-based authentication  
* Admin and standard user roles  
* Built-in user management endpoints  
* Private, per-user chat storage  

---

### 💬 UI & UX

* Clean Bootstrap-based interface  
* Mobile-friendly layout  
* Real-time streaming message bubbles  
* Manual **Stop Generation** button  
* Optional **auto-search notification** when triggered dynamically  

---

### 🧱 Deployment

* Runs under a **custom sub-path** (useful for reverse proxies)
* **Docker-ready** stack: PostgreSQL + Redis + Go-based backend  
* **OpenAPI spec** included for integration and extension  

---

## ⚡ Performance Focus

Optimised for low-power devices with:

* Efficient Go concurrency for streaming and background fetches  
* Minimal JavaScript footprint  
* Very low RAM footprint (~50 MB idle)  
* On-demand SearxNG queries only when needed  

The result: an interface that feels instant even on hardware where most AI dashboards crawl.

---

## 📸 Screenshots

See the `/screenshots` directory.

---

## 🐳 Installation (Docker)

Install without cloning the repository:

```bash
# Download templates
curl -L -o docker-compose.yml https://raw.githubusercontent.com/TheFozid/go-llama/main/docker-compose.yml.sample
curl -L -o config.json https://raw.githubusercontent.com/TheFozid/go-llama/main/config.sample.json

# Edit with your DB, Redis, LLM, and SearxNG settings
nano config.json
nano docker-compose.yml

# Launch
docker compose up -d
```

Application URL:  
[http://localhost:8070/go-llama](http://localhost:8070/go-llama)

---

## 🔄 Updating

To update to the latest version:

```bash
docker compose pull
docker compose up -d
```

All user data in Docker volumes is preserved across updates.
