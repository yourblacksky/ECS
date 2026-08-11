const DB_NAME = "ecs-browser-leaf";
const STORE_NAME = "identity";
const encoder = new TextEncoder();
const toolDefinitions = [
  {
    name: "calculate", version: "1.1.0", description: "基础数学计算", risk: "low",
    input_schema: { type: "object", properties: {
      a: { type: "number" },
      op: { type: "string", enum: ["add", "subtract", "multiply", "divide", "power", "log_base"] },
      b: { type: "number" }
    }, required: ["a", "op", "b"], additionalProperties: false }
  },
  {
    name: "clock", version: "1.0.0", description: "浏览器当前时间", risk: "low",
    input_schema: { type: "object", properties: {}, additionalProperties: false }
  },
  {
    name: "text_stats", version: "1.0.0", description: "文本字符、词和行统计", risk: "low",
    input_schema: { type: "object", properties: { text: { type: "string" } },
      required: ["text"], additionalProperties: false }
  },
  {
    name: "text_replace", version: "1.0.0", description: "精确替换文本", risk: "low",
    input_schema: { type: "object", properties: {
      text: { type: "string" }, old: { type: "string" }, new: { type: "string" }
    }, required: ["text", "old", "new"], additionalProperties: false }
  },
  {
    name: "make_id", version: "1.0.0", description: "生成随机标识", risk: "low",
    input_schema: { type: "object", properties: { prefix: { type: "string" } },
      additionalProperties: false }
  }
].map((tool) => ({ ...tool, requires_approval: false }));

const state = {
  identity: null,
  socket: null,
  heartbeat: null,
  reconnect: null,
  reconnectDelay: 2000,
  manuallyClosed: false,
  canceledTasks: new Set(),
  activeTasks: new Set(),
  activities: []
};

const $ = (selector) => document.querySelector(selector);

function openDatabase() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(STORE_NAME);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function databaseGet(key) {
  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const request = database.transaction(STORE_NAME).objectStore(STORE_NAME).get(key);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function databaseSet(key, value) {
  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, "readwrite");
    transaction.objectStore(STORE_NAME).put(value, key);
    transaction.oncomplete = resolve;
    transaction.onerror = () => reject(transaction.error);
  });
}

async function databaseClear() {
  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, "readwrite");
    transaction.objectStore(STORE_NAME).clear();
    transaction.oncomplete = resolve;
    transaction.onerror = () => reject(transaction.error);
  });
}

function bytesToHex(bytes) {
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function toBase64URL(buffer) {
  let binary = "";
  for (const value of new Uint8Array(buffer)) binary += String.fromCharCode(value);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

function randomPairingCode() {
  const value = new Uint32Array(1);
  crypto.getRandomValues(value);
  return String(value[0] % 1000000).padStart(6, "0");
}

async function loadIdentity() {
  let identity = await databaseGet("device");
  if (!identity) {
    const keys = await crypto.subtle.generateKey(
      { name: "ECDSA", namedCurve: "P-256" },
      false,
      ["sign", "verify"]
    );
    const rawPublicKey = await crypto.subtle.exportKey("raw", keys.publicKey);
    const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", rawPublicKey));
    identity = {
      keys,
      nodeID: `ecs_${bytesToHex(digest.slice(0, 16))}`,
      nodeName: `Browser ${bytesToHex(digest.slice(0, 3))}`,
      pairingCode: randomPairingCode()
    };
    await databaseSet("device", identity);
  }
  state.identity = identity;
  $("#nodeID").textContent = identity.nodeID;
  $("#nodeName").value = identity.nodeName;
  $("#pairingCode").textContent = identity.pairingCode;
  $("#parentOrigin").textContent = location.origin;
}

function capabilities() {
  return {
    protocol: "ecs/1",
    node_id: state.identity.nodeID,
    node: state.identity.nodeName,
    url: `browser://${state.identity.nodeID}`,
    model: "",
    platform: {
      name: "browser",
      runtime_mode: "client",
      topology_role: "leaf",
      accepts_children: false,
      supports_inbound: false,
      supports_background: false,
      supports_subprocess: false,
      supports_multicast: false,
      supports_push_wakeup: false
    },
    features: ["browser_leaf", "webcrypto_identity", "websocket_tasks"],
    tools: toolDefinitions
  };
}

function setStatus(kind, label) {
  const badge = $("#statusBadge");
  badge.className = `status ${kind}`;
  badge.querySelector("span").textContent = label;
}

function showNotice(message) {
  $("#notice").hidden = !message;
  $("#notice").textContent = message || "";
}

async function connect() {
  clearTimeout(state.reconnect);
  state.manuallyClosed = false;
  if (state.socket && state.socket.readyState < WebSocket.CLOSING) state.socket.close();
  state.identity.nodeName = $("#nodeName").value.trim() || state.identity.nodeName;
  $("#nodeName").value = state.identity.nodeName;
  await databaseSet("device", state.identity);

  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  const socket = new WebSocket(`${scheme}//${location.host}/v1/network/browser/connect`);
  state.socket = socket;
  setStatus("pending", "正在连接");
  $("#connectButton").disabled = true;

  socket.addEventListener("message", async (event) => {
    const message = JSON.parse(event.data);
    if (message.type === "challenge") {
      const publicKey = await crypto.subtle.exportKey("jwk", state.identity.keys.publicKey);
      const payload = `ecs-browser-v1\n${message.challenge}\n${state.identity.nodeID}`;
      const signature = await crypto.subtle.sign(
        { name: "ECDSA", hash: "SHA-256" },
        state.identity.keys.privateKey,
        encoder.encode(payload)
      );
      socket.send(JSON.stringify({
        type: "authenticate",
        node_id: state.identity.nodeID,
        node_name: state.identity.nodeName,
        pairing_code: state.identity.pairingCode,
        public_key: publicKey,
        signature: toBase64URL(signature),
        capabilities: capabilities()
      }));
      return;
    }
    if (message.type === "enrollment") {
      setStatus("pending", message.status === "approved" ? "等待激活" : "等待上级批准");
      showNotice(`入网申请 ${message.enrollment_id} 已提交。请在上级管理后台核对节点 ID 和配对码后批准。`);
      return;
    }
    if (message.type === "authenticated") {
      state.reconnectDelay = 2000;
      setStatus("online", "已连接");
      showNotice("");
      $("#connectButton").disabled = false;
      $("#connectButton").textContent = "重新连接";
      clearInterval(state.heartbeat);
      const interval = Math.max(5000, message.heartbeat_ms || 15000);
      state.heartbeat = setInterval(sendHeartbeat, interval);
      sendHeartbeat();
      return;
    }
    if (message.type === "heartbeat_ack") {
      return;
    }
    if (message.type === "task") {
      await runTask(message);
      return;
    }
    if (message.type === "task_cancel") {
      if (state.activeTasks.has(message.task_id)) state.canceledTasks.add(message.task_id);
      return;
    }
    if (message.type === "error") {
      setStatus("error", "连接被拒绝");
      showNotice(message.error || "上级拒绝了连接");
    }
  });

  socket.addEventListener("close", () => {
    clearInterval(state.heartbeat);
    $("#connectButton").disabled = false;
    if (state.manuallyClosed) {
      setStatus("offline", "已断开");
      return;
    }
    if (!$("#statusBadge").classList.contains("pending")) setStatus("offline", "连接中断");
    state.reconnect = setTimeout(connect, state.reconnectDelay);
    state.reconnectDelay = Math.min(30000, state.reconnectDelay * 1.7);
  });

  socket.addEventListener("error", () => {
    setStatus("error", "无法连接");
  });
}

function sendHeartbeat() {
  if (state.socket?.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify({ type: "heartbeat", sent_at: new Date().toISOString() }));
  }
}

function assertObject(argumentsValue) {
  if (!argumentsValue || typeof argumentsValue !== "object" || Array.isArray(argumentsValue)) {
    throw new Error("arguments 必须是对象");
  }
}

function validateArguments(toolName, value) {
  assertObject(value);
  const definition = toolDefinitions.find((tool) => tool.name === toolName);
  if (!definition) throw new Error(`浏览器没有工具 ${toolName}`);
  const schema = definition.input_schema;
  const properties = schema.properties || {};
  for (const required of schema.required || []) {
    if (!Object.hasOwn(value, required)) throw new Error(`缺少必填参数 ${required}`);
  }
  if (schema.additionalProperties === false) {
    const unknown = Object.keys(value).find((key) => !Object.hasOwn(properties, key));
    if (unknown) throw new Error(`不支持参数 ${unknown}`);
  }
  for (const [key, item] of Object.entries(value)) {
    const property = properties[key];
    if (!property) continue;
    if (property.type === "number" && typeof item !== "number") throw new Error(`${key} 必须是数字`);
    if (property.type === "string" && typeof item !== "string") throw new Error(`${key} 必须是字符串`);
    if (property.enum && !property.enum.includes(item)) throw new Error(`${key} 不在允许范围内`);
  }
}

const executors = {
  calculate(input) {
    assertObject(input);
    const { a, op, b } = input;
    if (typeof a !== "number" || typeof b !== "number") throw new Error("a 和 b 必须是数字");
    let result;
    if (op === "add") result = a + b;
    else if (op === "subtract") result = a - b;
    else if (op === "multiply") result = a * b;
    else if (op === "divide") {
      if (b === 0) throw new Error("不能除以零");
      result = a / b;
    } else if (op === "power") result = a ** b;
    else if (op === "log_base") {
      if (a <= 0 || b <= 0 || b === 1) throw new Error("对数参数无效");
      result = Math.log(a) / Math.log(b);
    } else throw new Error("不支持的计算操作");
    if (!Number.isFinite(result)) throw new Error("计算结果不是有限数字");
    return { result };
  },
  clock(input) {
    assertObject(input);
    const now = new Date();
    return { time: now.toISOString(), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone };
  },
  text_stats(input) {
    assertObject(input);
    if (typeof input.text !== "string") throw new Error("text 必须是字符串");
    return {
      characters: [...input.text].length,
      words: input.text.trim() ? input.text.trim().split(/\s+/u).length : 0,
      lines: input.text ? input.text.split("\n").length : 0
    };
  },
  text_replace(input) {
    assertObject(input);
    if (typeof input.text !== "string" || typeof input.old !== "string" || typeof input.new !== "string") {
      throw new Error("text、old、new 必须是字符串");
    }
    if (!input.old) throw new Error("old 不能为空");
    return { text: input.text.split(input.old).join(input.new) };
  },
  make_id(input) {
    assertObject(input);
    const prefix = typeof input.prefix === "string" ? input.prefix : "";
    const value = new Uint8Array(12);
    crypto.getRandomValues(value);
    return { id: prefix + bytesToHex(value) };
  }
};

async function runTask(message) {
  const started = performance.now();
  state.activeTasks.add(message.task_id);
  let result;
  let error = "";
  if (state.socket?.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify({ type: "task_ack", task_id: message.task_id }));
  }
  try {
    if (new Date(message.deadline).getTime() <= Date.now()) throw new Error("任务已经超过截止时间");
    const executor = executors[message.tool];
    if (!executor) throw new Error(`浏览器没有工具 ${message.tool}`);
    validateArguments(message.tool, message.arguments || {});
    result = await executor(message.arguments || {});
  } catch (taskError) {
    error = taskError.message;
  }
  if (state.canceledTasks.delete(message.task_id)) {
    state.activeTasks.delete(message.task_id);
    addActivity(message.tool, "任务已被上级取消", true);
    return;
  }
  state.activeTasks.delete(message.task_id);
  const duration = `${Math.round(performance.now() - started)}ms`;
  if (state.socket?.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify({
      type: "task_result",
      task_id: message.task_id,
      result,
      error,
      duration
    }));
  }
  addActivity(message.tool, error || JSON.stringify(result), Boolean(error));
}

function addActivity(tool, result, failed) {
  state.activities.unshift({ tool, result, failed, time: new Date() });
  state.activities = state.activities.slice(0, 30);
  renderActivities();
}

function renderActivities() {
  const list = $("#activity");
  if (!state.activities.length) {
    list.innerHTML = '<li class="empty">等待上级下发任务</li>';
    return;
  }
  list.replaceChildren(...state.activities.map((item) => {
    const row = document.createElement("li");
    if (item.failed) row.className = "failed";
    const top = document.createElement("div");
    top.className = "task-top";
    const name = document.createElement("strong");
    name.textContent = item.tool;
    const time = document.createElement("time");
    time.textContent = item.time.toLocaleTimeString();
    const result = document.createElement("p");
    result.className = "task-result";
    result.textContent = item.result;
    top.append(name, time);
    row.append(top, result);
    return row;
  }));
}

async function resetIdentity() {
  if (!confirm("重置后需要重新向上级申请入网，确定继续？")) return;
  state.manuallyClosed = true;
  state.socket?.close();
  clearInterval(state.heartbeat);
  await databaseClear();
  location.reload();
}

async function initialize() {
  if (!window.isSecureContext) {
    setStatus("error", "需要安全连接");
    showNotice("Web Crypto 身份需要 HTTPS；localhost 是浏览器允许的开发例外。");
    return;
  }
  await loadIdentity();
  $("#toolList").replaceChildren(...toolDefinitions.map((tool) => {
    const item = document.createElement("div");
    item.className = "tool";
    const name = document.createElement("code");
    name.textContent = tool.name;
    const description = document.createElement("span");
    description.textContent = tool.description;
    item.append(name, description);
    return item;
  }));
  if ("serviceWorker" in navigator) navigator.serviceWorker.register("/browser/sw.js").catch(() => {});
  connect();
}

$("#connectButton").addEventListener("click", connect);
$("#resetButton").addEventListener("click", resetIdentity);
$("#clearButton").addEventListener("click", () => {
  state.activities = [];
  renderActivities();
});
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") sendHeartbeat();
});

initialize().catch((error) => {
  setStatus("error", "初始化失败");
  showNotice(error.message);
});
