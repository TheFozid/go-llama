// Go-LLama Frontend Core Logic

console.log("main.js loaded!");

const SUBPATH = window.SUBPATH || "";
const API_BASE = SUBPATH;

console.log("SUBPATH:", SUBPATH);

document.addEventListener("DOMContentLoaded", function () {
    // --- LOGIN PAGE ---
    if (document.getElementById("loginForm")) {
        redirectIfLoggedIn();
        document.getElementById("loginForm").addEventListener("submit", async function (e) {
            e.preventDefault();
            const username = document.getElementById("username").value.trim();
            const password = document.getElementById("password").value;
            const errBox = document.getElementById("loginError");
            errBox.classList.add("d-none");
            try {
                const res = await fetch(API_BASE + "/auth/login", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ username, password })
                });
                const data = await res.json();
                if (res.ok && data.token) {
                    setJWT(data.token);
                    window.location.href = SUBPATH;
                } else if (data.error?.need_setup) {
                    window.location.href = SUBPATH + "/setup";
                } else {
                    errBox.textContent = data.error?.message || "Login failed";
                    errBox.classList.remove("d-none");
                }
            } catch (e) {
                errBox.textContent = "Network error";
                errBox.classList.remove("d-none");
            }
        });
        return;
    }

    // --- SETUP PAGE ---
    if (document.getElementById("setupForm")) {
        redirectIfLoggedIn();
        document.getElementById("setupForm").addEventListener("submit", async function (e) {
            e.preventDefault();
            const username = document.getElementById("setupUsername").value.trim();
            const password = document.getElementById("setupPassword").value;
            const errBox = document.getElementById("setupError");
            errBox.classList.add("d-none");
            try {
                const res = await fetch(API_BASE + "/setup", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ username, password })
                });
                const data = await res.json();
                if (res.ok && data.setup_complete) {
                    window.location.href = SUBPATH + "/login";
                } else {
                    errBox.textContent = data.error?.message || "Setup failed";
                    errBox.classList.remove("d-none");
                }
            } catch (e) {
                errBox.textContent = "Network error";
                errBox.classList.remove("d-none");
            }
        });
        return;
    }

    // --- MAIN CHAT UI PAGE ---
    if (document.getElementById("mainContent")) {
        redirectIfNotLoggedIn();

        async function updateOnlineUsers() {
            const n = await getOnlineUsers();
            const badge = document.getElementById("onlineUsers");
            badge.textContent = "Online: " + n;
            badge.className = "badge rounded-pill bg-" + (n > 0 ? "success" : "secondary");
        }
        updateOnlineUsers();
        setInterval(updateOnlineUsers, 10000);

        getModels().then(models => {
            modelsCache = models;
            if (models.length > 0) {
                activeModel = models[0].name;
                document.getElementById("currentModel").textContent = activeModel;
            }
        });

        document.getElementById("historySidebar").classList.add("d-none");
        ensureInitialChat().then(loadChatHistory);

document.getElementById("newChatBtn").onclick = function () {
    const modal = new bootstrap.Modal(document.getElementById("modelModal"));
    const systemChoices = document.getElementById("systemChoices");
    
// Clear previous selections
systemChoices.innerHTML = "";

// Add standard model options FIRST
modelsCache.forEach(m => {
    const modelOption = document.createElement("button");
    modelOption.type = "button";
    modelOption.className = "list-group-item list-group-item-action";
    modelOption.innerHTML = `<h6 class="mb-1">${m.name}</h6>`;
    modelOption.dataset.system = "standard";
    modelOption.dataset.model = m.name;
    systemChoices.appendChild(modelOption);
});

// Add separator
const separator = document.createElement("div");
separator.className = "list-group-item disabled";
separator.innerHTML = "<small class='text-muted'>Advanced</small>";
systemChoices.appendChild(separator);

// Add GrowerAI option LAST
const growerAIOption = document.createElement("button");
growerAIOption.type = "button";
growerAIOption.className = "list-group-item list-group-item-action";
growerAIOption.innerHTML = `
    <div class="d-flex w-100 justify-content-between">
        <h6 class="mb-1">GrowerAI</h6>
    </div>
`;
growerAIOption.dataset.system = "growerai";
growerAIOption.dataset.model = "";
systemChoices.appendChild(growerAIOption);
    
    // Handle selection
    let selectedSystem = null;
    let selectedModel = null;
    
    systemChoices.querySelectorAll('button[data-system]').forEach(btn => {
        btn.onclick = function() {
            // Remove active from all
            systemChoices.querySelectorAll('button').forEach(b => b.classList.remove('active'));
            // Add active to clicked
            this.classList.add('active');
            selectedSystem = this.dataset.system;
            selectedModel = this.dataset.model;
        };
    });
    
    modal.show();

    document.getElementById("modelForm").onsubmit = async function (e) {
        e.preventDefault();
        
        if (!selectedSystem) {
            alert("Please select a system");
            return;
        }
        
        const useGrowerAI = selectedSystem === "growerai";
        const modelName = useGrowerAI ? "" : selectedModel;
        
        modal.hide();
        
        // Clear GrowerAI message memory when creating new GrowerAI chat
        if (useGrowerAI) {
            window.growerAIMessages = [];
        }
        
        const chat = await createChat(modelName, useGrowerAI);
        if (chat.id) {
            loadChatHistory();
            switchChat(chat.id, chat.model_name || modelName, chat.use_grower_ai || useGrowerAI);
        }
    };
};

document.getElementById("toggleHistoryBtn").onclick = function () {
    const sidebar = document.getElementById("historySidebar");
    const overlay = document.getElementById("sidebarOverlay");

    if (sidebar.classList.contains("d-none")) {
        // Currently hidden → show with animation
        sidebar.classList.remove("d-none");
        overlay.classList.remove("d-none");

        // trigger reflow so CSS transitions apply
        void sidebar.offsetWidth;
        void overlay.offsetWidth;

        sidebar.classList.add("showing");
        overlay.classList.add("showing");
    } else {
        // Currently visible → animate out before hiding
        sidebar.classList.remove("showing");
        overlay.classList.remove("showing");

        const hideAfter = () => {
            sidebar.classList.add("d-none");
            overlay.classList.add("d-none");
        };

        sidebar.addEventListener("transitionend", hideAfter, { once: true });
    }
};

// === NEW: Clicking the overlay closes sidebar ===
document.getElementById("sidebarOverlay").onclick = function () {
    const sidebar = document.getElementById("historySidebar");
    const overlay = document.getElementById("sidebarOverlay");

    // If already hidden, do nothing
    if (sidebar.classList.contains("d-none")) return;

    sidebar.classList.remove("showing");
    overlay.classList.remove("showing");

    sidebar.addEventListener("transitionend", () => {
        sidebar.classList.add("d-none");
        overlay.classList.add("d-none");
    }, { once: true });
};

// === NEW: ESC key closes sidebar ===
document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;

    const sidebar = document.getElementById("historySidebar");
    const overlay = document.getElementById("sidebarOverlay");

    if (!sidebar.classList.contains("d-none")) {
        overlay.click(); // reuse closing logic
    }
});



        document.getElementById("signOutBtn").onclick = function () {
            clearJWT();
            window.location.href = SUBPATH + "/login";
        };

        document.getElementById("settingsBtn").onclick = function () {
            const modal = new bootstrap.Modal(document.getElementById("settingsModal"));
            modal.show();
            loadUsersTable();
        };
        document.getElementById("addUserBtn").onclick = addUserFromModal;

        // --- Prompt textarea: Shift+Enter for new line, auto-resize up to 5 lines ---
        const textarea = document.getElementById("promptInput");
        if (textarea) {
            textarea.addEventListener("keydown", function(e) {
                if (e.key === "Enter") {
                    if (e.shiftKey) {
                        // Insert newline at cursor
                        const {selectionStart, selectionEnd, value} = textarea;
                        textarea.value = value.slice(0, selectionStart) + "\n" + value.slice(selectionEnd);
                        textarea.selectionStart = textarea.selectionEnd = selectionStart + 1;
                        e.preventDefault();
                        setTimeout(() => autoResizePrompt(), 0);
                    } else {
                        // Submit form on Enter without Shift
                        document.getElementById("sendBtn").click();
                        e.preventDefault();
                    }
                }
            });

            textarea.addEventListener("input", autoResizePrompt);

function autoResizePrompt() {
    // Always reset rows and height before measuring
    textarea.rows = 1;
    textarea.style.height = "auto";

    // Get computed line height
    const style = window.getComputedStyle(textarea);
    const lineHeight = parseFloat(style.lineHeight);
    const padding = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
    const border = parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);

    // Calculate visible rows from scrollHeight
    const contentHeight = textarea.scrollHeight - padding - border;
    let rows = Math.round(contentHeight / lineHeight);

    if (isNaN(rows) || rows < 1) rows = 1;
    if (rows > 8) rows = 8;

    textarea.rows = rows;

    if (rows < 8) {
        textarea.style.overflowY = "hidden";
        textarea.style.height = (lineHeight * rows + padding + border) + "px";
    } else {
        textarea.style.overflowY = "auto";
        textarea.style.height = (lineHeight * 8 + padding + border) + "px";
    }
}

            // Initial resize on page load
            autoResizePrompt();
        }


document.getElementById("promptForm").onsubmit = function (e) {
    e.preventDefault();
    const input = document.getElementById("promptInput");
    const prompt = input.value.trim();
    if (!prompt || !activeChatId) return;

    // Handle GrowerAI chats (skip model validation, store in memory)
    if (window.isGrowerAIChat) {
        input.value = "";
        autoResizePrompt();
        
        // Store user message in memory FIRST
        window.growerAIMessages.push({
            sender: "user",
            content: prompt,
            created_at: new Date().toISOString()
        });
        
        // Re-render all messages from memory (including new user message)
        renderMessages(window.growerAIMessages);
        
        // Start streaming (this will add the thinking placeholder AFTER the messages)
        startStreamingResponse(prompt);
        return;
    }

    // Check if the model is still available (for standard LLM chats)
    const isModelAvailable = modelsCache.some(m => m.name === activeModel);

    if (!isModelAvailable) {
        // Show model selection modal and force user to pick a new model
        const modal = new bootstrap.Modal(document.getElementById("modelModal"));
        const systemChoices = document.getElementById("systemChoices");
        
        // Clear previous selections
        systemChoices.innerHTML = "";
        
        // Add GrowerAI option
        const growerAIOption = document.createElement("button");
        growerAIOption.type = "button";
        growerAIOption.className = "list-group-item list-group-item-action";
        growerAIOption.innerHTML = `
            <div class="d-flex w-100 justify-content-between">
                <h6 class="mb-1">GrowerAI</h6>
                <small class="text-muted">Perpetual Learning</small>
            </div>
        `;
        growerAIOption.dataset.system = "growerai";
        growerAIOption.dataset.model = "";
        systemChoices.appendChild(growerAIOption);
        
        // Add separator
        const separator = document.createElement("div");
        separator.className = "list-group-item disabled";
        separator.innerHTML = "<small class='text-muted'>Standard LLM Models</small>";
        systemChoices.appendChild(separator);
        
        // Add standard model options
        modelsCache.forEach(m => {
            const modelOption = document.createElement("button");
            modelOption.type = "button";
            modelOption.className = "list-group-item list-group-item-action";
            modelOption.innerHTML = `<h6 class="mb-1">${m.name}</h6>`;
            modelOption.dataset.system = "standard";
            modelOption.dataset.model = m.name;
            systemChoices.appendChild(modelOption);
        });
        
        // Handle selection
        let selectedSystem = null;
        let selectedModel = null;
        
        systemChoices.querySelectorAll('button[data-system]').forEach(btn => {
            btn.onclick = function() {
                systemChoices.querySelectorAll('button').forEach(b => b.classList.remove('active'));
                this.classList.add('active');
                selectedSystem = this.dataset.system;
                selectedModel = this.dataset.model;
            };
        });
        
        modal.show();

        document.getElementById("modelForm").onsubmit = async function (e2) {
            e2.preventDefault();
            
            if (!selectedSystem) {
                alert("Please select a system");
                return;
            }
            
            const newModel = selectedSystem === "growerai" ? "" : selectedModel;
            modal.hide();
            
            // Update the chat's model on the backend
            await apiFetch(`/chats/${activeChatId}`, {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ model_name: newModel || modelsCache[0].name })
            });
            activeModel = newModel || modelsCache[0].name;
            document.getElementById("currentModel").textContent = activeModel;
            
            // Now send the prompt
            input.value = "";
            autoResizePrompt();
            const chatMessagesDiv = document.getElementById("chatMessages");
            const userDiv = document.createElement("div");
            userDiv.className = "user";
            const userBubble = document.createElement("div");
            userBubble.className = "message";
            userBubble.textContent = prompt;
            userDiv.appendChild(userBubble);
            chatMessagesDiv.appendChild(userDiv);
            chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
            startStreamingResponse(prompt);
        };
        // Don't send prompt yet, wait for model selection
        return;
    }

    input.value = "";
    autoResizePrompt();
    const chatMessagesDiv = document.getElementById("chatMessages");
    const userDiv = document.createElement("div");
    userDiv.className = "user";
    const userBubble = document.createElement("div");
    userBubble.className = "message";
    userBubble.textContent = prompt;
    userDiv.appendChild(userBubble);
    chatMessagesDiv.appendChild(userDiv);
    chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
    startStreamingResponse(prompt);
};

getChatHistory().then(chats => {
    if (Array.isArray(chats) && chats.length > 0) {
        activeChatId = chats[0].id;
        activeModel = chats[0].model_name;
        window.isGrowerAIChat = chats[0].use_grower_ai || false;
        document.getElementById("currentModel").textContent = window.isGrowerAIChat ? "🧠 GrowerAI" : activeModel;
        switchChat(activeChatId, activeModel, window.isGrowerAIChat);
    }
});

        if (!window.marked) {
            const script = document.createElement("script");
            script.src = "https://cdn.jsdelivr.net/npm/marked/marked.min.js";
            document.body.appendChild(script);
        }
    }
});

// --- Expose globals ---
window.activeChatId = null;
window.activeModel = null;
window.isGrowerAIChat = false;
window.growerAIMessages = []; // NEW: Store GrowerAI messages in frontend memory
let modelsCache = [];

// --- Helper to get WebSocket URL ---
function getWSUrl() {
    let wsProto = location.protocol === "https:" ? "wss:" : "ws:";
    let host = location.host;
    let path = API_BASE + "/ws/chat";
    const jwt = getJWT();
    return `${wsProto}//${host}${path}?token=${encodeURIComponent(jwt)}`;
}

// --- Session Helpers ---
function setJWT(token) {
    localStorage.setItem("jwt", token);
    localStorage.setItem("jwt_time", Date.now());
}

function parseJwt(token) {
    // JWT is in format header.payload.signature
    try {
        const payload = JSON.parse(atob(token.split('.')[1]));
        return payload;
    } catch (e) {
        return {};
    }
}

function getJWT() {
    const token = localStorage.getItem("jwt");
    if (!token) return null;
    const payload = parseJwt(token);
    const exp = payload.exp;
    if (!exp) {
        clearJWT();
        return null;
    }
    // exp is in seconds since epoch
    if (Date.now() / 1000 > exp) {
        clearJWT();
        return null;
    }
    return token;
}
function refreshJWTActivity() {
    if (localStorage.getItem("jwt")) localStorage.setItem("jwt_time", Date.now());
}
function clearJWT() {
    localStorage.removeItem("jwt");
    localStorage.removeItem("jwt_time");
}

function showGreetingIfEmpty() {
    getUserInfo().then(data => {
        let username = data.username || "there";
        const chatMessagesDiv = document.getElementById("chatMessages");
        if (chatMessagesDiv && chatMessagesDiv.children.length === 0) {
            const div = document.createElement("div");
            div.className = "llm";
            const bubble = document.createElement("div");
            bubble.className = "message fade-in";
            bubble.textContent = `Hi ${username}, how can I help?`;
            div.appendChild(bubble);
            chatMessagesDiv.appendChild(div);
        }
    });
}

// --- Routing Helpers ---
function redirectIfNotLoggedIn() {
    const path = window.location.pathname;
    if (
        !getJWT() &&
        !path.endsWith("/login") &&
        !path.endsWith("/setup")
    ) {
        window.location.href = SUBPATH + "/login";
    }
}
function redirectIfLoggedIn() {
    if (getJWT()) window.location.href = SUBPATH;
}

// --- Advanced Typewriter Animation Helper ---
function showTypewriterPlaceholder(word) {
    const chatMessagesDiv = document.getElementById("chatMessages");
    const div = document.createElement("div");
    div.className = "llm";
    const bubble = document.createElement("div");
    bubble.className = "message";
    bubble.id = "streamingBubble";

    let html = '<span class="typewriter-anim">';
    for (let i = 0; i < word.length; i++) {
        html += `<span class="letter" style="--delay:${i*0.06}s">${word[i]}</span>`;
    }
    html += `<span class="dots-anim"><span>.</span><span>.</span><span>.</span></span></span>`;

    bubble.innerHTML = html;
    div.appendChild(bubble);
    chatMessagesDiv.appendChild(div);
    chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
}

// --- API Helpers ---
async function apiFetch(path, opts = {}) {
    opts.headers = opts.headers || {};
    const jwt = getJWT();
    if (jwt) opts.headers["Authorization"] = `Bearer ${jwt}`;
    const res = await fetch(API_BASE + path, opts);
    if (res.status === 401) {
        clearJWT();
        window.location.href = SUBPATH + "/login";
        return {};
    }
    if (res.status === 204) return {};
    try {
        return await res.json();
    } catch (e) {
        return {};
    }
}
async function getOnlineUsers() {
    const res = await fetch(API_BASE + "/users/online");
    if (res.ok) {
        const data = await res.json();
        return data.online || 0;
    }
    return 0;
}
async function getUserInfo() {
    return await apiFetch("/auth/me", { method: "GET" });
}
async function getUsers() {
    return await apiFetch("/users", { method: "GET" });
}
async function getModels() {
    const res = await fetch(API_BASE + "/llms");
    return res.ok ? await res.json() : [];
}
async function getChatHistory() {
    return await apiFetch("/chats", { method: "GET" });
}
async function getChatMessages(chatId) {
    return await apiFetch(`/chats/${chatId}/messages`, { method: "GET" });
}
async function createChat(modelName, useGrowerAI = false) {
    return await apiFetch("/chats", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ 
            model_name: modelName,
            use_grower_ai: useGrowerAI 
        })
    });
}
async function sendMessage(chatId, content, webSearch) {
    return await apiFetch(`/chats/${chatId}/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ content, web_search: webSearch })
    });
}
async function editChatTitle(chatId, newTitle) {
    return await apiFetch(`/chats/${chatId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: newTitle })
    });
}
async function deleteChat(chatId) {
    try {
        const res = await apiFetch(`/chats/${chatId}`, {
            method: "DELETE"
        });
        return res;
    } catch (e) {
        return {};
    }
}
async function addUser(username, password) {
    return await apiFetch("/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password })
    });
}
async function editUserPassword(userId, password) {
    return await apiFetch(`/users/${userId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password })
    });
}
async function editOwnPassword(password) {
    return await apiFetch("/users/me", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password })
    });
}
async function deleteUser(userId) {
    return await apiFetch(`/users/${userId}`, {
        method: "DELETE"
    });
}
async function deleteOwnUser() {
    return await apiFetch("/users/me", {
        method: "DELETE"
    });
}

// --- Markdown Renderer (using marked.js CDN) ---
function renderMarkdown(md) {
    if (window.marked) {
        return marked.parse(md);
    }
    return md.replace(/\n/g, "<br>");
}



// --- Thinking Bubble Renderer ---
function renderWithThinkingBubbles(mdText) {
    // If no <think> tags, just render as markdown
    if (mdText.indexOf("<think>") === -1) {
        return renderMarkdown(mdText);
    }

    let output = "";
    let idx = 0;
    while (idx < mdText.length) {
        let thinkStart = mdText.indexOf("<think>", idx);
        let thinkEnd = mdText.indexOf("</think>", idx);

        // If no more <think> tags, render the rest as markdown and break
        if (thinkStart === -1) {
            output += renderMarkdown(mdText.slice(idx));
            break;
        }
        // Render text before <think> as markdown
        output += renderMarkdown(mdText.slice(idx, thinkStart));
        // Find matching </think> AFTER <think>
        if (thinkEnd === -1 || thinkEnd < thinkStart) {
            output += `<div class="thinking-bubble">${mdText.slice(thinkStart + 7).trim()}</div>`;
            break;
        }
        let thinkingText = mdText.slice(thinkStart + 7, thinkEnd).trim();
        output += `<div class="thinking-bubble">${thinkingText}</div>`;
        idx = thinkEnd + 8; // after </think>
    }
    return output;
}
// --- Chat Functions ---
async function ensureInitialChat() {
    const chats = await getChatHistory();
    if (!Array.isArray(chats) || chats.length === 0) {
        const models = await getModels();
        modelsCache = models;
        const chat = await createChat(models[0].name);
        if (chat.id) {
            activeChatId = chat.id;
            activeModel = chat.model || models[0].name;
            document.getElementById("currentModel").textContent = activeModel;
        }
    }
}
window.ensureInitialChat = ensureInitialChat;

async function loadChatHistory() {
    const sidebar = document.getElementById("historySidebar");
    const ul = document.getElementById("chatHistory");
    ul.innerHTML = "";
    const chats = await getChatHistory();
    if (!Array.isArray(chats)) return;
    chats.forEach(chat => {
        const li = document.createElement("li");
        li.className = "list-group-item d-flex justify-content-between align-items-center";
li.innerHTML = `
   <span class="history-title" contenteditable="false">${chat.title || `Chat ${chat.id}`}</span>
   <span class="chat-history-btns d-flex flex-column ms-2">
     <button class="btn chat-history-btn editChatBtn mb-1 edit-title-btn" title="Edit Title">
       <span class="material-symbols-outlined">edit_note</span>
     </button>
     <button class="btn chat-history-btn deleteChatBtn delete-chat-btn" title="Delete">
       <span class="material-symbols-outlined">delete</span>
     </button>
   </span>
`;
        li.onclick = (e) => {
            if (e.target.classList.contains("edit-title-btn") || e.target.classList.contains("delete-chat-btn")) return;
            switchChat(chat.id, chat.model_name, chat.use_grower_ai);
        };
        li.querySelector(".edit-title-btn").onclick = (e) => {
            e.stopPropagation();
            const span = li.querySelector(".history-title");
            span.contentEditable = true;
            span.focus();
            span.onblur = async () => {
                await editChatTitle(chat.id, span.textContent.trim());
                span.contentEditable = false;
                loadChatHistory();
            };
            span.onkeydown = async (ke) => {
                if (ke.key === "Enter") {
                    ke.preventDefault();
                    span.blur();
                }
            };
        };
        li.querySelector(".delete-chat-btn").onclick = async (e) => {
            e.stopPropagation();
            const ok = confirm("Delete this chat?");
            if (!ok) return;
            await deleteChat(chat.id);
            await loadChatHistory();
            const remaining = await getChatHistory();
if (chat.id === activeChatId) {
    if (remaining.length > 0) {
        switchChat(remaining[0].id, remaining[0].model_name, remaining[0].use_grower_ai);
    } else {
        // No remaining chats: clear chat window
        document.getElementById("chatMessages").innerHTML = "";
        // Optionally show greeting
        showGreetingIfEmpty();
        activeChatId = null;
        activeModel = null;
        window.isGrowerAIChat = false;
        window.growerAIMessages = [];
        document.getElementById("currentModel").textContent = "";
    }
}
        };
        ul.appendChild(li);
    });
}
window.loadChatHistory = loadChatHistory;

async function switchChat(chatId, modelName, useGrowerAI) {
    activeChatId = chatId;
    activeModel = modelName;
    window.isGrowerAIChat = useGrowerAI || false;
    
    document.getElementById("currentModel").textContent = useGrowerAI ? "GrowerAI" : (modelName || "");
    
    // For GrowerAI chats: use in-memory messages, skip database
    if (useGrowerAI) {
        // Initialize empty message array for this chat if it doesn't exist
        if (!window.growerAIMessages) {
            window.growerAIMessages = [];
        }
        
        renderMessages(window.growerAIMessages);
        
        // Show greeting only if no messages yet
        if (window.growerAIMessages.length === 0) {
            showGreetingIfEmpty();
        }
        return;
    }
    
    // For standard LLM chats: fetch from database as normal
    const chatMessages = await getChatMessages(chatId);
    renderMessages(chatMessages);
    if (!chatMessages || chatMessages.length === 0) {
        showGreetingIfEmpty();
    }
}
window.switchChat = switchChat;

// --- Streaming and Chat Logic ---
function startStreamingResponse(prompt) {
    if (window.lastWS && window.lastWS.readyState === 1) {
        window.lastWS.close();
    }
    const ws = new WebSocket(getWSUrl());
    window.lastWS = ws;

    window.lastWS.tokens = "";
    window.lastWS.sources = [];
    window.lastWS.tokenCount = 0;
    window.lastWS.streamStart = Date.now();
    window.lastWS.renderedError = false;

    window.lastWS.onopen = () => {
        window.lastWS.send(JSON.stringify({
            chatId: window.activeChatId,
            prompt: prompt,
            web_search: false
        }));
        document.getElementById("sendBtn").style.display = "none";
        document.getElementById("stopBtn").style.display = "inline-block";
        window.lastWS.streamStart = Date.now();
        window.lastWS.tokenCount = 0;
        window.lastWS.sources = [];
        window.lastWS.renderedError = false;

        if (window.webSearchEnabled) {
            showTypewriterPlaceholder('Searching');
            setTimeout(() => {
                const bubble = document.getElementById("streamingBubble");
                if (bubble && bubble.innerHTML.includes("Searching")) {
                    let html = '<span class="typewriter-anim">';
                    const word = "Thinking";
                    for (let i = 0; i < word.length; i++) {
                        html += `<span class="letter" style="--delay:${i*0.06}s">${word[i]}</span>`;
                    }
                    html += `<span class="dots-anim"><span>.</span><span>.</span><span>.</span></span></span>`;
                    bubble.innerHTML = html;
                }
            }, 4000);
        } else {
            showTypewriterPlaceholder('Thinking');
        }
    };

    window.lastWS.onmessage = (event) => {
        let msg;
        try {
            msg = JSON.parse(event.data);
        } catch (e) {
            console.error("JSON parse error", e, event.data);
            renderStreaming("Error streaming response.");
            window.lastWS.renderedError = true;
            document.getElementById("sendBtn").style.display = "inline-block";
            document.getElementById("stopBtn").style.display = "none";
            return;
        }

        if (msg.Choices && msg.Choices[0] && msg.Choices[0].Delta && msg.Choices[0].Delta.Content !== undefined) {
            window.lastWS.tokens += msg.Choices[0].Delta.Content;
            window.lastWS.tokenCount += msg.Choices[0].Delta.Content.length;
            renderStreaming(window.lastWS.tokens);
        }
// Auto search notification
if (msg.auto_search) {
    const chatMessagesDiv = document.getElementById("chatMessages");
    const badge = document.createElement("div");
    badge.textContent = "Auto Web Search ✓";
    badge.style.fontSize = "0.75rem";
    badge.style.opacity = "0.6";
    badge.style.margin = "4px 0";
    chatMessagesDiv.appendChild(badge);
    chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
}

        if (msg.token !== undefined) {
            window.lastWS.tokens += msg.token;
            window.lastWS.tokenCount += msg.token.length;
            renderStreaming(window.lastWS.tokens);
        }
        if (msg.sources) {
            window.lastWS.sources = msg.sources;
        }

        if (msg.event === "end" || (msg.FinishReason !== undefined && msg.FinishReason !== null)) {
            window.lastWS.close();
            document.getElementById("sendBtn").style.display = "inline-block";
            document.getElementById("stopBtn").style.display = "none";

            let finalMd = window.lastWS.tokens;

            let toksPerSec = "N/A";
            if (typeof msg.tokens_per_sec === "number") {
                toksPerSec = msg.tokens_per_sec.toFixed(2);
            } else {
                const seconds = (Date.now() - window.lastWS.streamStart) / 1000;
                toksPerSec = seconds > 0 ? (window.lastWS.tokenCount / seconds).toFixed(2) : "N/A";
            }
            finalMd += `\n\n_Tokens/sec: ${toksPerSec}_`;

            // For GrowerAI: finalize the streaming bubble, then add to memory
            if (window.isGrowerAIChat) {
                // Render final content in the streaming bubble
                renderStreaming(finalMd, window.lastWS.sources, true);
                
                // Store bot response in memory
                window.growerAIMessages.push({
                    sender: "bot",
                    content: finalMd,
                    created_at: new Date().toISOString()
                });
                
                // Remove the streamingBubble ID so it becomes a permanent message
                const bubble = document.getElementById("streamingBubble");
                if (bubble) {
                    bubble.removeAttribute("id");
                }
                
                return;
            }

            // For standard LLM chats: render and reload from database
            renderStreaming(finalMd, window.lastWS.sources, true);
            
            loadChatHistory();
            getChatMessages(window.activeChatId).then(async (messages) => {
                renderMessages(messages);

                if (Array.isArray(messages) && messages.length === 2) {
                    const firstPrompt = messages[0]?.content?.slice(0, 50) || "Chat";
                    await editChatTitle(window.activeChatId, firstPrompt);
                    await loadChatHistory();
                }
            });
        }
    };

    document.getElementById("stopBtn").onclick = () => {
        if (window.lastWS) window.lastWS.close();
        document.getElementById("sendBtn").style.display = "inline-block";
        document.getElementById("stopBtn").style.display = "none";
    };
}
window.startStreamingResponse = startStreamingResponse;

// --- Streaming renderer (PATCHED for thinking bubble) ---
function renderStreaming(mdText, sources, isFinal) {
    const bubble = document.getElementById("streamingBubble");
    if (!bubble) return;
    window.requestAnimationFrame(() => {
        bubble.innerHTML = renderWithThinkingBubbles(mdText);

        // Always scroll chatMessages div to bottom
        const chatMessagesDiv = document.getElementById("chatMessages");
        if (chatMessagesDiv) {
            chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
        }

        // Scroll the inner thinking bubble to bottom if present
        const thinkingBubble = bubble.querySelector('.thinking-bubble');
        if (thinkingBubble) {
            thinkingBubble.scrollTop = thinkingBubble.scrollHeight;
        }
    });
}
window.renderStreaming = renderStreaming;

// --- Chat history renderer (PATCHED for thinking bubble) ---
function renderMessages(messages) {
    const chatMessagesDiv = document.getElementById("chatMessages");
    chatMessagesDiv.innerHTML = "";
    messages.forEach(msg => {
        const div = document.createElement("div");
        div.className = msg.sender === "user" ? "user" : "llm";
        const bubble = document.createElement("div");
        bubble.className = "message";
        if (msg.sender === "bot") {
            bubble.innerHTML = renderWithThinkingBubbles(msg.content);
        } else {
            bubble.textContent = msg.content;
        }
        div.appendChild(bubble);
        chatMessagesDiv.appendChild(div);
    });
    chatMessagesDiv.scrollTop = chatMessagesDiv.scrollHeight;
}
window.renderMessages = renderMessages;

// --- User Management Functions: GLOBAL SCOPE ---
async function loadUsersTable() {
    const addUserBtn = document.getElementById("addUserBtn");
    const tbody = document.getElementById("usersTable").querySelector("tbody");
    tbody.innerHTML = "";
    const me = await getUserInfo();

    if (me.role === "admin") {
        addUserBtn.disabled = false;
        const users = await getUsers();
        if (!Array.isArray(users)) {
            tbody.innerHTML = `<tr><td colspan="4" class="text-danger">No users found or access denied.</td></tr>`;
            return;
        }
        users.forEach(user => {
            const tr = document.createElement("tr");
            tr.innerHTML = `
                <td>${user.username}</td>
                <td>${user.role}</td>
                <td>
                  <button class="btn btn-sm btn-outline-primary edit-user-btn" data-id="${user.id}">Edit</button>
                </td>
                <td>
                  <button class="btn btn-sm btn-outline-danger delete-user-btn" data-id="${user.id}" ${user.role === "admin" ? "disabled" : ""}>Delete</button>
                </td>
            `;
            tbody.appendChild(tr);
        });
        tbody.querySelectorAll('.edit-user-btn').forEach(btn => {
            btn.onclick = async function() {
                const id = btn.getAttribute('data-id');
                const newPass = prompt("Enter new password for user:");
                if (newPass) {
                    await editUserPassword(id, newPass);
                    loadUsersTable();
                }
            };
        });
        tbody.querySelectorAll('.delete-user-btn').forEach(btn => {
            btn.onclick = async function() {
                const id = btn.getAttribute('data-id');
                if (btn.disabled) return;
                if (confirm("Are you sure you want to delete this user?")) {
                    await deleteUser(id);
                    loadUsersTable();
                }
            };
        });
    } else {
        addUserBtn.disabled = true;
        const tr = document.createElement("tr");
        tr.innerHTML = `
            <td>${me.username}</td>
            <td>${me.role}</td>
            <td>
              <button class="btn btn-sm btn-outline-primary edit-user-btn" data-id="${me.id}">Edit</button>
            </td>
            <td>
              <button class="btn btn-sm btn-outline-danger delete-user-btn" data-id="${me.id}">Delete</button>
            </td>
        `;
        tbody.appendChild(tr);
        tbody.querySelector('.edit-user-btn').onclick = async function() {
            const newPass = prompt("Enter new password for your account:");
            if (newPass) {
                await editOwnPassword(newPass);
                loadUsersTable();
            }
        };
        tbody.querySelector('.delete-user-btn').onclick = async function() {
            if (confirm("Are you sure you want to delete your account?")) {
                await deleteOwnUser();
                clearJWT();
                window.location.href = SUBPATH + "/login";
            }
        };
    }
    document.getElementById("usersTable").parentElement.style.maxHeight = "225px";
}
window.loadUsersTable = loadUsersTable;

async function addUserFromModal() {
    const username = prompt("Enter new username:");
    if (!username) return;
    const password = prompt("Enter password for new user:");
    if (!password) return;
    await addUser(username, password);
    loadUsersTable();
}
window.addUserFromModal = addUserFromModal;
