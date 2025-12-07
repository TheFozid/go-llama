# Go-LLama Web App Development: Project Anchor

---

## Master Project Prompt (2025-10-09)

### **Overview**
Go-LLama is a modern chat UI for local LLMs (llama.cpp/llamafile), with user-authenticated history, live streaming, and optional web search.

**Stack:**  
- **Dev:** Arch Linux desktop (Go)  
- **Deploy:** Debian 12 server  
- **Frontend:** HTML, JS, CSS (Bootstrap)  
- **Backend:** Go (Gin)  
- **Auth:** JWT  
- **Database:** PostgreSQL  
- **Cache:** Redis  
- **Web Search:** SearxNG instance  
- **LLM API:** llama.cpp/llamafile (OpenAI-compatible, streaming via WebSocket)

---

### **Priorities**
- Low latency (async, goroutines, caching)
- Simplicity/MVP
- Basic scalability
- Fully documented, maintainable code

---

### **Copilot Role**
- Step-by-step coding guide: plans, code, explanations, docs, troubleshooting.
- Feedback loop before each phase.

---

## **Current Architecture**
- **docker-compose.yml:** PostgreSQL, Redis
- **config.json:** Endpoints, secrets, subpath config (default `/go-llama`)
- **LLM & SearxNG endpoints:** Hosted on remote server

---

## **App Workflow**
- First start: DB migration, `/setup` for admin, then `/login`.
- After login: Chat UI (`/`), top bar, chat area, history, settings, online counter.
- Prompt submission: response streamed via WebSocket, history updates.
- Web Search toggle: SearxNG results fed to LLM.
- Routing: `/login`, `/setup`, `/` based on user/session state.
- **User management:** Admins manage all users except themselves; users manage their own account.

---

## **Extra Requirements**
- Favicon at `/static/favicon.ico`
- Responsive UI: tooltips, icons, transitions.
- DB: Automated migrations.
- JWT/session: 30min inactivity expiry.
- Subpath: config option.
- Troubleshooting: tips for each phase.

---

## **Phase Summary**

### **Phase 1: Backend API & Core Logic [COMPLETE]**
- Go/Gin backend, JWT, DB migration, Redis, SearxNG, LLM integration, streaming, docs/tests.
- Online user count (`/users/online`) via Redis.

### **Phase 2: Frontend MVP [COMPLETE]**
- `/frontend` directory, vanilla JS, Bootstrap CSS.
- Auth/routing, main chat UI, API integration, UI/UX polish, user management, deployment.

### **Phase 3: Integration & Workflow [COMPLETE]**
- Frontend-backend integration, session persistence, chat history/model switching, error handling, user management workflows.

### **Phase 4: Advanced Features & Optimisation [COMPLETE]**
- **Code review and optimisation completed.**
- All code files, configuration, documentation, and environment reviewed for style, maintainability, integration, and brief compliance.
- Latency optimisation, scaling strategies, further UI polish, accessibility, and security hardening documented.
- Manual and automated test guides verified.
- Optional: Mobile polish, advanced admin settings.

### **Phase 5: Deployment & Production [NEXT]**
- Deploy to Debian 12 server, Docker Compose, nginx/TLS/subpath, setup/migration/recovery docs, monitoring/logging, backup/restore.

### **Phase 6: Android App Development [PLANNED]**
- Develop Android app to connect directly to the existing backend API.
- Mobile-first UI/UX, authentication, chat streaming, push notifications (optional).
- Use OpenAPI spec for endpoint integration.
- Backend API is stable; no major web app changes needed for Android.
- Plan for shared features (PWA/offline) if required.

---

## **Mobile Scaling Note (2025-10-09)**

- Responsive UI scaling now uses a combination of:
  - `clamp()` for desktop fluid font-size.
  - Media queries for common breakpoints.
  - `@media (pointer: coarse) and (min-resolution: 2dppx)` to boost font size for high-DPI mobile browsers, ensuring clean UI on mobile portrait even for high-res devices.
- Scaling solution is robust for desktop and mobile, and can be tuned further for future devices.

---

## **Recent Fixes (2025-10-06 to 2025-10-08)**

| Fix/Change                        | Date         | Impact/Result                                            |
|------------------------------------|--------------|----------------------------------------------------------|
| Chat Saved Notification Removed    | 2025-10-06   | No notification, instant history update, no UI flicker   |
| Reference Section Logic Updated    | 2025-10-06   | Only model output rendered, no frontend patching         |
| Streaming Patch                    | 2025-10-06   | tokens/sec always displayed, stop button reliable        |
| Prompt Box/Buttons Shadow/Spacing  | 2025-10-06   | Heavy shadow, unified controls, websearch toggle clear   |
| Chat Window Margin Increased       | 2025-10-06   | More space below chat bubbles for visibility             |
| Thinking Bubble Logic Finalized    | 2025-10-08   | Robust markdown/thinking bubble rendering, scrollable    |
| Manual Test Pass                   | 2025-10-08   | All major flows validated, UI consistent, history robust |

---

## **Status**

- **Backend:** Complete, tested, backed up.
- **Frontend:** MVP and polish complete, mobile layout working, robust streaming/history.
- **Deployment:** Ready to deploy; static build and documentation prepared.
- **Android:** Planned after web deployment.
- **Code Review:** **COMPLETE**  
  All code, config, environment, and documentation reviewed.  
  See `CODE_REVIEW_TRACKER.md` for detailed tracker.  
  All brief compliance items checked and validated.

---

## **Next Steps**

1. **Web App Deployment:**  
   - Deploy to Debian 12 server.
   - Configure Docker Compose/nginx, TLS, subpath set up.
   - Validate production, backup/restore flows.

2. **Android App Development:**  
   - Start mobile app project after web deployment.
   - Use same backend API; no changes required to web app for Android.
   - Follow OpenAPI spec for integration.
   - Design mobile-friendly UI; consider PWA/shared features if needed.

---

## **Brief Compliance Checklist**

- [x] MVP and core flows implemented and tested
- [x] Deployment, backup, recovery documented and ready
- [x] Latency and scalability strategies started
- [x] User management, security, session handling complete
- [x] UI/UX polished, mobile responsive
- [x] Favicon, icons, transitions, Markdown/thinking bubbles robust
- [x] Documentation up to date
- [x] **Code review and tracker complete** ✅

---

## **Workflow Policy**

- For each phase:
  - Copilot outlines steps, provides code/instructions, asks for feedback.
  - All code supports Dockerized DB/Redis.
  - Troubleshooting tips for common issues.
  - Wait for user confirmation before next phase.

---

## **Feedback Loop**

- **Code review and optimisation phase is now complete.**
- All files, documentation, and requirements have been reviewed.
- Please confirm this summary and proceed to deployment or next development phase.
