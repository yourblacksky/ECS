function ownKeys(e, r) { var t = Object.keys(e); if (Object.getOwnPropertySymbols) { var o = Object.getOwnPropertySymbols(e); r && (o = o.filter(function (r) { return Object.getOwnPropertyDescriptor(e, r).enumerable; })), t.push.apply(t, o); } return t; }
function _objectSpread(e) { for (var r = 1; r < arguments.length; r++) { var t = null != arguments[r] ? arguments[r] : {}; r % 2 ? ownKeys(Object(t), !0).forEach(function (r) { _defineProperty(e, r, t[r]); }) : Object.getOwnPropertyDescriptors ? Object.defineProperties(e, Object.getOwnPropertyDescriptors(t)) : ownKeys(Object(t)).forEach(function (r) { Object.defineProperty(e, r, Object.getOwnPropertyDescriptor(t, r)); }); } return e; }
function _defineProperty(e, r, t) { return (r = _toPropertyKey(r)) in e ? Object.defineProperty(e, r, { value: t, enumerable: !0, configurable: !0, writable: !0 }) : e[r] = t, e; }
function _toPropertyKey(t) { var i = _toPrimitive(t, "string"); return "symbol" == typeof i ? i : i + ""; }
function _toPrimitive(t, r) { if ("object" != typeof t || !t) return t; var e = t[Symbol.toPrimitive]; if (void 0 !== e) { var i = e.call(t, r || "default"); if ("object" != typeof i) return i; throw new TypeError("@@toPrimitive must return a primitive value."); } return ("string" === r ? String : Number)(t); }
const state = {
  overview: null,
  selectedNode: "local",
  tools: [],
  selectedTool: null,
  config: null,
  token: sessionStorage.getItem("ecs-admin-token") || "",
  debugSessionID: sessionStorage.getItem("ecs-debug-session-id") || "",
  dirty: false,
  debugTarget: "local",
  debugMessages: [],
  network: null,
  office: {
    components: [],
    orders: [],
    component: "",
    selectedOrder: "",
    detail: null
  }
};
const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];

// Android 8 system images can ship an older WebView without these helpers.
// Keep the admin console usable even when WebView cannot be updated through Play.
if (!Array.prototype.flatMap) {
  Array.prototype.flatMap = function (callback, thisArg) {
    return this.reduce((result, item, index, array) => result.concat(callback.call(thisArg, item, index, array)), []);
  };
}
if (!Object.fromEntries) {
  Object.fromEntries = function (entries) {
    return entries.reduce((result, entry) => {
      result[entry[0]] = entry[1];
      return result;
    }, {});
  };
}
if (!HTMLFormElement.prototype.requestSubmit) {
  HTMLFormElement.prototype.requestSubmit = function (submitter) {
    if (submitter) {
      submitter.click();
      return;
    }
    const event = new Event("submit", {
      bubbles: true,
      cancelable: true
    });
    if (this.dispatchEvent(event)) this.submit();
  };
}
function createDebugSessionID() {
  const timestamp = new Date().toISOString().replace(/\D/g, "").slice(0, 14);
  const random = crypto.getRandomValues(new Uint32Array(1))[0].toString(36).slice(0, 6);
  return `admin-${timestamp}-${random}`;
}
async function api(path, options = {}) {
  const headers = _objectSpread({
    Accept: "application/json"
  }, options.headers || {});
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const response = await fetch(path, _objectSpread(_objectSpread({}, options), {}, {
    headers
  }));
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error || `${response.status} ${response.statusText}`);
    error.payload = body;
    error.status = response.status;
    throw error;
  }
  return body;
}
function showNotice(message, error = false) {
  const node = $("#notice");
  node.textContent = message;
  node.className = `notice${error ? " error" : ""}`;
  node.hidden = false;
  clearTimeout(showNotice.timer);
  showNotice.timer = setTimeout(() => node.hidden = true, 5000);
}
function formatBytes(value) {
  if (value == null) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let index = 0;
  let size = value;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index++;
  }
  return `${size.toFixed(index > 1 ? 1 : 0)} ${units[index]}`;
}
function formatUptime(seconds) {
  if (seconds == null) return "—";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor(seconds % 86400 / 3600);
  const minutes = Math.floor(seconds % 3600 / 60);
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : `${minutes}m`;
}
function formatTime(value) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "short",
    timeStyle: "medium"
  }).format(new Date(value));
}
function currentNode() {
  var _state$overview, _state$overview2;
  return ((_state$overview = state.overview) === null || _state$overview === void 0 ? void 0 : _state$overview.nodes.find(node => node.id === state.selectedNode)) || ((_state$overview2 = state.overview) === null || _state$overview2 === void 0 ? void 0 : _state$overview2.nodes[0]);
}
async function loadOverview() {
  state.overview = await api("/v1/admin/overview");
  if (!state.overview.nodes.some(node => node.id === state.selectedNode)) state.selectedNode = state.overview.local_node_id;
  renderNodePicker();
  renderOverview();
  renderTopology();
  renderDebugControls();
  setConnection(true);
}
async function loadNetworkRelationships() {
  try {
    state.network = await api("/v1/admin/network/relationships");
  } catch (error) {
    if (error.message.includes("404")) state.network = null;else throw error;
  }
  renderNetworkRelationships();
}
function setConnection(ok, text) {
  $(".pulse-line").classList.toggle("error", !ok);
  $("#connectionText").textContent = text || (ok ? "已连接 ECS" : "连接失败");
  const status = $(".status-chip");
  if (!ok && status) {
    status.className = "status-chip waiting";
    status.innerHTML = `<i></i> ${text !== null && text !== void 0 && text.toLowerCase().includes("authorization") ? "需要凭证" : "等待连接"}`;
  }
}
function renderNodePicker() {
  const select = $("#nodeSelect");
  select.innerHTML = state.overview.nodes.map(node => `<option value="${escapeHTML(node.id)}">${escapeHTML(node.name)} · ${escapeHTML(node.source)}</option>`).join("");
  select.value = state.selectedNode;
}
function renderOverview() {
  var _runtime$goroutines, _capabilities$pipelin, _capabilities$tools, _capabilities$skills, _capabilities$availab, _capabilities$pipelin2;
  const node = currentNode();
  if (!node) return;
  const capabilities = node.capabilities || {};
  const runtime = node.runtime;
  $("#nodeName").textContent = node.name;
  $("#nodeUrl").textContent = node.url || "地址尚未声明";
  $("#nodeSource").textContent = node.source.toUpperCase();
  $("#uptime").textContent = formatUptime(runtime === null || runtime === void 0 ? void 0 : runtime.uptime_seconds);
  $("#requests").textContent = runtime ? `${runtime.active_requests} / ${runtime.request_capacity}` : "REMOTE";
  $("#memory").textContent = formatBytes(runtime === null || runtime === void 0 ? void 0 : runtime.memory_alloc_bytes);
  $("#goroutines").textContent = (_runtime$goroutines = runtime === null || runtime === void 0 ? void 0 : runtime.goroutines) !== null && _runtime$goroutines !== void 0 ? _runtime$goroutines : "—";
  $("#modelName").textContent = capabilities.model || "—";
  $("#protocolName").textContent = capabilities.protocol || "—";
  $("#pipelineName").textContent = ((_capabilities$pipelin = capabilities.pipelines) === null || _capabilities$pipelin === void 0 || (_capabilities$pipelin = _capabilities$pipelin.find(item => item.default)) === null || _capabilities$pipelin === void 0 ? void 0 : _capabilities$pipelin.name) || "—";
  $("#lastSeen").textContent = node.source === "local" ? "当前" : formatTime(node.last_seen);
  const callable = callableCapabilities(capabilities);
  const stats = [["TOOLS · REGISTERED", ((_capabilities$tools = capabilities.tools) === null || _capabilities$tools === void 0 ? void 0 : _capabilities$tools.length) || 0], ["TOOLS · CALLABLE", callable.tools.length], ["SKILLS · REGISTERED", ((_capabilities$skills = capabilities.skills) === null || _capabilities$skills === void 0 ? void 0 : _capabilities$skills.length) || 0], ["SKILLS · CALLABLE", callable.skills.length], ["UNAVAILABLE", ((_capabilities$availab = capabilities.availability) === null || _capabilities$availab === void 0 ? void 0 : _capabilities$availab.filter(item => item.available === false).length) || 0], ["PIPELINES", ((_capabilities$pipelin2 = capabilities.pipelines) === null || _capabilities$pipelin2 === void 0 ? void 0 : _capabilities$pipelin2.length) || 0]];
  $("#capabilityStats").innerHTML = stats.map(([label, value]) => `<div><strong>${value}</strong><span>${label}</span></div>`).join("");
  const platform = capabilities.platform || {};
  const platformTags = [platform.name, platform.runtime_mode, platform.supports_background ? "background" : "", platform.supports_subprocess ? "subprocess" : "in-process", platform.supports_multicast ? "multicast" : ""].filter(Boolean);
  const unavailableTags = (capabilities.availability || []).filter(item => item.available === false).map(item => `${item.kind}:${item.name} (${item.reason || "unavailable"})`);
  const officeTags = (capabilities.office_components || []).map(component => component.ready ? `office:${component.name} (ready)` : `office:${component.name} (missing: ${(component.missing_connectors || []).join(", ") || "connector"})`);
  const featureTags = [...platformTags, ...(capabilities.features || []), ...officeTags, ...unavailableTags];
  $("#featureList").innerHTML = featureTags.map(feature => `<span class="tag">${escapeHTML(feature)}</span>`).join("") || '<span class="subdued">暂无能力声明</span>';
  const status = $(".status-chip");
  status.className = `status-chip ${node.status === "online" ? "online" : "waiting"}`;
  status.innerHTML = `<i></i> ${escapeHTML(node.status.toUpperCase())}`;
  renderToolsForNode();
}
function callableCapabilities(capabilities) {
  var _pipeline$executors;
  const pipelines = capabilities.pipelines || [];
  const pipeline = pipelines.find(item => item.default) || pipelines[0];
  const executorName = (pipeline === null || pipeline === void 0 ? void 0 : pipeline.fallback_executor) || (pipeline === null || pipeline === void 0 || (_pipeline$executors = pipeline.executors) === null || _pipeline$executors === void 0 ? void 0 : _pipeline$executors[0]);
  const executor = (capabilities.agents || []).find(item => item.name === executorName);
  const unavailable = new Set((capabilities.availability || []).filter(item => item.available === false).map(item => `${item.kind}:${item.name}`));
  const registeredTools = new Set((capabilities.tools || []).map(item => item.name));
  const registeredSkills = new Set((capabilities.skills || []).map(item => item.name));
  return {
    tools: ((executor === null || executor === void 0 ? void 0 : executor.allowed_tools) || []).filter(name => registeredTools.has(name) && !unavailable.has(`tool:${name}`)),
    skills: ((executor === null || executor === void 0 ? void 0 : executor.allowed_skills) || []).filter(name => registeredSkills.has(name) && !unavailable.has(`skill:${name}`))
  };
}
function officeIsLocal() {
  var _currentNode, _state$overview3;
  return ((_currentNode = currentNode()) === null || _currentNode === void 0 ? void 0 : _currentNode.id) === ((_state$overview3 = state.overview) === null || _state$overview3 === void 0 ? void 0 : _state$overview3.local_node_id);
}
async function loadOffice() {
  var _$;
  const node = currentNode();
  if (!node) return;
  if (!officeIsLocal()) {
    var _node$capabilities;
    state.office.components = ((_node$capabilities = node.capabilities) === null || _node$capabilities === void 0 ? void 0 : _node$capabilities.office_components) || [];
    state.office.orders = [];
    state.office.selectedOrder = "";
    state.office.detail = null;
    renderOffice();
    return;
  }
  const status = ((_$ = $("#officeStatusFilter")) === null || _$ === void 0 ? void 0 : _$.value) || "";
  const query = new URLSearchParams({
    limit: "100"
  });
  if (state.office.component) query.set("component", state.office.component);
  if (status) query.set("status", status);
  const [components, orders] = await Promise.all([api("/v1/office/components"), api(`/v1/office/work-orders?${query.toString()}`)]);
  state.office.components = components;
  state.office.orders = orders;
  if (state.office.selectedOrder && !orders.some(order => order.id === state.office.selectedOrder)) {
    state.office.selectedOrder = "";
    state.office.detail = null;
  }
  renderOffice();
  if (state.office.selectedOrder) await loadOfficeOrder(state.office.selectedOrder, false);
}
function renderOffice() {
  const components = state.office.components || [];
  const orders = state.office.orders || [];
  renderOfficeDispatch();
  $("#officeRemoteNotice").hidden = officeIsLocal();
  $("#officeComponentCount").textContent = components.length;
  $("#officeReadyCount").textContent = components.filter(component => component.ready).length;
  $("#officeOrderCount").textContent = orders.length;
  $("#officePendingCount").textContent = orders.reduce((sum, order) => sum + (order.pending_tasks || 0), 0);
  $("#clearOfficeFilter").disabled = !state.office.component;
  $("#officeComponentList").innerHTML = components.map(component => {
    const missing = component.missing_connectors || [];
    return `<button class="office-component ${state.office.component === component.name ? "active" : ""}" data-component="${escapeHTML(component.name)}">
      <span class="office-item-head">
        <strong>${escapeHTML(component.name)}</strong>
        <span class="office-status ${component.ready ? "ready" : "blocked"}">${component.ready ? "READY" : "BLOCKED"}</span>
      </span>
      <p>${escapeHTML(component.description || "")}</p>
      <small>${component.stages || 0} STAGES · ${(component.agents || []).length} AGENTS</small>
      ${missing.length ? `<div class="connector-list">${missing.map(name => `<span>${escapeHTML(name)}</span>`).join("")}</div>` : ""}
    </button>`;
  }).join("") || '<div class="office-empty">当前节点没有声明办公组件。</div>';
  $$("#officeComponentList .office-component").forEach(button => button.addEventListener("click", async () => {
    state.office.component = button.dataset.component;
    state.office.selectedOrder = "";
    state.office.detail = null;
    try {
      await loadOffice();
    } catch (error) {
      handleError(error);
    }
  }));
  $("#officeOrderList").innerHTML = orders.map(order => {
    var _order$missing_connec;
    return `<button class="office-order ${state.office.selectedOrder === order.id ? "active" : ""}" data-order="${escapeHTML(order.id)}">
    <span class="office-item-head">
      <strong>${escapeHTML(order.component)}</strong>
      <span class="office-status ${officeStatusClass(order.status)}">${escapeHTML(order.status)}</span>
    </span>
    <p class="mono">${escapeHTML(order.id)}</p>
    <small>${escapeHTML(formatTime(order.created_at))} · ${order.tasks} TASKS · ${order.handoffs} HANDOFFS · ${order.active_attempts || 0} ACTIVE · ${order.pending_outbox || 0} OUTBOX</small>
    ${(_order$missing_connec = order.missing_connectors) !== null && _order$missing_connec !== void 0 && _order$missing_connec.length ? `<div class="connector-list">${order.missing_connectors.map(name => `<span>${escapeHTML(name)}</span>`).join("")}</div>` : ""}
  </button>`;
  }).join("") || `<div class="office-empty">${officeIsLocal() ? "当前筛选条件下没有工单。" : "远程工单详情尚未授权读取。"}</div>`;
  $$("#officeOrderList .office-order").forEach(button => button.addEventListener("click", () => {
    loadOfficeOrder(button.dataset.order).catch(handleError);
  }));
  if (!state.office.detail) renderOfficeDetail(null);
}
function renderOfficeDispatch() {
  var _state$config, _currentNode2;
  const select = $("#officeDispatchComponent");
  const previous = select.value;
  select.innerHTML = (state.office.components || []).map(component => `<option value="${escapeHTML(component.name)}">${escapeHTML(component.name)} · ${component.stages || 0} stages</option>`).join("");
  if ([...select.options].some(option => option.value === previous)) select.value = previous;
  const configuredGroups = Object.keys(((_state$config = state.config) === null || _state$config === void 0 ? void 0 : _state$config.groups) || {});
  const capabilityGroups = ((_currentNode2 = currentNode()) === null || _currentNode2 === void 0 || (_currentNode2 = _currentNode2.capabilities) === null || _currentNode2 === void 0 ? void 0 : _currentNode2.groups) || [];
  const groups = [...new Set([...configuredGroups, ...capabilityGroups])].sort();
  $("#officeGroupOptions").innerHTML = groups.map(group => `<option value="${escapeHTML(group)}"></option>`).join("");
  if (!$("#officeDispatchGroup").value && groups.length) $("#officeDispatchGroup").value = groups[0];
  const local = officeIsLocal();
  $("#officeDispatchForm").querySelectorAll("input, select, textarea, button").forEach(element => {
    element.disabled = !local || !(state.office.components || []).length;
  });
  $("#officeDispatchState").textContent = local ? (state.office.components || []).length ? "选择组件并描述任务" : "当前没有可投递组件" : "请切换到本地控制节点进行投递";
}
async function submitOfficeWorkOrder(event) {
  var _event$submitter;
  event.preventDefault();
  const action = ((_event$submitter = event.submitter) === null || _event$submitter === void 0 ? void 0 : _event$submitter.value) || "dispatch";
  const component = $("#officeDispatchComponent").value;
  const prompt = $("#officeDispatchPrompt").value.trim();
  const group = $("#officeDispatchGroup").value.trim();
  const node = $("#officeDispatchNode").value.trim();
  const requestedBy = $("#officeDispatchRequester").value.trim();
  if (!component || !prompt) return;
  if (action === "dispatch" && !group) {
    showNotice("创建并派发时必须填写目标 Group", true);
    $("#officeDispatchGroup").focus();
    return;
  }
  let context = {};
  let input;
  try {
    var _find;
    input = officeInputFromText(prompt, (_find = (state.office.components || []).find(item => item.name === component)) === null || _find === void 0 ? void 0 : _find.input_schema);
  } catch (error) {
    showNotice(`任务输入格式错误：${error.message}`, true);
    $("#officeDispatchPrompt").focus();
    return;
  }
  const contextText = $("#officeDispatchContext").value.trim();
  try {
    if (contextText) context = JSON.parse(contextText);
    if (!context || Array.isArray(context) || typeof context !== "object") throw new Error("上下文必须是 JSON 对象");
    for (const value of Object.values(context)) {
      if (typeof value !== "string") throw new Error("上下文中的值必须是字符串");
    }
  } catch (error) {
    showNotice(`上下文格式错误：${error.message}`, true);
    $("#officeDispatchContext").focus();
    return;
  }
  const buttons = $$("#officeDispatchForm button");
  buttons.forEach(button => button.disabled = true);
  $("#officeDispatchState").textContent = action === "dispatch" ? "正在创建并派发…" : "正在创建工单…";
  try {
    let order = await api(`/v1/office/components/${encodeURIComponent(component)}/work-orders`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json"
      },
      body: JSON.stringify({
        input,
        context,
        requested_by: requestedBy
      })
    });
    if (action === "dispatch") {
      order = await api(`/v1/office/work-orders/${encodeURIComponent(order.id)}/dispatch`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify(_objectSpread({
          group
        }, node ? {
          node
        } : {}))
      });
    }
    state.office.selectedOrder = order.id;
    state.office.detail = order;
    $("#officeDispatchPrompt").value = "";
    await loadOffice();
    await loadOfficeOrder(order.id);
    showNotice(action === "dispatch" ? `工单 ${order.id} 已派发并处理` : `工单 ${order.id} 已创建`);
  } catch (error) {
    showNotice(`工单投递失败：${error.message}`, true);
  } finally {
    buttons.forEach(button => button.disabled = false);
    $("#officeDispatchState").textContent = "选择组件并描述任务";
  }
}
function officeInputFromText(text, schema = {}) {
  const trimmed = text.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) return JSON.parse(trimmed);
  if (schema.type === "string") return trimmed;
  if (schema.type !== "object") throw new Error("该组件需要 JSON 输入");
  const properties = schema.properties || {};
  const required = schema.required || [];
  const preferred = ["prompt", "request", "topic", "goal", "task", "query", "text", "content"];
  const stringFields = Object.entries(properties).filter(([, definition]) => (definition === null || definition === void 0 ? void 0 : definition.type) === "string").map(([name]) => name);
  const field = preferred.find(name => stringFields.includes(name) && (required.includes(name) || required.length === 0)) || (required.length === 1 && stringFields.includes(required[0]) ? required[0] : "") || (required.length === 0 && stringFields.length === 1 ? stringFields[0] : "");
  if (!field) throw new Error("该组件包含多个输入字段，请填写符合 input_schema 的 JSON");
  return {
    [field]: trimmed
  };
}
async function loadOfficeOrder(id, rerenderList = true) {
  if (!officeIsLocal()) return;
  const detail = await api(`/v1/office/work-orders/${encodeURIComponent(id)}`);
  state.office.selectedOrder = id;
  state.office.detail = detail;
  if (rerenderList) renderOffice();
  renderOfficeDetail(detail);
}
function renderOfficeDetail(order) {
  var _order$missing_connec2;
  if (!order) {
    $("#officeDetailTitle").textContent = "选择一个工单";
    $("#officeDetailStatus").textContent = "—";
    $("#officeDetailStatus").className = "office-status";
    $("#officeDetail").className = "office-detail-empty";
    $("#officeDetail").innerHTML = "<strong>尚未选择工单</strong><span>这里将展示任务 DAG、结构化交接、审批、连接器和审计事件。</span>";
    return;
  }
  $("#officeDetailTitle").textContent = order.component;
  $("#officeDetailStatus").textContent = order.status;
  $("#officeDetailStatus").className = `office-status ${officeStatusClass(order.status)}`;
  $("#officeDetail").className = "office-detail";
  const taskNames = Object.fromEntries((order.tasks || []).map(task => [task.id, task.name]));
  const tasks = (order.tasks || []).map((task, index) => {
    var _task$approval, _task$result, _task$result2;
    const approval = (_task$approval = task.approval) !== null && _task$approval !== void 0 && _task$approval.mode && task.approval.mode !== "none" ? `<span class="approval-chip">${escapeHTML(task.approval.mode)} · ${escapeHTML(task.approval.risk || "unspecified")}</span>` : "";
    const approvalRecord = task.approval_record;
    const approvalGate = (approvalRecord === null || approvalRecord === void 0 ? void 0 : approvalRecord.status) === "pending" ? `<div class="office-approval-gate" data-order="${escapeHTML(order.id)}" data-task="${escapeHTML(task.stage_id || task.id)}" data-approval="${escapeHTML(approvalRecord.id)}">
          <div class="approval-gate-head">
            <strong>等待人工审批</strong>
            <span>${escapeHTML(approvalRecord.risk || "unspecified")} · ${escapeHTML(formatTime(approvalRecord.expires_at))} 过期</span>
          </div>
          <div class="approval-scope-list">
            ${(approvalRecord.skills || []).map(name => `<span>SKILL ${escapeHTML(name)}</span>`).join("")}
            ${(approvalRecord.tools || []).map(name => `<span>TOOL ${escapeHTML(name)}</span>`).join("")}
            ${(approvalRecord.connectors || []).map(name => `<span>CONNECTOR ${escapeHTML(name)}</span>`).join("")}
          </div>
          <div class="approval-fields">
            <input class="approval-actor" value="admin-console" aria-label="审批人" placeholder="审批人">
            <input class="approval-opinion" aria-label="审批意见" placeholder="审批意见（可选）">
            <button class="primary-button decide-office-approval" data-decision="approve">批准并放行</button>
            <button class="danger-button decide-office-approval" data-decision="reject">拒绝</button>
          </div>
        </div>` : approvalRecord ? `<div class="office-approval-decision ${escapeHTML(approvalRecord.status)}">
            <strong>${escapeHTML(approvalRecord.status)}</strong>
            <span>${escapeHTML(approvalRecord.decided_by || "system")} · ${escapeHTML(formatTime(approvalRecord.decided_at))}${approvalRecord.opinion ? ` · ${escapeHTML(approvalRecord.opinion)}` : ""}</span>
          </div>` : "";
    const dependencies = (task.depends_on || []).map(id => taskNames[id] || id);
    const toolRuns = ((_task$result = task.result) === null || _task$result === void 0 ? void 0 : _task$result.tool_runs) || [];
    const taskResult = ((_task$result2 = task.result) === null || _task$result2 === void 0 || (_task$result2 = _task$result2.message) === null || _task$result2 === void 0 ? void 0 : _task$result2.content) || "";
    const receipts = task.receipts || [];
    const attempts = task.attempts || [];
    const compensation = task.compensation_record;
    const duration = task.started_at && task.completed_at ? Math.max(0, new Date(task.completed_at) - new Date(task.started_at)) : null;
    return `<div class="office-task">
      <div class="task-index">${String(index + 1).padStart(2, "0")}</div>
      <div>
        <span class="office-item-head"><strong>${escapeHTML(task.name)}</strong><span class="office-status ${officeStatusClass(task.status)}">${escapeHTML(task.status)}</span></span>
        <p>${escapeHTML(task.purpose || "")}</p>
        <div class="task-meta">
          <span>AGENT ${escapeHTML(task.agent)} / ${escapeHTML(task.agent_role)}</span>
          ${task.assigned_node ? `<span>NODE ${escapeHTML(task.assigned_node)}</span>` : ""}
          ${duration !== null ? `<span>${duration} MS</span>` : ""}
          <span>FAILURE ${escapeHTML(task.on_failure || "fail")}</span>
          ${approval}
        </div>
        ${dependencies.length ? `<small>依赖：${escapeHTML(dependencies.join("、"))}</small>` : '<small>入口任务</small>'}
        ${(task.connectors || []).length ? `<div class="connector-list">${task.connectors.map(name => `<span>${escapeHTML(name)}</span>`).join("")}</div>` : ""}
        ${approvalGate}
        ${attempts.length ? `<details class="office-raw"><summary>执行 Attempt · ${attempts.length}${task.active_attempt_id ? " · ACTIVE" : ""}</summary><pre>${escapeHTML(JSON.stringify(attempts, null, 2))}</pre></details>` : ""}
        ${receipts.length ? `<details class="office-raw"><summary>已验证执行回执 · ${receipts.length}</summary><pre>${escapeHTML(JSON.stringify(receipts, null, 2))}</pre></details>` : ""}
        ${compensation ? `<div class="office-compensation ${escapeHTML(compensation.status)}">
          <strong>${escapeHTML(compensation.status)}</strong>
          <span>${escapeHTML(compensation.action)} · ${escapeHTML(compensation.connector)}${compensation.tool ? ` · ${escapeHTML(compensation.tool)}` : ""}</span>
          ${compensation.error ? `<small>${escapeHTML(compensation.error)}</small>` : ""}
          ${(compensation.receipts || []).length ? `<details class="office-raw"><summary>补偿回执 · ${compensation.receipts.length}</summary><pre>${escapeHTML(JSON.stringify(compensation.receipts, null, 2))}</pre></details>` : ""}
        </div>` : ""}
        ${task.error ? `<div class="office-warning"><strong>执行错误</strong><span>${escapeHTML(task.error)}</span></div>` : ""}
        ${taskResult || toolRuns.length ? `<details class="office-raw"><summary>执行结果${toolRuns.length ? ` · ${toolRuns.length} TOOL RUNS` : ""}</summary>
          ${taskResult ? `<pre>${escapeHTML(taskResult)}</pre>` : ""}
          ${toolRuns.length ? `<pre>${escapeHTML(JSON.stringify(toolRuns, null, 2))}</pre>` : ""}
        </details>` : ""}
      </div>
    </div>`;
  }).join("");
  const handoffs = (order.handoffs || []).map(handoff => `<div class="handoff-row">
    <strong>${escapeHTML(taskNames[handoff.from_task] || handoff.from_task)}</strong>
    <span>→</span>
    <strong>${escapeHTML(taskNames[handoff.to_task] || handoff.to_task)}</strong>
    <small>${escapeHTML((handoff.artifacts || []).join(", ") || "无显式产物")} · ${escapeHTML(handoff.integrity)}</small>
  </div>`).join("") || '<div class="office-empty">没有跨任务交接。</div>';
  const audit = (order.audit || []).map(event => `<div class="audit-row">
    <span>${escapeHTML(formatTime(event.at))}</span>
    <strong>${escapeHTML(event.action)}</strong>
    <small>${escapeHTML(event.actor || "system")}</small>
    <code>${escapeHTML(JSON.stringify(event.details || {}))}</code>
  </div>`).join("") || '<div class="office-empty">没有审计事件。</div>';
  const context = order.context && Object.keys(order.context).length ? JSON.stringify(order.context, null, 2) : "{}";
  const outbox = order.outbox || [];
  const finalResult = renderOfficeFinalResult(order);
  $("#officeDetail").innerHTML = `
    <div class="office-order-meta">
      <div><span>WORK ORDER</span><strong>${escapeHTML(order.id)}</strong></div>
      <div><span>CREATED</span><strong>${escapeHTML(formatTime(order.created_at))}</strong></div>
      <div><span>REQUESTED BY</span><strong>${escapeHTML(order.requested_by || "anonymous")}</strong></div>
      <div><span>CONNECTORS</span><strong>${order.ready ? "READY" : `${(order.missing_connectors || []).length} MISSING`}</strong></div>
    </div>
    ${(_order$missing_connec2 = order.missing_connectors) !== null && _order$missing_connec2 !== void 0 && _order$missing_connec2.length ? `<div class="office-warning"><strong>缺失连接器</strong>${order.missing_connectors.map(name => `<span>${escapeHTML(name)}</span>`).join("")}</div>` : ""}
    ${order.status === "recovery_required" ? `<div class="office-warning"><strong>执行结果未知，需要恢复</strong><span>将沿用原业务幂等键创建新的 Attempt；请先确认目标 Group。</span><button class="primary-button recover-office-order" data-order="${escapeHTML(order.id)}">恢复执行</button></div>` : ""}
    ${finalResult}
    <section class="office-detail-section"><h4>任务 DAG</h4><div class="office-task-graph">${tasks}</div></section>
    <section class="office-detail-section"><h4>Durable Outbox</h4>${outbox.length ? `<details class="office-raw" open><summary>OUTBOX EVENTS · ${outbox.length}</summary><pre>${escapeHTML(JSON.stringify(outbox, null, 2))}</pre></details>` : '<div class="office-empty">尚无执行 Intent。</div>'}</section>
    <section class="office-detail-section"><h4>结构化 Handoff</h4><div class="handoff-list">${handoffs}</div></section>
    <section class="office-detail-section"><h4>审计事件</h4><div class="audit-list">${audit}</div></section>
    <details class="office-raw"><summary>输入与结构化上下文</summary><h5>INPUT</h5><pre>${escapeHTML(JSON.stringify(order.input || {}, null, 2))}</pre><h5>CONTEXT</h5><pre>${escapeHTML(context)}</pre></details>
    ${order.trace_id ? `<div class="office-trace"><button class="secondary-button load-office-trace" data-trace="${escapeHTML(order.trace_id)}" data-target="officePlanTraceView">读取编排 Trace</button><span class="mono">${escapeHTML(order.trace_id)}</span></div><pre id="officePlanTraceView"></pre>` : ""}
    ${order.execution_trace_id ? `<div class="office-trace"><button class="secondary-button load-office-trace" data-trace="${escapeHTML(order.execution_trace_id)}" data-target="officeExecutionTraceView">读取执行 Trace</button><span class="mono">${escapeHTML(order.execution_trace_id)}</span></div><pre id="officeExecutionTraceView"></pre>` : ""}
  `;
  $$(".load-office-trace").forEach(button => button.addEventListener("click", () => {
    loadOfficeTrace(button.dataset.trace, button.dataset.target);
  }));
  $$(".decide-office-approval").forEach(button => button.addEventListener("click", () => {
    decideOfficeApproval(button).catch(handleError);
  }));
  $$(".recover-office-order").forEach(button => button.addEventListener("click", () => {
    recoverOfficeWorkOrder(button).catch(handleError);
  }));
}

async function recoverOfficeWorkOrder(button) {
  const group = $("#officeDispatchGroup").value.trim();
  const node = $("#officeDispatchNode").value.trim();
  if (!group) {
    showNotice("恢复执行前必须在投递区填写目标 Group", true);
    $("#officeDispatchGroup").focus();
    return;
  }
  button.disabled = true;
  try {
    const order = await api(`/v1/office/work-orders/${encodeURIComponent(button.dataset.order)}/dispatch`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json"
      },
      body: JSON.stringify(_objectSpread({
        group
      }, node ? {
        node
      } : {}))
    });
    state.office.detail = order;
    renderOfficeDetail(order);
    await loadOfficeOrders();
    showNotice(order.status === "recovery_required" ? "恢复尝试仍需处理" : `工单 ${order.id} 已恢复执行`);
  } finally {
    button.disabled = false;
  }
}
async function decideOfficeApproval(button) {
  var _gate$querySelector, _gate$querySelector2;
  const gate = button.closest(".office-approval-gate");
  if (!gate) return;
  const decision = button.dataset.decision;
  const actor = ((_gate$querySelector = gate.querySelector(".approval-actor")) === null || _gate$querySelector === void 0 ? void 0 : _gate$querySelector.value.trim()) || "admin-console";
  const opinion = ((_gate$querySelector2 = gate.querySelector(".approval-opinion")) === null || _gate$querySelector2 === void 0 ? void 0 : _gate$querySelector2.value.trim()) || "";
  gate.querySelectorAll("button").forEach(item => item.disabled = true);
  try {
    const order = await api(`/v1/admin/office/work-orders/${encodeURIComponent(gate.dataset.order)}/tasks/${encodeURIComponent(gate.dataset.task)}/approval`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json"
      },
      body: JSON.stringify({
        approval_id: gate.dataset.approval,
        decision,
        actor,
        opinion
      })
    });
    state.office.detail = order;
    renderOfficeDetail(order);
    await loadOfficeOrders();
    showNotice(decision === "approve" ? "审批已通过，可以重新派发工单" : "审批已拒绝");
  } catch (error) {
    gate.querySelectorAll("button").forEach(item => item.disabled = false);
    throw error;
  }
}
function renderOfficeFinalResult(order) {
  var _finalTask$result, _finalTask$result2;
  const orderTasks = order.tasks || [];
  const completed = order.status === "completed";
  const finalTask = [...orderTasks].reverse().find(task => {
    var _task$result3, _task$result4;
    return completed ? (_task$result3 = task.result) === null || _task$result3 === void 0 || (_task$result3 = _task$result3.message) === null || _task$result3 === void 0 ? void 0 : _task$result3.content : task.error || ((_task$result4 = task.result) === null || _task$result4 === void 0 || (_task$result4 = _task$result4.message) === null || _task$result4 === void 0 ? void 0 : _task$result4.content);
  });
  const message = (finalTask === null || finalTask === void 0 || (_finalTask$result = finalTask.result) === null || _finalTask$result === void 0 || (_finalTask$result = _finalTask$result.message) === null || _finalTask$result === void 0 ? void 0 : _finalTask$result.content) || (finalTask === null || finalTask === void 0 ? void 0 : finalTask.error) || "";
  const toolRuns = (finalTask === null || finalTask === void 0 || (_finalTask$result2 = finalTask.result) === null || _finalTask$result2 === void 0 ? void 0 : _finalTask$result2.tool_runs) || [];
  const failed = ["failed", "rejected", "blocked"].includes(order.status) || Boolean(finalTask === null || finalTask === void 0 ? void 0 : finalTask.error);
  const stateLabel = completed ? "FINAL" : failed ? "FAILED" : "PENDING";
  const content = message || (failed ? "工单未产生可用的最终结果，请查看失败任务和 Trace。" : "工单仍在处理，最终结果将在 verifier 或末级任务完成后显示。");
  return `<section class="office-final-result ${failed ? "failed" : completed ? "completed" : "pending"}">
    <div class="office-final-head">
      <div><span>WORK ORDER RESULT</span><h4>工单最终结果</h4></div>
      <strong>${stateLabel}</strong>
    </div>
    <div class="office-final-content">${escapeHTML(content)}</div>
    ${finalTask ? `<div class="office-final-meta">
      <span>STAGE ${escapeHTML(finalTask.name)}</span>
      <span>AGENT ${escapeHTML(finalTask.agent || finalTask.agent_role || "unknown")}</span>
      ${finalTask.assigned_node ? `<span>NODE ${escapeHTML(finalTask.assigned_node)}</span>` : ""}
      ${finalTask.trace_id ? `<span>TRACE ${escapeHTML(finalTask.trace_id)}</span>` : ""}
    </div>` : ""}
    ${toolRuns.length ? `<div class="office-final-tools">${toolRuns.map(run => `<span><strong>${escapeHTML(run.name)}</strong>${escapeHTML(run.error || run.output || "完成")}</span>`).join("")}</div>` : ""}
  </section>`;
}
async function loadOfficeTrace(traceID, targetID) {
  const view = $(`#${targetID}`);
  view.textContent = "正在读取 Trace…";
  try {
    const value = await api(`/v1/traces/${encodeURIComponent(traceID)}`);
    view.textContent = JSON.stringify(value, null, 2);
  } catch (error) {
    view.textContent = `Trace 读取失败：${error.message}`;
  }
}
function officeStatusClass(status) {
  if (status === "completed" || status === "ready") return "ready";
  if (status === "failed" || status === "rejected" || status === "blocked" || status === "recovery_required") return "blocked";
  if (status === "running") return "running";
  return "pending";
}
function renderTopology() {
  const overview = state.overview;
  if (!overview) return;
  const roots = overview.nodes.filter(node => !overview.edges.some(edge => edge.child === node.id));
  const rows = roots.map(root => {
    const children = overview.edges.filter(edge => edge.parent === root.id).map(edge => overview.nodes.find(node => node.id === edge.child)).filter(Boolean);
    return `<div class="topology-row">
      ${nodeCard(root)}
      <div class="edge-arrow">${children.length ? "→" : "·"}</div>
      <div class="child-stack">${children.length ? children.map(nodeCard).join("") : '<span class="subdued">没有声明直接子节点</span>'}</div>
    </div>`;
  });
  const included = new Set([...roots, ...roots.flatMap(root => overview.edges.filter(edge => edge.parent === root.id).map(edge => overview.nodes.find(node => node.id === edge.child)))].filter(Boolean).map(node => node.id));
  const orphans = overview.nodes.filter(node => !included.has(node.id));
  $("#topologyCanvas").innerHTML = rows.join("") + (orphans.length ? `<div class="child-stack">${orphans.map(nodeCard).join("")}</div>` : "");
  $$("#topologyCanvas .node-card").forEach(card => card.addEventListener("click", () => selectNode(card.dataset.id)));
}
function renderNetworkRelationships() {
  const panel = $("#networkAuthPanel");
  if (!state.network) {
    panel.hidden = true;
    return;
  }
  panel.hidden = false;
  const identity = state.network.identity || {};
  $("#networkIdentity").textContent = identity.node_id ? `${identity.node_id}${identity.pairing_code ? ` · 本机配对码 ${identity.pairing_code}` : ""}` : "";
  const relationships = state.network.relationships || [];
  $("#networkRelationships").innerHTML = relationships.map(item => {
    var _item$capabilities, _item$capabilities2;
    const pending = item.status === "pending";
    const active = item.status === "active" || item.status === "offline";
    const tools = ((_item$capabilities = item.capabilities) === null || _item$capabilities === void 0 || (_item$capabilities = _item$capabilities.tools) === null || _item$capabilities === void 0 ? void 0 : _item$capabilities.length) || 0;
    const skills = ((_item$capabilities2 = item.capabilities) === null || _item$capabilities2 === void 0 || (_item$capabilities2 = _item$capabilities2.skills) === null || _item$capabilities2 === void 0 ? void 0 : _item$capabilities2.length) || 0;
    return `<article class="relationship-item">
      <div>
        <strong>${escapeHTML(item.peer_name)} <span class="relationship-status ${escapeHTML(item.status)}">${escapeHTML(item.status)}</span></strong>
        <p class="mono">${escapeHTML(item.peer_node_id)}</p>
        <small>${escapeHTML(item.role)} · ${tools} Tool · ${skills} Skill${pending ? ` · 配对码 ${escapeHTML(item.pairing_code)}` : ""}</small>
      </div>
      <div class="relationship-actions">
        ${pending ? `<button class="primary-button" data-network-action="approve" data-enrollment="${escapeHTML(item.enrollment_id)}">批准</button>` : ""}
        ${!pending && item.status !== "revoked" && item.role !== "controller" ? `<button class="secondary-button" data-network-action="permissions" data-enrollment="${escapeHTML(item.enrollment_id)}">权限</button>` : ""}
        ${active ? `<button class="secondary-button" data-network-action="suspend" data-enrollment="${escapeHTML(item.enrollment_id)}">暂停</button>` : ""}
        ${item.status === "suspended" ? `<button class="secondary-button" data-network-action="active" data-enrollment="${escapeHTML(item.enrollment_id)}">恢复</button>` : ""}
        ${item.status !== "revoked" ? `<button class="danger-button" data-network-action="revoked" data-enrollment="${escapeHTML(item.enrollment_id)}">吊销</button>` : ""}
      </div>
    </article>`;
  }).join("") || '<div class="inline-message">还没有加入申请或认证关系。</div>';
  $$("#networkRelationships [data-network-action]").forEach(button => button.addEventListener("click", () => {
    if (button.dataset.networkAction === "permissions" || button.dataset.networkAction === "approve") {
      openNetworkPermissions(button.dataset.enrollment);
    } else {
      updateNetworkRelationship(button.dataset.enrollment, button.dataset.networkAction);
    }
  }));
}
const networkScopeOptions = ["capabilities:read", "heartbeat:write", "reply:execute", "recursive:execute", "group:execute", "agentteams:execute", "agentteams:read", "session:read", "topology:read", "trace:read"];
function permissionCheckboxes(container, values, selected, required = []) {
  const selectedSet = new Set(selected || []);
  const requiredSet = new Set(required);
  $(container).innerHTML = values.map(value => `<label>
    <input type="checkbox" value="${escapeHTML(value)}" ${selectedSet.has(value) ? "checked" : ""} ${requiredSet.has(value) ? "disabled checked" : ""}>
    <span>${escapeHTML(value)}</span>
  </label>`).join("") || '<span class="subdued">无可授权项</span>';
}
function declaredCapabilityNames(capabilities, kind) {
  return [...new Set([...((capabilities === null || capabilities === void 0 ? void 0 : capabilities[kind === "tool" ? "tools" : "skills"]) || []).map(entry => entry.name), ...((capabilities === null || capabilities === void 0 ? void 0 : capabilities.availability) || []).filter(entry => entry.kind === kind).map(entry => entry.name)])].sort();
}
function openNetworkPermissions(enrollment) {
  var _state$network, _item$capabilities3, _item$capabilities4;
  const item = (_state$network = state.network) === null || _state$network === void 0 || (_state$network = _state$network.relationships) === null || _state$network === void 0 ? void 0 : _state$network.find(relationship => relationship.enrollment_id === enrollment);
  if (!item) {
    showNotice("认证关系不存在或已刷新", true);
    return;
  }
  const pending = item.status === "pending";
  const defaultScopes = ["capabilities:read", "heartbeat:write", "reply:execute"];
  const defaultTools = (((_item$capabilities3 = item.capabilities) === null || _item$capabilities3 === void 0 ? void 0 : _item$capabilities3.tools) || []).filter(entry => entry.risk !== "high" && !entry.requires_approval).map(entry => entry.name);
  const defaultSkills = (((_item$capabilities4 = item.capabilities) === null || _item$capabilities4 === void 0 ? void 0 : _item$capabilities4.skills) || []).filter(entry => entry.risk !== "high").map(entry => entry.name);
  $("#networkPermissionEnrollment").value = enrollment;
  $("#networkPermissionTitle").textContent = pending ? "核对配对码并批准权限" : "编辑关系权限";
  $("#networkPermissionPeer").textContent = pending ? `${item.peer_name} · ${item.peer_node_id} · 配对码 ${item.pairing_code}` : `${item.peer_name} · ${item.peer_node_id} · policy v${item.policy_version || 0}`;
  $("#networkPermissionGroups").value = (item.groups || []).join(", ");
  $("#networkPermissionAllowUpstream").checked = pending ? true : item.allow_upstream_execute !== false;
  permissionCheckboxes("#networkPermissionScopes", networkScopeOptions, pending ? defaultScopes : item.scopes, ["heartbeat:write"]);
  permissionCheckboxes("#networkPermissionTools", declaredCapabilityNames(item.capabilities, "tool"), pending ? defaultTools : item.allowed_tools);
  permissionCheckboxes("#networkPermissionSkills", declaredCapabilityNames(item.capabilities, "skill"), pending ? defaultSkills : item.allowed_skills);
  $("#saveNetworkPermissionButton").textContent = pending ? "确认并批准" : "保存权限";
  $("#networkPermissionDialog").showModal();
}
function checkedPermissionValues(container) {
  return $$(`${container} input:checked`).map(input => input.value);
}
async function saveNetworkPermissions(event) {
  var _state$network2;
  event.preventDefault();
  const enrollment = $("#networkPermissionEnrollment").value;
  const relationship = (_state$network2 = state.network) === null || _state$network2 === void 0 || (_state$network2 = _state$network2.relationships) === null || _state$network2 === void 0 ? void 0 : _state$network2.find(item => item.enrollment_id === enrollment);
  const pending = (relationship === null || relationship === void 0 ? void 0 : relationship.status) === "pending";
  const groups = $("#networkPermissionGroups").value.split(",").map(value => value.trim()).filter(Boolean);
  const permissions = {
    groups,
    allowed_tools: checkedPermissionValues("#networkPermissionTools"),
    allowed_skills: checkedPermissionValues("#networkPermissionSkills"),
    scopes: checkedPermissionValues("#networkPermissionScopes"),
    allow_upstream_execute: $("#networkPermissionAllowUpstream").checked
  };
  $("#saveNetworkPermissionButton").disabled = true;
  try {
    const endpoint = pending ? `/v1/admin/network/enrollments/${encodeURIComponent(enrollment)}/approve` : `/v1/admin/network/relationships/${encodeURIComponent(enrollment)}/permissions`;
    const options = pending ? jsonRequest(permissions) : {
      method: "PUT",
      headers: {
        "Content-Type": "application/json"
      },
      body: JSON.stringify(permissions)
    };
    await api(endpoint, options);
    $("#networkPermissionDialog").close();
    showNotice(pending ? "配对码已核对，节点加入申请已批准" : "关系权限已更新；后续每一跳会按新策略重新鉴权");
    await Promise.all([loadNetworkRelationships(), loadOverview()]);
  } catch (error) {
    showNotice(error.message, true);
  } finally {
    $("#saveNetworkPermissionButton").disabled = false;
  }
}
async function updateNetworkRelationship(enrollment, action) {
  try {
    await api(`/v1/admin/network/relationships/${encodeURIComponent(enrollment)}/status`, jsonRequest({
      status: action
    }));
    showNotice(`认证关系已更新为 ${action}`);
    await Promise.all([loadNetworkRelationships(), loadOverview()]);
  } catch (error) {
    showNotice(error.message, true);
  }
}
function renderDebugControls() {
  var _local$capabilities, _targetNode$capabilit, _local$capabilities2, _targetNode$capabilit2, _local$capabilities3;
  if (!state.overview) return;
  const selectedSkills = new Set($$("#debugSkills input:checked").map(input => input.value));
  const selectedPipeline = $("#debugPipeline").value;
  const local = state.overview.nodes.find(node => node.id === state.overview.local_node_id);
  const groups = (local === null || local === void 0 || (_local$capabilities = local.capabilities) === null || _local$capabilities === void 0 ? void 0 : _local$capabilities.groups) || [];
  const remotes = state.overview.nodes.filter(node => node.id !== state.overview.local_node_id && ["discovered", "configured", "network"].includes(node.source));
  const discovered = remotes.filter(node => node.source === "discovered" && node.status === "online" && node.can_reply !== false);
  const options = [["local", `本地 · ${(local === null || local === void 0 ? void 0 : local.name) || "ECS"}`, false], ["recursive", "递归网络 · 本节点及子 ECS", false], ...groups.map(group => [`group:${group}`, `节点组 · ${group}`, false]), ...(discovered.length ? [["group:all", "已发现网络 · 全部节点", false]] : []), ...remotes.map(node => [`node:${node.id}`, `指定节点 · ${node.name} · ${node.status}${node.can_reply === false ? " · 无 Reply 权限" : ""}`, node.can_reply === false])];
  if (!options.some(([value,, disabled]) => value === state.debugTarget && !disabled)) {
    state.debugTarget = "local";
  }
  $("#debugTarget").innerHTML = options.map(([value, label, disabled]) => `<option value="${escapeHTML(value)}" ${disabled ? "disabled" : ""}>${escapeHTML(label)}</option>`).join("");
  $("#debugTarget").value = state.debugTarget;
  const targetNode = debugTargetNode();
  const targetsRemoteNode = state.debugTarget.startsWith("node:");
  const pipelines = (targetNode === null || targetNode === void 0 || (_targetNode$capabilit = targetNode.capabilities) === null || _targetNode$capabilit === void 0 ? void 0 : _targetNode$capabilit.pipelines) || (targetsRemoteNode ? [] : (local === null || local === void 0 || (_local$capabilities2 = local.capabilities) === null || _local$capabilities2 === void 0 ? void 0 : _local$capabilities2.pipelines) || []);
  $("#debugPipeline").innerHTML = '<option value="">节点默认</option>' + pipelines.map(pipeline => `<option value="${escapeHTML(pipeline.name)}">${escapeHTML(pipeline.name)}${pipeline.default ? " · default" : ""}</option>`).join("");
  if ([...$("#debugPipeline").options].some(option => option.value === selectedPipeline)) {
    $("#debugPipeline").value = selectedPipeline;
  }
  const skills = (targetNode === null || targetNode === void 0 || (_targetNode$capabilit2 = targetNode.capabilities) === null || _targetNode$capabilit2 === void 0 ? void 0 : _targetNode$capabilit2.skills) || (targetsRemoteNode ? [] : (local === null || local === void 0 || (_local$capabilities3 = local.capabilities) === null || _local$capabilities3 === void 0 ? void 0 : _local$capabilities3.skills) || []);
  $("#debugSkills").innerHTML = skills.map(skill => `<label class="skill-option">
    <input type="checkbox" value="${escapeHTML(skill.name)}" ${selectedSkills.has(skill.name) ? "checked" : ""}><span>${escapeHTML(skill.name)}</span>
  </label>`).join("") || '<span class="subdued">目标未声明 Skill</span>';
  $("#depthField").hidden = state.debugTarget !== "recursive";
  if (state.debugTarget === "local") {
    $("#debugRouteHint").textContent = "请求直接进入本地 ECS Pipeline、LLM 与工具循环。";
  } else if (state.debugTarget === "recursive") {
    $("#debugRouteHint").textContent = "本节点执行后递归调用子 ECS，并由各层 LLM 汇总。";
  } else if (state.debugTarget.startsWith("group:")) {
    $("#debugRouteHint").textContent = "本地 ECS 并发请求目标节点组，随后执行二次汇总。";
  } else {
    $("#debugRouteHint").textContent = "请求经本地受控代理发送到已发现节点；不允许任意 URL。";
  }
}
function debugTargetNode() {
  if (!state.overview) return null;
  if (!state.debugTarget.startsWith("node:")) {
    return state.overview.nodes.find(node => node.id === state.overview.local_node_id);
  }
  return state.overview.nodes.find(node => node.id === state.debugTarget.slice(5));
}
async function sendDebugRequest(event) {
  event.preventDefault();
  const prompt = $("#debugPrompt").value.trim();
  if (!prompt) return;
  const started = performance.now();
  appendChat("user", prompt, debugTargetLabel());
  $("#debugPrompt").value = "";
  $("#sendDebugButton").disabled = true;
  $("#sendDebugButton").textContent = "执行中…";
  const skills = $$("#debugSkills input:checked").map(input => input.value);
  state.debugSessionID = $("#debugSession").value.trim();
  if (state.debugSessionID) sessionStorage.setItem("ecs-debug-session-id", state.debugSessionID);
  const request = {
    prompt,
    session_id: state.debugSessionID || undefined,
    pipeline: $("#debugPipeline").value || undefined,
    skills: skills.length ? skills : undefined
  };
  try {
    let response;
    if (state.debugTarget === "local") {
      response = await api("/v1/reply", jsonRequest(request));
    } else if (state.debugTarget === "recursive") {
      response = await api("/v1/recursive/reply", jsonRequest({
        reply: request,
        max_depth: Number($("#debugDepth").value || 0)
      }));
    } else if (state.debugTarget.startsWith("group:")) {
      const group = state.debugTarget.slice(6);
      response = await api(`/v1/groups/${encodeURIComponent(group)}/reply`, jsonRequest(request));
    } else {
      const id = state.debugTarget.slice(5);
      response = await api(`/v1/admin/debug/nodes/${encodeURIComponent(id)}/reply`, jsonRequest(request));
    }
    const duration = Math.round(performance.now() - started);
    appendChat("assistant", responseText(response), `${debugTargetLabel()} · ${duration}ms`);
    await inspectResponse(response, duration);
  } catch (error) {
    const duration = Math.round(performance.now() - started);
    appendChat("error", error.message, `${debugTargetLabel()} · 请求失败 · ${duration}ms`);
    if (error.payload) await inspectResponse(error.payload, duration);
    showNotice(error.message, true);
  } finally {
    $("#sendDebugButton").disabled = false;
    $("#sendDebugButton").textContent = "发送请求 ↗";
    $("#debugPrompt").focus();
  }
}
function jsonRequest(value) {
  return {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(value)
  };
}
function debugTargetLabel() {
  var _$$selectedOptions$;
  return ((_$$selectedOptions$ = $("#debugTarget").selectedOptions[0]) === null || _$$selectedOptions$ === void 0 ? void 0 : _$$selectedOptions$.textContent) || state.debugTarget;
}
function responseText(response) {
  var _response$message, _response$summary, _response$response, _response$reply;
  if ((_response$message = response.message) !== null && _response$message !== void 0 && _response$message.content) return response.message.content;
  if ((_response$summary = response.summary) !== null && _response$summary !== void 0 && _response$summary.content) return response.summary.content;
  if ((_response$response = response.response) !== null && _response$response !== void 0 && (_response$response = _response$response.message) !== null && _response$response !== void 0 && _response$response.content) return response.response.message.content;
  if ((_response$reply = response.reply) !== null && _response$reply !== void 0 && (_response$reply = _response$reply.message) !== null && _response$reply !== void 0 && _response$reply.content) return response.reply.message.content;
  if (Array.isArray(response.results)) {
    return response.results.map(result => {
      var _result$response;
      return ((_result$response = result.response) === null || _result$response === void 0 || (_result$response = _result$response.message) === null || _result$response === void 0 ? void 0 : _result$response.content) || `${result.peer}: ${result.error || "无返回"}`;
    }).join("\n\n");
  }
  return "请求完成；请在右侧查看原始响应。";
}
function appendChat(role, content, meta) {
  state.debugMessages.push({
    role,
    content,
    meta,
    time: new Date()
  });
  renderDebugMessages();
}
function renderDebugMessages() {
  const stream = $("#chatStream");
  if (!state.debugMessages.length) {
    stream.innerHTML = '<div class="empty-chat"><strong>发送一条真实请求</strong><span>可调试本地 LLM、指定 ECS、节点组或递归网络。</span></div>';
    return;
  }
  stream.innerHTML = state.debugMessages.map(message => `<div class="chat-message ${escapeHTML(message.role)}">
    <div class="chat-message-head"><span>${escapeHTML(message.role)}</span><span>${escapeHTML(message.meta)}</span></div>
    <p>${escapeHTML(message.content)}</p>
  </div>`).join("");
  stream.scrollTop = stream.scrollHeight;
}
async function loadDebugSession({
  silent = false
} = {}) {
  const sessionID = $("#debugSession").value.trim();
  state.debugSessionID = sessionID;
  if (sessionID) sessionStorage.setItem("ecs-debug-session-id", sessionID);else sessionStorage.removeItem("ecs-debug-session-id");
  if (!sessionID) {
    state.debugMessages = [];
    renderDebugMessages();
    return;
  }
  try {
    const conversation = await api(`/v1/sessions/${encodeURIComponent(sessionID)}`);
    if ($("#debugSession").value.trim() !== sessionID) return;
    state.debugMessages = (conversation.messages || []).map(message => ({
      role: message.role,
      content: message.content,
      meta: `Session · ${formatTime(conversation.updated_at)}`,
      time: new Date(conversation.updated_at)
    }));
    renderDebugMessages();
    if (!silent) showNotice(`已恢复 Session：${sessionID}`);
  } catch (error) {
    if ($("#debugSession").value.trim() !== sessionID) return;
    if (error.message === "session not found") {
      state.debugMessages = [];
      renderDebugMessages();
      if (!silent) showNotice(`Session 尚无历史：${sessionID}`);
      return;
    }
    if (!silent) showNotice(`Session 恢复失败：${error.message}`, true);
  }
}
async function inspectResponse(response, duration) {
  const traceID = findTraceID(response);
  const runs = collectRuns(response);
  $("#runSummary").innerHTML = `<strong>${escapeHTML(debugTargetLabel())}</strong>
    ${duration}ms · ${runs.skills.length} Skill · ${runs.tools.length} Tool${traceID ? `<br>Trace ${escapeHTML(traceID)}` : ""}`;
  $("#inspectorRaw").textContent = JSON.stringify(response, null, 2);
  $("#inspectorRuns").innerHTML = [...runs.skills.map(run => runCard("SKILL", run, run.status || (run.error ? "failed" : "success"))), ...runs.tools.map(run => runCard("TOOL", run, run.error ? "failed" : "success"))].join("") || '<span class="subdued">本轮没有工具执行记录。</span>';
  $("#inspectorTrace").textContent = traceID ? "正在读取 Trace…" : "响应中没有 Trace ID。";
  if (traceID && !state.debugTarget.startsWith("node:")) {
    try {
      const trace = await api(`/v1/traces/${encodeURIComponent(traceID)}`);
      $("#inspectorTrace").textContent = JSON.stringify(trace, null, 2);
    } catch (error) {
      $("#inspectorTrace").textContent = `Trace 读取失败：${error.message}`;
    }
  } else if (traceID) {
    $("#inspectorTrace").textContent = `远程 Trace ID: ${traceID}\n当前节点不代理读取远端 Trace；请在执行节点使用独立管理凭证查看。`;
  }
}
function runCard(kind, run, status) {
  var _run$arguments;
  const input = (_run$arguments = run.arguments) !== null && _run$arguments !== void 0 ? _run$arguments : run.input;
  const inputText = input == null ? "无参数" : typeof input === "string" ? input : JSON.stringify(input, null, 2);
  const output = run.error || run.output || "无输出";
  return `<div class="run-item ${status === "success" || status === "degraded" ? "success" : "failed"}">
    <div class="run-item-head"><span>${escapeHTML(kind)} · ${escapeHTML(run.name)}</span><span class="run-status">${escapeHTML(status)}</span></div>
    <small class="subdued">参数</small><pre>${escapeHTML(inputText)}</pre>
    <small class="subdued">${run.error ? "错误" : "输出"}</small><pre>${escapeHTML(output)}</pre>
  </div>`;
}
function collectRuns(response) {
  const skills = [];
  const tools = [];
  const visit = value => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value.skill_runs)) skills.push(...value.skill_runs);
    if (Array.isArray(value.tool_runs)) tools.push(...value.tool_runs);
    if (value.response) visit(value.response);
    if (value.reply) visit(value.reply);
    if (Array.isArray(value.results)) value.results.forEach(visit);
    if (Array.isArray(value.children)) value.children.forEach(visit);
  };
  visit(response);
  return {
    skills,
    tools
  };
}
function findTraceID(response) {
  var _response$response2, _response$reply2, _response$results;
  return response.trace_id || ((_response$response2 = response.response) === null || _response$response2 === void 0 ? void 0 : _response$response2.trace_id) || ((_response$reply2 = response.reply) === null || _response$reply2 === void 0 ? void 0 : _response$reply2.trace_id) || ((_response$results = response.results) === null || _response$results === void 0 || (_response$results = _response$results.find(result => {
    var _result$response2;
    return (_result$response2 = result.response) === null || _result$response2 === void 0 ? void 0 : _result$response2.trace_id;
  })) === null || _response$results === void 0 || (_response$results = _response$results.response) === null || _response$results === void 0 ? void 0 : _response$results.trace_id) || "";
}
function nodeCard(node) {
  var _node$capabilities2;
  const platform = (_node$capabilities2 = node.capabilities) === null || _node$capabilities2 === void 0 ? void 0 : _node$capabilities2.platform;
  const platformLabel = platform !== null && platform !== void 0 && platform.name ? ` · ${platform.name}/${platform.runtime_mode || "unknown"}` : "";
  return `<button class="node-card ${node.source === "local" ? "local" : ""}" data-id="${escapeHTML(node.id)}">
    <strong><i class="dot ${node.status === "online" ? "online" : "unknown"}"></i> ${escapeHTML(node.name)}</strong>
    <small>${escapeHTML(node.source)} · ${escapeHTML(node.access)}${escapeHTML(platformLabel)}</small>
  </button>`;
}
function selectNode(id) {
  state.selectedNode = id;
  $("#nodeSelect").value = id;
  renderOverview();
  switchView("overview");
}
async function loadLocalTools() {
  state.tools = await api("/v1/admin/tools");
  if (state.selectedTool) state.selectedTool = state.tools.find(item => item.manifest.name === state.selectedTool.manifest.name) || null;
  renderToolsForNode();
}
function renderToolsForNode() {
  var _state$overview4, _node$capabilities3;
  const node = currentNode();
  const local = (node === null || node === void 0 ? void 0 : node.id) === ((_state$overview4 = state.overview) === null || _state$overview4 === void 0 ? void 0 : _state$overview4.local_node_id);
  $("#newToolButton").disabled = !local;
  $("#remoteToolNotice").hidden = local;
  let tools = local ? state.tools : ((node === null || node === void 0 || (_node$capabilities3 = node.capabilities) === null || _node$capabilities3 === void 0 ? void 0 : _node$capabilities3.tools) || []).map(manifest => ({
    manifest,
    enabled: true,
    remote: true
  }));
  $("#toolList").innerHTML = tools.map(item => {
    var _item$skill_dependenc, _state$selectedTool;
    const manifest = item.manifest;
    const dependencies = (_item$skill_dependenc = item.skill_dependencies) !== null && _item$skill_dependenc !== void 0 && _item$skill_dependenc.length ? ` · ${item.skill_dependencies.length} Skills` : "";
    const availability = item.available === false ? `<span class="availability unavailable" title="${escapeHTML(item.unavailable_reason || "unavailable")}">UNAVAILABLE</span>` : "";
    return `<button class="tool-item ${((_state$selectedTool = state.selectedTool) === null || _state$selectedTool === void 0 ? void 0 : _state$selectedTool.manifest.name) === manifest.name ? "active" : ""}" data-tool="${escapeHTML(manifest.name)}">
      <span class="tool-item-head"><strong>${escapeHTML(manifest.name)}</strong><span>${availability}<span class="risk ${escapeHTML(manifest.risk || "low")}">${escapeHTML((manifest.risk || "low").toUpperCase())}</span></span></span>
      <p>${escapeHTML(manifest.description || "")}${escapeHTML(dependencies)}${item.available === false ? ` · ${escapeHTML(item.unavailable_reason || "unavailable")}` : ""}</p>
    </button>`;
  }).join("") || '<div class="inline-message">此节点没有声明工具。</div>';
  $$("#toolList .tool-item").forEach(button => button.addEventListener("click", () => {
    const item = tools.find(tool => tool.manifest.name === button.dataset.tool);
    editTool(item, !local);
  }));
  if (!local) {
    state.selectedTool = null;
    setEditor(null, true);
  }
}
function editTool(item, readOnly = false) {
  state.selectedTool = item;
  state.dirty = false;
  setEditor(item, readOnly);
  renderToolsForNode();
}
function setEditor(item, readOnly = false) {
  const editor = $("#toolEditor");
  $("#editorTitle").textContent = (item === null || item === void 0 ? void 0 : item.manifest.name) || "选择一个工具";
  editor.value = item ? JSON.stringify(item.manifest, null, 2) : "";
  editor.disabled = !item || readOnly;
  $("#formatToolButton").disabled = !item || readOnly;
  $("#saveToolButton").disabled = !item || readOnly;
  $("#deleteToolButton").disabled = !item || readOnly || item.remote;
  setDirty(false);
}
function newTool() {
  const manifest = {
    name: "new_tool",
    version: "1.0.0",
    description: "说明这个工具可以完成什么",
    command: "/absolute/path/to/tool",
    args: [],
    timeout: "10s",
    risk: "low",
    input_schema: {
      type: "object",
      properties: {},
      additionalProperties: false
    },
    output_schema: {
      type: "object",
      properties: {}
    }
  };
  editTool({
    manifest,
    enabled: true,
    isNew: true
  });
  $("#toolEditor").focus();
}
function setDirty(value) {
  state.dirty = value;
  $("#dirtyState").textContent = value ? "未保存" : "已同步";
  $("#dirtyState").classList.toggle("dirty", value);
}
function parsedEditor() {
  const manifest = JSON.parse($("#toolEditor").value);
  if (!manifest.name) throw new Error("工具 name 不能为空");
  return manifest;
}
async function saveTool() {
  try {
    var _state$selectedTool2, _state$selectedTool3;
    const manifest = parsedEditor();
    if (!((_state$selectedTool2 = state.selectedTool) !== null && _state$selectedTool2 !== void 0 && _state$selectedTool2.isNew) && manifest.name !== ((_state$selectedTool3 = state.selectedTool) === null || _state$selectedTool3 === void 0 ? void 0 : _state$selectedTool3.manifest.name)) {
      throw new Error("已有工具不能直接改名；请新建工具后卸载旧工具");
    }
    await api(`/v1/admin/tools/${encodeURIComponent(manifest.name)}`, {
      method: "PUT",
      headers: {
        "Content-Type": "application/json"
      },
      body: JSON.stringify(manifest)
    });
    state.selectedTool = {
      manifest,
      enabled: manifest.enabled !== false
    };
    await Promise.all([loadLocalTools(), loadOverview()]);
    showNotice(`工具 ${manifest.name} 已保存并热重载`);
  } catch (error) {
    showNotice(error.message, true);
  }
}
async function deleteTool() {
  if (!state.selectedTool) return;
  const name = state.selectedTool.manifest.name;
  if (!confirm(`确定卸载工具 ${name}？若 Skill 仍依赖它，ECS 会拒绝并自动回滚。`)) return;
  try {
    await api(`/v1/admin/tools/${encodeURIComponent(name)}`, {
      method: "DELETE"
    });
    state.selectedTool = null;
    setEditor(null);
    await Promise.all([loadLocalTools(), loadOverview()]);
    showNotice(`工具 ${name} 已卸载`);
  } catch (error) {
    showNotice(error.message, true);
  }
}
async function loadLogs() {
  const level = $("#logLevel").value;
  const logs = await api(`/v1/admin/logs?limit=300&level=${encodeURIComponent(level)}`);
  $("#logStream").innerHTML = logs.map(entry => `<div class="log-row">
    <span class="log-time">${escapeHTML(formatTime(entry.time))}</span>
    <span class="log-level ${escapeHTML(entry.level)}">${escapeHTML(entry.level)}</span>
    <span class="log-message">${escapeHTML(entry.message)}</span>
    <span class="log-attrs">${escapeHTML(JSON.stringify(entry.attributes || {}))}</span>
  </div>`).join("") || '<div class="inline-message">当前筛选条件下没有日志。</div>';
}
async function loadConfig() {
  state.config = await api("/v1/admin/config");
  $("#configView").textContent = JSON.stringify(state.config, null, 2);
}
function switchView(name) {
  $$(".nav-item").forEach(item => {
    const active = item.dataset.view === name;
    item.classList.toggle("active", active);
    if (active) item.setAttribute("aria-current", "page");else item.removeAttribute("aria-current");
  });
  $$(".view").forEach(view => view.classList.toggle("active", view.id === name));
  const labels = {
    overview: "运行概览",
    debug: "对话调试",
    topology: "节点网络",
    tools: "工具挂载",
    office: "办公流程",
    logs: "运行日志",
    config: "配置快照"
  };
  $("#pageTitle").textContent = labels[name];
  if (name === "logs") loadLogs().catch(handleError);
  if (name === "office") loadOffice().catch(handleError);
}
function handleError(error) {
  setConnection(false, error.message);
  showNotice(error.message, true);
  if (error.status === 401 && !$("#credentialDialog").open) {
    $("#tokenInput").value = state.token;
    $("#credentialDialog").showModal();
  }
}
function escapeHTML(value) {
  return String(value !== null && value !== void 0 ? value : "").replace(/[&<>"']/g, character => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#039;"
  })[character]);
}
async function refreshAll() {
  $("#refreshButton").disabled = true;
  try {
    await Promise.all([loadOverview(), loadNetworkRelationships(), loadLocalTools(), loadConfig()]);
    if ($("#office").classList.contains("active")) await loadOffice();
    if ($("#logs").classList.contains("active")) await loadLogs();
  } catch (error) {
    handleError(error);
  } finally {
    $("#refreshButton").disabled = false;
  }
}
$$(".nav-item").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
$("#nodeSelect").addEventListener("change", event => selectNode(event.target.value));
$("#debugTarget").addEventListener("change", event => {
  $$("#debugSkills input").forEach(input => input.checked = false);
  $("#debugPipeline").value = "";
  state.debugTarget = event.target.value;
  renderDebugControls();
});
$("#debugForm").addEventListener("submit", sendDebugRequest);
$("#debugPrompt").addEventListener("keydown", event => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    $("#debugForm").requestSubmit();
  }
});
function clearDebugView() {
  state.debugMessages = [];
  renderDebugMessages();
  $("#runSummary").innerHTML = '<span class="subdued">尚未执行请求</span>';
  $("#inspectorRuns").innerHTML = "";
  $("#inspectorRaw").textContent = "";
  $("#inspectorTrace").textContent = "";
}
$("#clearChatButton").addEventListener("click", clearDebugView);
$("#newSessionButton").addEventListener("click", () => {
  $("#debugSession").value = createDebugSessionID();
  state.debugSessionID = $("#debugSession").value;
  sessionStorage.setItem("ecs-debug-session-id", state.debugSessionID);
  clearDebugView();
  showNotice(`已新建 Session：${$("#debugSession").value}`);
  $("#debugPrompt").focus();
});
$("#debugSession").addEventListener("change", () => loadDebugSession());
$("#debugSession").addEventListener("input", () => {
  clearTimeout(loadDebugSession.timer);
  loadDebugSession.timer = setTimeout(() => loadDebugSession({
    silent: true
  }), 350);
});
$("#restoreSessionButton").addEventListener("click", () => loadDebugSession());
$$(".inspector-tabs button").forEach(button => button.addEventListener("click", () => {
  $$(".inspector-tabs button").forEach(item => {
    const active = item === button;
    item.classList.toggle("active", active);
    item.setAttribute("aria-selected", String(active));
  });
  $$(".inspector-content").forEach(content => content.classList.remove("active"));
  $(`#inspector${button.dataset.inspector[0].toUpperCase()}${button.dataset.inspector.slice(1)}`).classList.add("active");
}));
$("#refreshButton").addEventListener("click", refreshAll);
$("#clearOfficeFilter").addEventListener("click", () => {
  state.office.component = "";
  state.office.selectedOrder = "";
  state.office.detail = null;
  loadOffice().catch(handleError);
});
$("#officeStatusFilter").addEventListener("change", () => {
  state.office.selectedOrder = "";
  state.office.detail = null;
  loadOffice().catch(handleError);
});
$("#officeDispatchForm").addEventListener("submit", submitOfficeWorkOrder);
$("#networkPermissionForm").addEventListener("submit", saveNetworkPermissions);
$("#cancelNetworkPermissionButton").addEventListener("click", () => $("#networkPermissionDialog").close());
$("#newToolButton").addEventListener("click", newTool);
$("#toolEditor").addEventListener("input", () => setDirty(true));
$("#formatToolButton").addEventListener("click", () => {
  try {
    $("#toolEditor").value = JSON.stringify(parsedEditor(), null, 2);
    setDirty(true);
  } catch (error) {
    showNotice(error.message, true);
  }
});
$("#saveToolButton").addEventListener("click", saveTool);
$("#deleteToolButton").addEventListener("click", deleteTool);
$("#logLevel").addEventListener("change", () => loadLogs().catch(handleError));
$("#credentialButton").addEventListener("click", () => {
  $("#tokenInput").value = state.token;
  $("#credentialDialog").showModal();
});
$("#saveTokenButton").addEventListener("click", event => {
  event.preventDefault();
  state.token = $("#tokenInput").value.trim();
  if (state.token) sessionStorage.setItem("ecs-admin-token", state.token);else sessionStorage.removeItem("ecs-admin-token");
  $("#credentialDialog").close();
  refreshAll();
});
$("#debugSession").value = state.debugSessionID || createDebugSessionID();
state.debugSessionID = $("#debugSession").value;
sessionStorage.setItem("ecs-debug-session-id", state.debugSessionID);
setInterval(() => {
  loadOverview().catch(() => {});
  loadNetworkRelationships().catch(() => {});
  if ($("#office").classList.contains("active")) loadOffice().catch(() => {});
  if ($("#autoRefreshLogs").checked && $("#logs").classList.contains("active")) loadLogs().catch(() => {});
}, 5000);
refreshAll().then(() => loadDebugSession({
  silent: true
}));
