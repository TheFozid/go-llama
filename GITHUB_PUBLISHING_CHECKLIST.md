# Go-LLama GitHub Publishing & Public Deployment Checklist

_Use this document to track the progress of putting Go-LLama on GitHub for public deployment.  
Update with notes, links, and status as you go._

---

## **1. Local Project Preparation**

- [ ] All source code present (`cmd/`, `internal/`, `frontend/`, etc.)
- [ ] Config files (`config.json`, `config.sample.json`, `docker-compose.yml`)
- [ ] Docs (`README.md`, `SETUP.md`, `MANUAL_TESTING.md`, `HARDENING.md`, `PROJECT_STATE.md`, `openapi/openapi.yaml`)
- [ ] Static assets (`static/Go-Llama-logo.png`, `static/favicon.ico`)
- [ ] `.gitignore` created (protect secrets, binaries, logs)
- [ ] Remove/clean any secrets before publishing

**Notes:**  
- Use `config.sample.json` with dummy values; real secrets/keys should never be committed.

---

## **2. Create New GitHub Repository**

- [ ] Go to https://github.com and log in
- [ ] Click "+" → "New repository"
- [ ] Name: `go-llama` (or your choice)
- [ ] Set **Public** for open sharing
- [ ] Add project description
- [ ] (Optional) Initialize with README

**Notes:**  
- If you have an existing local folder, clone the empty repo first and copy files in.

---

## **3. Add Local Files to Repo**

- [ ] Initialize git: `git init` (if not done)
- [ ] Set remote: `git remote add origin https://github.com/YOUR_USERNAME/go-llama.git`
- [ ] Add all files: `git add .`
- [ ] Commit: `git commit -m "Initial commit: Go-LLama source code, config, docs, assets"`
- [ ] Push: `git push -u origin main`

**Notes:**  
- Double-check `.gitignore` before pushing!  
- Do not push secrets, compiled binaries, or personal info.

---

## **4. Write/Update README.md**

- [ ] Project description
- [ ] Features
- [ ] Quickstart/Setup instructions
- [ ] Docs/config links
- [ ] License info (MIT recommended)
- [ ] (Optional) Screenshots, badges

**Notes:**  
- Ask Copilot for a starter `README.md` if needed.

---

## **5. Organize Docs and Configs**

- [ ] Place docs in root or `/docs`
- [ ] Add `config.sample.json`
- [ ] Ensure `config.json` is in `.gitignore`
- [ ] All docs up to date with latest code

---

## **6. Add LICENSE**

- [ ] Add MIT or preferred license (`LICENSE` file)
- [ ] Verify GitHub recognizes license

---

## **7. (Optional) Setup GitHub Pages**

- [ ] Enable GitHub Pages in repo settings
- [ ] Set source to `/docs` or `/` branch
- [ ] Add `index.md` in `/docs` for homepage
- [ ] Link docs from README

---

## **8. Verify Repo & Share**

- [ ] All files visible online
- [ ] README and docs easy to find
- [ ] Static assets present
- [ ] Share repo URL for deployment

---

## **9. Deployment (From GitHub)**

- [ ] Clone repo on target machine
- [ ] Follow `SETUP.md`/`MANUAL_TESTING.md`
- [ ] Confirm services run and app works

---

## **10. (Optional) Open Issues/Discussions**

- [ ] Create GitHub Issues for bugs, feature requests, questions
- [ ] Enable Discussions for community support

---

## **Progress Notes**

_Add any blockers, completed steps, or changes here as you work through the checklist:_

- 2025-10-09: Checklist started. Local files ready. Need to draft README and .gitignore.
- ...

---

## **Links**

- [Go-LLama GitHub Repo](https://github.com/YOUR_USERNAME/go-llama) (update after publishing)
- [Docs folder](https://github.com/YOUR_USERNAME/go-llama/tree/main/docs) (if used)
- [GitHub Pages](https://YOUR_USERNAME.github.io/go-llama) (if enabled)

---

> Update this checklist as you go.  
> Track each step, add notes, and ensure a smooth publishing and deployment process.
