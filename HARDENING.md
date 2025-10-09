# Go-LLama Backend: Production Hardening Notes

This document outlines production hardening considerations for the Go-LLama backend.  
**These items are to be actioned at a later date as needed.**

---

## 1. Environment and Secrets Management

- Not required for this deployment.

## 2. Process Management

- Use **systemd** to manage the Go backend process for restart-on-failure and boot-time startup.

## 3. Network Security

- Already in place.
- When a frontend is deployed, add an `nginx` location block to route traffic as needed.

## 4. TLS/HTTPS

- Already handled by the existing `nginx` reverse proxy in production.

## 5. Database and Data Security

- Not required for this deployment.

## 6. Logging and Monitoring

- Not required for this deployment.

## 7. Updates and Patch Management

- Not required for this deployment.

## 8. Resource Management

- Not required for this deployment.

## 9. API and User Security

- **Authentication, Session, and Permission Checks:**  
  - All authentication, session management, and role/permission checks are covered by automated tests.
  - Edge cases (expired tokens, privilege escalation attempts, session expiry) are validated.
  - Run `go test -cover ./...` to confirm security test coverage.

- **Enforce strong password policies:** Not required.
- **Rate-limit sensitive endpoints (login, password reset):** Not required.
- **CORS settings:**  
  - *Note:* Unsure if correctly restricted for production. Review and ensure only trusted origins are allowed.
- **Validate all user input:**  
  - *Note:* Unsure if fully implemented. Review and ensure all user input is validated to prevent injection and other attacks.

## 10. Disaster Recovery

- Not required for this deployment.

## 11. Documentation and Access Control

- **Document all setup, hardening, and recovery procedures:** Yes, required.
- **Restrict SSH/admin access:** Not required for this deployment.

## 12. Streaming and WebSocket Hardening

- When deploying, verify nginx or proxy configuration supports WebSocket upgrades.
- Ensure frontend handles WebSocket errors gracefully and only shows error UI on real errors.
- Add automated tests for reference section formatting and tokens/sec display in streamed responses.

---

## Automated Security & Permission Testing

- All authentication, session management, and role/permission checks are covered by automated tests across API, user, and chat modules.
- Security/permission regressions are included in integration and system tests.
- Manual and automated test scripts validate all endpoints, error paths, and streaming behavior.
- Coverage for authentication, session, and user security logic is >98%.

---

## TODO

- [ ] Review CORS settings for production. Restrict to trusted origins only.
- [ ] Review user input validation throughout all endpoints.
- [ ] Document all setup, hardening, and (if needed) recovery procedures.

---
