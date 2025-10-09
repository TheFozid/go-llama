# Go-LLama Manual Testing Guide

_Anchored to PROJECT_STATE.md and current codebase/infra as of 2025-10-06._

## Automated Testing & Coverage

All backend and API logic is covered by unit/integration tests.  
To run all tests and check coverage:

```bash
go test -cover ./...
```

---

## 1. Start/Reset All Services

### **(A) Stop & Remove Existing Docker Data**
```sh
docker compose down -v
```

### **(B) Start PostgreSQL and Redis**
```sh
docker compose up -d
```

### **(C) Start the Backend Server**
```sh
go run ./cmd/server/main.go
```
Or, build and run:
```sh
go build -o go-llama-backend ./cmd/server
./go-llama-backend
```
The server will listen on `0.0.0.0:8070` and serve under `/go-llama`.

---

## 2. Initial User Setup (Admin)

```sh
curl -X POST http://localhost:8070/go-llama/setup \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "adminpass"}'
```

---

## 3. Login & Get JWT

```sh
curl -X POST http://localhost:8070/go-llama/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "adminpass"}'
```

_Response contains:_
```json
{
  "token": "YOUR_JWT_HERE",
  "user": { ... }
}
```
```sh
export JWT=PUT_YOUR_TOKEN_HERE
```

---

## 4. Chat CRUD & Streaming

### **Create a Chat**
```sh
curl -X POST http://localhost:8070/go-llama/chats \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT" \
  -d '{"title": "Test Chat"}'
```
_Note returned `"id"` as CHATID._

### **Send Message (No Web Search)**
```sh
curl -X POST http://localhost:8070/go-llama/chats/CHATID/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT" \
  -d '{"content": "What is Go?"}'
```

### **Send Message (With Web Search)**
```sh
curl -X POST http://localhost:8070/go-llama/chats/CHATID/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT" \
  -d '{"content": "What is Go?", "web_search": true}'
```

### **Rename Chat**
```sh
curl -X PUT http://localhost:8070/go-llama/chats/CHATID \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT" \
  -d '{"title": "Renamed Chat"}'
```

### **Delete Chat**
```sh
curl -X DELETE http://localhost:8070/go-llama/chats/CHATID \
  -H "Authorization: Bearer $JWT"
```

---

## 5. Test `/search` Endpoint

```sh
curl -X POST http://localhost:8070/go-llama/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT" \
  -d '{"prompt": "Explain Golang"}'
```

---

## 6. WebSocket Streaming (`/ws/chat`)

```sh
npm install -g wscat
wscat -c "ws://localhost:8070/go-llama/ws/chat?token=$JWT"
```
Send:
```json
{"chatId": CHATID, "prompt": "Stream something about concurrency in Go"}
```
You should see streamed JSON tokens.

---

## 7. Online User Count

Open multiple browsers/incognito sessions with different users.  
Poll with:
```sh
curl -X GET http://localhost:8070/go-llama/users/online
```
_Response:_
```json
{ "online": N }
```

---

## 8. Frontend Manual Testing Scenarios

- **Login flow:** Setup, login, session expiry after inactivity.
- **Chat UI:** Prompt stays visible while streaming; LLM response streams below.
- **Chat history:** Rename and delete work; history updates live.
- **Web search:** Toggle, sources display.
- **Error handling:** Test invalid JWT, network errors, backend failures.
- **Online users:** Real-time badge reflects active sessions.
- **User management:**
  - **Admin:** Can add/edit/delete all users except self (edit only).
  - **Normal user:** Can view/edit/delete own account (password change, account removal), cannot add users.
  - **Test:** Admin adds user, normal user logs in, edits/deletes own account, attempts to access other users (should be denied).
- **Streaming/Stop Button:**  
  - While streaming, click "Stop". Confirm UI stops updating and backend stops generating. Bubble should not show "Error streaming response" unless backend sends an explicit error.
- **References Handling:**  
  - Test streamed responses with references. Confirm references only appear at the end, are not empty, and are formatted correctly. No empty bullets or missing references.
- **Tokens/sec:**  
  - Confirm that tokens/sec is shown at the end of each streamed response and is always visible in chat history after reload.
- **Error Handling:**  
  - Induce backend error or forcibly disconnect the WebSocket. Confirm "Error streaming response" only appears for actual error, not on normal stop.
- **Recent Bugs & Fixes:**  
  - Streaming patch: tokens/sec now always saved and shown.
  - References section now only shown with valid `[n]: ...` markdown.
  - Stop button works robustly, backend and frontend both halt streaming.
  - No UI flicker, lost tokens/sec, or empty references after history reload.

---

## 9. Troubleshooting

- **401 Unauthorized?**  
  Check JWT, re-login if expired.
- **DB/Redis errors?**  
  Ensure containers are running.
- **LLM/SearxNG errors?**  
  Check config endpoints.
- **Streaming fails?**  
  Check WS URL and headers.
- **Rename/delete 404?**  
  Confirm backend router has `PUT`/`DELETE` routes for `/chats/:id`.
- **User management 403?**
  - Admin endpoints require admin JWT.
  - Normal users use `/users/me` for self-service.

---

## Notes

- All endpoints under `/go-llama` (set in `config.json`).
- Use `export JWT=...` for curl convenience.
- See `openapi/openapi.yaml` for full API schema.

_Keep this doc up to date with API/UI changes._
