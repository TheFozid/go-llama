# Go-LLama Code Review Tracker

> Use this document to track review progress, add notes, and ensure correct interactions between files.  
> Mark each file as `[x] Reviewed` when complete. Add comments, issues, or integration notes as needed.

---

## **Backend/API Core**

- [x] `cmd/server/main.go`
    - Entrypoint. Handles server startup.
    - Notes: Loads config, initializes DB/Redis, sets up API router, starts server. Error handling is clean. Integration with config, DB, Redis, and API router confirmed. No graceful shutdown (can be added later).
- [x] `internal/api/handlers.go`
    - Main API routing.
    - Notes: Defines `/health` and `/config` endpoints. Only exposes safe config fields. Handlers are idiomatic and clean.
- [x] `internal/api/router.go`
    - Route definitions, middleware.
    - Notes: Loads HTML templates, serves static assets, routes for `/`, `/login`, `/setup`, `/favicon.ico` with user existence guard. API routes grouped under subpath for health, config, setup, auth, user management, LLMs, chat, streaming, web search, online users, chat edit/delete. Uses `auth.AuthMiddleware(cfg, rdb, isAdmin)` for protected endpoints. All handlers referenced must exist and be implemented. Static assets and HTML templates paths are relative; ensure deployment matches expected structure.
- [x] `internal/api/chat_handlers.go`
    - Chat API endpoints.
    - Notes: Handlers for model listing, chat creation, chat listing, chat editing/deletion, get chat/messages, send message (LLM/web search integration). All DB interactions scoped to authenticated user. LLM API call is abstracted and testable. Error handling is robust. Handlers match those registered in router.go. Depends on `chat.Chat` and `chat.Message` models, and `db.DB`.
- [x] `internal/api/ws_chat_handler.go`
    - WebSocket chat streaming.
    - Notes: WebSocket endpoint for live LLM streaming. JWT auth from header or query. Handles chat/model/user validation, streaming tokens, graceful stop, saves bot reply with stats. Uses Markdown formatting and SearxNG for optional web search. Robust error handling and performance stats returned to client.
- [x] `internal/api/user_handlers.go`
    - User management endpoints.
    - Notes: Handlers for login (JWT + Redis session), logout, get user profile, online user count. All DB operations use authenticated user context. Good error handling. Integration with `auth`, `user`, and Redis session logic.
- [x] `internal/auth/jwt.go`
    - JWT logic.
    - Notes: Implements claims, token generation, parsing. Used by login, middleware, WebSocket auth. Secure, clean, robust.
- [x] `internal/auth/middleware.go`
    - Auth middleware.
    - Notes: Checks JWT, validates session in Redis, refreshes expiry, adds user info to context, enforces admin if required. Consistent errors and integration with router.
- [x] `internal/db/db.go`
    - DB connection, migration.
    - Notes: Connects to PostgreSQL, auto-migrates user, chat, message models. Sets global DB instance. Used by all handlers. Logs success/failure.
- [x] `internal/redis/client.go`
    - Redis connection.
    - Notes: Simple Redis client factory. Reads config, returns ready-to-use client. Used for session, online user tracking, etc.
- [x] `internal/config/config.go`
    - App config loader.
    - Notes: Loads and parses config.json. Singleton pattern. Minimal validation (JWT secret required). Used everywhere for dependency setup.

---

## **Frontend**

- [x] `frontend/index.html`
    - Main chat UI.
    - Notes: Bootstrap-based chat UI with model selection, online users, chat history, prompt box, user management modal, and modals for model selection. Uses JS for chat logic, Markdown, and API calls. Integrates with subpath.
- [x] `frontend/login.html`
    - Login page.
    - Notes: Clean login form, logo, error display. Uses subpath for assets and JS. Integrates with backend login endpoint.
- [x] `frontend/setup.html`
    - Admin setup page.
    - Notes: Initial admin creation page. Simple form for username and password. Uses subpath for assets and JS.
- [x] `frontend/css/main.css`
    - Main stylesheet.
    - Notes: Responsive, mobile-optimized chat UI. All major layout and interaction elements styled. Animations and accessibility included.
- [x] `frontend/js/main.js`
    - Main JS logic.
    - Notes: Handles all page logic: login/setup flows, JWT/session, chat streaming, markdown, UI events, user management, API calls. Matches backend API.

---

## **Config & Environment**

- [x] `config.json`
    - App configuration.
    - Notes: All settings present and match backend config loader. Internal service IPs; credentials must be changed in prod. JWT secret required.
- [x] `docker-compose.yml`
    - Service orchestrator.
    - Notes: Sets up postgres and redis with persistent volumes. Default credentials; must be changed in prod. Exposes ports for local dev.

---

## **Docs & OpenAPI**

- [x] `MANUAL_TESTING.md`
    - Manual test steps.
    - Notes: Comprehensive, matches API and frontend. Covers all flows, troubleshooting, and recent bug fixes.
- [x] `SETUP.md`
    - Setup guide.
    - Notes: Local and remote setup steps, config, deployment, nginx proxy tips. Matches project requirements.
- [x] `HARDENING.md`
    - Security checklist.
    - Notes: Covers all major security areas, permission tests, and TODOs for CORS/input validation. Automated test coverage noted.
- [x] `openapi/openapi.yaml`
    - API spec.
    - Notes: Full OpenAPI 3 spec. All major endpoints and schemas. JWT bearer security, detailed docs, matches backend.
- [x] `PROJECT_STATE.md`
    - Project anchor.
    - Notes: **Needs update to reflect completed code review and tracker.**

---

## **Optional / Branding / Tests**

- [x] `static/Go-Llama-logo.png`
    - Branding.
    - Notes:
- [x] `static/favicon.ico`
    - Favicon.
    - Notes:
- [ ] All `*_test.go` files
    - Coverage.
    - Notes:

---

## **General Review Notes**

- All core files, docs, config, and environment have been reviewed.
- API contracts, frontend/backend integration, and hardening steps are well documented.
- Manual and automated test guides are up to date.
- Recommend: Update `PROJECT_STATE.md` to reflect code review completion and tracker status.
- Track any further bugs, patches, or refactors in this file and/or future issues.

---

## **Legend**

- `[x]` = Reviewed
- `[ ]` = Pending

---

> Update this file as review progresses. Add notes for each file and integration point.
