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

### LLM Interaction
* Connects to **local LLM APIs** (vLLM, llama.cpp, llamafile, etc.)
* **True streaming output** with tokens-per-second metrics  
* **Session reuse** for models that support incremental conversation memory  
* **Automatic context trimming** to stay within model limits  
* Per-user chat isolation and persistent history

---

### Intelligent Web Search (SearxNG Integration)

Go-LLama's search pipeline is more than a simple fetch-and-dump of results:

1. **Smart Auto-Search Trigger**  
   - Searches the web automatically when the user's question implies a need for current or factual data (dates, tickers, "latest", etc.)  
   - User can force or block searches naturally via phrases like *"search the web for…"* or *"don't search online"*.

2. **Result Ranking & Filtering**  
   - Raw SearxNG results are **ranked by semantic relevance** to the query.  
   - Irrelevant or low-content hits are dropped automatically.  
   - Only the **top 50 % of the most relevant** results (respecting your configured limit) are retained.

3. **LLM-Optimised Context Assembly**  
   - Summaries are formatted into a concise, numbered context block fed directly into the model prompt.
   - The LLM is instructed to answer using those references and cite them inline (`[1]`, `[2]`, …).  
   - The user sees a **clean, cited answer** with expandable **source links** appended.

This process produces higher-quality responses than naive web-injection — fast, relevant, and grounded without overwhelming the model.

---

### GrowerAI - Perpetual Learning System (Experimental)

**WARNING: GrowerAI is in active development and considered highly experimental.**

GrowerAI is an optional perpetual learning system that transforms the LLM from a stateless chatbot into an entity that learns and evolves through continuous experience. Instead of isolated chat sessions, it maintains a single continuous memory substrate across all interactions.

**Key concepts:**
* **Space-based memory management** - Stores up to 1,000,000 memories (~2.7 GB) with intelligent compression when capacity limits are reached
* **Tiered memory architecture** - Recent → Medium → Long → Ancient, with automatic compression and concept extraction
* **Good/bad outcome learning** - Every interaction is tagged and the system learns from what works and what doesn't
* **Dynamic principles (10 Commandments)** - Replaces static system prompts with learned behavioral rules that evolve over time
* **Neural memory linking** - Memories form a knowledge graph, enabling associative retrieval and pattern recognition

**Configuration:**

Enable GrowerAI by selecting it when creating a new chat in the UI. Configure in `config.json`:

```json
"growerai": {
  "storage_limits": {
    "max_total_memories": 1000000,
    "compression_trigger": 0.90
  },
  "compression": {
    "enabled": true,
    "schedule_hours": 24
  },
  "principles": {
    "evolution_schedule_hours": 168
  }
}
```

**Requirements:**
* Qdrant vector database (included in docker-compose)
* Embedding model (all-MiniLM-L6-v2 recommended)
* Additional ~3-5 GB disk space for full memory capacity

**Status:** Under heavy active development. Core functionality is operational but expect breaking changes, incomplete features, and rough edges. Use for experimentation only.

For detailed documentation, see `growerai_brief.md` and `growerai_progress-log.md` in the project root.

---

### Authentication & User System

* JWT-based authentication  
* Admin and standard user roles  
* Built-in user management endpoints  
* Private, per-user chat storage  

---

### UI & UX

* Clean Bootstrap-based interface  
* Mobile-friendly layout  
* Real-time streaming message bubbles  
* Manual **Stop Generation** button  
* Optional **auto-search notification** when triggered dynamically  

---

### Deployment

* Runs under a **custom sub-path** (useful for reverse proxies)
* **Docker-ready** stack: PostgreSQL + Redis + Go-based backend  
* **OpenAPI spec** included for integration and extension  

---

## Performance Focus

Optimised for low-power devices with:

* Efficient Go concurrency for streaming and background fetches  
* Minimal JavaScript footprint  
* Very low RAM footprint (~50 MB idle)  
* On-demand SearxNG queries only when needed  

The result: an interface that feels instant even on hardware where most AI dashboards crawl.

---

## Screenshots

<p align="center">
  <img src="/screenshots/Screenshot1.jpg" alt="Screenshot 1" width="200" />
  <img src="/screenshots/Screenshot2.jpg" alt="Screenshot 2" width="200" />
  <img src="/screenshots/Screenshot3.jpg" alt="Screenshot 3" width="200" />
</p>
<p align="center">
  <img src="/screenshots/Screenshot4.jpg" alt="Screenshot 4" width="200" />
  <img src="/screenshots/Screenshot5.jpg" alt="Screenshot 5" width="200" />
  <img src="/screenshots/Screenshot6.jpg" alt="Screenshot 6" width="200" />
</p>
<p align="center">
  <img src="/screenshots/Screenshot7.jpg" alt="Screenshot 7" width="200" />
  <img src="/screenshots/Screenshot8.jpg" alt="Screenshot 8" width="200" />
  <img src="/screenshots/Screenshot9.jpg" alt="Screenshot 9" width="200" />
</p>

---

## Installation (Docker)

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

## Updating

To update to the latest version:

```bash
docker compose pull
docker compose up -d
```

All user data in Docker volumes is preserved across updates.

<p align="center">
  <a href="https://buymeacoffee.com/danny_and_serin">
    <img src="https://www.buymeacoffee.com/assets/img/custom_images/yellow_img.png" alt="Buy Me A Coffee">
  </a>
</p>
