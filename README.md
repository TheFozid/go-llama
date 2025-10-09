# Go-LLama

![Go-Llama Logo](static/Go-Llama-logo.png)

---

## Modern Local LLM Chat Web App

Go-LLama is a fully self-hosted, modern chat UI for local LLMs (llama.cpp/llamafile), featuring user authentication, persistent chat history, live streaming, and optional web search via SearxNG.  
**Deploy on your own hardware, scale as needed, and keep your data private.**

---

## Features

- **Local LLM chat:** Connects to llama.cpp/llamafile and compatible APIs.  
- **User authentication:** JWT-based login, admin/user roles, session expiry.
- **Persistent chat history:** Each user gets private chat history, can rename/delete chats.
- **Live streaming:** WebSocket-powered, tokens/sec display, stop button.
- **Web search integration:** Toggle SearxNG results in chat for up-to-date answers.
- **User management:** Admins can add/edit/delete users; users manage their own accounts.
- **Responsive front-end:** Bootstrap UI, mobile-friendly, tooltips, icons, transitions.
- **Configurable subpath:** Deploy under any subpath (default: `/go-llama`).
- **Dockerized database & cache:** PostgreSQL, Redis, and backend via Docker Compose.
- **OpenAPI spec:** See [openapi/openapi.yaml](openapi/openapi.yaml) for full API documentation.

---

## Quickstart (Dockerized Deployment)

1. **Clone this repo:**
    ```sh
    git clone https://github.com/TheFozid/go-llama.git
    cd go-llama
    ```

2. **Copy & edit config:**
    ```sh
    cp config.sample.json config.json
    # Edit config.json for your setup (see comments inside)
    # For Docker Compose, use: host=postgres (not localhost) for database, addr=redis:6379 for Redis
    ```

3. **Build and start all services (backend, database, cache):**
    ```sh
    docker compose up --build -d
    ```

4. **Open in browser:**  
   Visit [http://localhost:8070/go-llama](http://localhost:8070/go-llama) (or your configured subpath).

---

## Reverse Proxy Example (Nginx)

To expose Go-LLama securely or behind a custom domain, use Nginx with a location block:

```nginx
location /go-llama/ {
    proxy_pass         http://localhost:8070/go-llama/;
    proxy_set_header   Host $host;
    proxy_set_header   X-Real-IP $remote_addr;
    proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_http_version 1.1;
    proxy_set_header   Upgrade $http_upgrade;
    proxy_set_header   Connection "upgrade";
}
```
- Place this inside your `server { ... }` block.
- Adjust `proxy_pass` if Go-LLama runs on a different port or host.
- For HTTPS, also set up SSL/TLS as usual.

---

## Documentation

- [Setup Guide](SETUP.md)
- [Manual Testing](MANUAL_TESTING.md)
- [Security Hardening](HARDENING.md)
- [API Spec](openapi/openapi.yaml)
- [Project State & Progress](PROJECT_STATE.md)
- [Code Review Tracker](CODE_REVIEW_TRACKER.md)

---

## Configuration

- See [`config.sample.json`](config.sample.json) for a template.
- **Do not commit `config.json` with secrets!**  
  Use `.gitignore` (already included).
- For Docker Compose, use service names (`postgres`, `redis`) as hosts in `config.json`.

---

## Troubleshooting

- **Frontend not loading?**  
  Make sure `frontend/` is included in your Docker image (see Dockerfile).
- **Database/Redis errors?**  
  Use `host=postgres`, `addr=redis:6379` in your config when running via Docker Compose.
- **Check logs:**  
  ```sh
  docker compose logs go-llama-backend
  ```

---

## License

MIT License (see [LICENSE](LICENSE))

---

## Credits

- **Created by:** [TheFozid](https://github.com/TheFozid)
- **Copilot & Documentation:** [GitHub Copilot](https://github.com/features/copilot), @copilot

---

## Contributing

PRs, issues, and suggestions welcome!  
Open an issue or discussion for questions, bugs, or feature requests.

---

## Screenshot

![Go-Llama Logo](static/Go-Llama-logo.png)

---

> Deploy, chat, and power up your local LLMs with Go-LLama!
