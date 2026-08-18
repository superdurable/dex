/*
Copyright (c) 2022-2026 Super Durable, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

const STORAGE_BACKEND = "dex-playground-backend";
const STORAGE_DEX_WEB = "dex-playground-dex-web";

const bootConfig = window.PLAYGROUND_CONFIG || {};
const catalog = window.PLAYGROUND_CATALOG || [];

const state = {
  backend: readStored(STORAGE_BACKEND, bootConfig.backend || "http://127.0.0.1:8080"),
  dexWeb: readStored(STORAGE_DEX_WEB, bootConfig.dexWeb || "http://127.0.0.1:8802"),
  flowIds: {},
  runIds: {},
};

function readStored(key, fallback) {
  try {
    return window.localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
}

function writeStored(key, value) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* ignore quota / private mode */
  }
}

function newFlowId(prefix) {
  return prefix + "-" + Date.now() + "-" + Math.random().toString(16).slice(2, 8);
}

function exampleFlowId(example) {
  if (!state.flowIds[example.id]) {
    state.flowIds[example.id] = newFlowId(example.flowIdPrefix);
  }
  return state.flowIds[example.id];
}

function setExampleFlowId(exampleId, flowId) {
  if (!flowId) {
    return;
  }
  state.flowIds[exampleId] = flowId;
  document.querySelectorAll(`[data-flow-id-for="${exampleId}"]`).forEach((input) => {
    if (input.value !== flowId) {
      input.value = flowId;
    }
  });
  renderExampleLinks(exampleId);
}

function setExampleRunId(exampleId, runId) {
  if (!runId) {
    return;
  }
  state.runIds[exampleId] = runId;
  renderExampleLinks(exampleId);
}

function trimSlash(url) {
  return String(url || "").replace(/\/+$/, "");
}

function dexWebFlowURL(flowId, runId) {
  const base = trimSlash(state.dexWeb);
  if (!flowId || !base) {
    return "";
  }
  if (runId) {
    return `${base}/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(runId)}`;
  }
  return `${base}/flows/${encodeURIComponent(flowId)}`;
}

function dexWebSearchURL(flowId) {
  const base = trimSlash(state.dexWeb);
  if (!flowId || !base) {
    return "";
  }
  return `${base}/?q=${encodeURIComponent(`WorkflowId="${flowId}"`)}`;
}

function fieldValue(form, field, example) {
  if (field.role === "flowId") {
    const input = form.querySelector(`[name="${field.name}"]`);
    return (input && input.value.trim()) || exampleFlowId(example);
  }
  const input = form.querySelector(`[name="${field.name}"]`);
  if (!input) {
    return field.default || "";
  }
  return input.value;
}

function buildRequest(example, endpoint, form) {
  const query = new URLSearchParams();
  const bodyObject = {};
  let rawJson = null;
  let injectFlowId = null;
  (endpoint.fields || []).forEach((field) => {
    const value = fieldValue(form, field, example);
    if (field.in === "raw-json") {
      rawJson = value;
      injectFlowId = field.injectFlowId;
      return;
    }
    if (field.in === "body") {
      if (field.type === "number") {
        bodyObject[field.name] = Number(value);
      } else if (field.type === "boolean") {
        bodyObject[field.name] = value === "true";
      } else {
        bodyObject[field.name] = value;
      }
      return;
    }
    if (value !== "") {
      query.set(field.name, value);
    }
  });
  const path = endpoint.path;
  const queryText = query.toString();
  const url = trimSlash(state.backend) + path + (queryText ? `?${queryText}` : "");
  const init = { method: endpoint.method, headers: {} };
  if (rawJson !== null) {
    let parsed = JSON.parse(rawJson);
    if (injectFlowId) {
      parsed[injectFlowId] = exampleFlowId(example);
    }
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(parsed);
  } else if (endpoint.method !== "GET" && Object.keys(bodyObject).length > 0) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(bodyObject);
  }
  return { url, init };
}

function extractIds(payload, text, endpoint) {
  let flowId = null;
  let runId = null;
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    flowId = payload.flowID || payload.flowId || payload.workflowId || payload.workflowID || payload.userId || null;
    runId = payload.runID || payload.runId || null;
  }
  const source = typeof payload === "string" ? payload : text;
  if (!flowId && source) {
    const match = source.match(
      /(?:started workflowId|Started workflowId|workflowId|flowID|flowId)\s*[:=]\s*([^\s,}"']+)/i,
    );
    if (match) {
      flowId = match[1];
    }
  }
  if (!runId && source) {
    const match = source.match(/(?:runId|runID)\s*[:=]?\s*([^\s,}"']+)/i);
    if (match) {
      runId = match[1];
    }
  }
  const isStartPath = endpoint && /\/start/i.test(endpoint.path);
  if (!runId && isStartPath && typeof payload === "string") {
    const bare = payload.replace(/^"|"$/g, "").trim();
    if (bare && !/\s/.test(bare) && bare.length < 80 && bare[0] !== "{" && bare[0] !== "[") {
      runId = bare;
    }
  }
  return { flowId, runId };
}

function renderField(example, field) {
  const defaultValue = field.role === "flowId" ? exampleFlowId(example) : (field.default ?? "");
  const flowAttr = field.role === "flowId" ? ` data-flow-id-for="${example.id}"` : "";
  if (field.in === "raw-json") {
    return `<label class="field field-wide"><span>${field.name}</span><textarea name="${field.name}" rows="10">${escapeHtml(defaultValue)}</textarea></label>`;
  }
  if (field.type === "select") {
    const options = (field.options || [])
      .map((option) => `<option value="${escapeAttr(option)}"${option === defaultValue ? " selected" : ""}>${escapeHtml(option)}</option>`)
      .join("");
    return `<label class="field"><span>${field.name}</span><select name="${field.name}">${options}</select></label>`;
  }
  if (field.type === "boolean") {
    return `<label class="field"><span>${field.name}</span><select name="${field.name}">
      <option value="true"${defaultValue === "true" ? " selected" : ""}>true</option>
      <option value="false"${defaultValue === "false" ? " selected" : ""}>false</option>
    </select></label>`;
  }
  const inputType = field.type === "number" ? "number" : "text";
  return `<label class="field"><span>${field.name}</span><input${flowAttr} type="${inputType}" name="${field.name}" value="${escapeAttr(defaultValue)}" autocomplete="off" /></label>`;
}

function renderEndpoint(example, endpoint, index) {
  const hasFlowIdField = (endpoint.fields || []).some((field) => field.role === "flowId");
  const flowField = hasFlowIdField
    ? ""
    : `<label class="field"><span>workflowId</span><input data-flow-id-for="${example.id}" type="text" name="workflowId" value="${escapeAttr(exampleFlowId(example))}" autocomplete="off" /></label>`;
  const fields = (endpoint.fields || []).map((field) => renderField(example, field)).join("");
  return `<article class="endpoint" data-example="${example.id}" data-endpoint="${index}">
    <header>
      <span class="method method-${endpoint.method.toLowerCase()}">${endpoint.method}</span>
      <code>${escapeHtml(endpoint.path)}</code>
      <strong>${escapeHtml(endpoint.title)}</strong>
    </header>
    <form>
      ${flowField}
      ${fields}
      <button type="submit">Call API</button>
    </form>
    <div class="result" hidden>
      <p class="meta"></p>
      <pre class="response"></pre>
      <p class="ids"></p>
      <p class="links"></p>
    </div>
  </article>`;
}

function renderExample(example) {
  const flowId = exampleFlowId(example);
  return `<section class="example" id="${example.group}-${example.id}">
    <div class="example-head">
      <h3>${escapeHtml(example.title)}</h3>
      <label class="field">
        <span>flowID / ${escapeHtml(example.idParam || "workflowId")}</span>
        <input data-flow-id-for="${example.id}" type="text" value="${escapeAttr(flowId)}" autocomplete="off" />
        <button type="button" class="secondary" data-new-id="${example.id}">New ID</button>
      </label>
      <p class="web-links" data-links-for="${example.id}"></p>
    </div>
    ${example.note ? `<p class="note">${escapeHtml(example.note)}</p>` : ""}
    ${example.endpoints.map((endpoint, index) => renderEndpoint(example, endpoint, index)).join("")}
  </section>`;
}

function render() {
  const groups = [
    { id: "products", title: "Products" },
    { id: "patterns", title: "Patterns" },
    { id: "primitives", title: "Primitives" },
  ];
  const nav = document.getElementById("nav");
  const main = document.getElementById("main");
  nav.innerHTML = groups
    .map((group) => {
      const items = catalog
        .filter((example) => example.group === group.id)
        .map((example) => `<a href="#${group.id}-${example.id}">${escapeHtml(example.title)}</a>`)
        .join("");
      return `<div class="nav-group"><h2>${group.title}</h2>${items}</div>`;
    })
    .join("");
  main.innerHTML = groups
    .map((group) => {
      const sections = catalog
        .filter((example) => example.group === group.id)
        .map(renderExample)
        .join("");
      return `<section class="group"><h2 id="${group.id}">${group.title}</h2>${sections}</section>`;
    })
    .join("");
  document.getElementById("backend-url").value = state.backend;
  document.getElementById("dex-web-url").value = state.dexWeb;
  catalog.forEach((example) => renderExampleLinks(example.id));
}

function renderExampleLinks(exampleId) {
  const container = document.querySelector(`[data-links-for="${exampleId}"]`);
  if (!container) {
    return;
  }
  const flowId = state.flowIds[exampleId];
  const runId = state.runIds[exampleId];
  if (!flowId) {
    container.innerHTML = "";
    return;
  }
  const flowURL = dexWebFlowURL(flowId, runId);
  const currentURL = dexWebFlowURL(flowId);
  const searchURL = dexWebSearchURL(flowId);
  container.innerHTML = `Dex Web:
    <a href="${escapeAttr(currentURL)}" target="_blank" rel="noreferrer">open ${escapeHtml(flowId)}</a>
    ${runId ? ` · <a href="${escapeAttr(flowURL)}" target="_blank" rel="noreferrer">run ${escapeHtml(runId)}</a>` : ""}
    · <a href="${escapeAttr(searchURL)}" target="_blank" rel="noreferrer">search</a>`;
}

async function onSubmit(event) {
  const form = event.target;
  if (!(form instanceof HTMLFormElement)) {
    return;
  }
  event.preventDefault();
  const article = form.closest(".endpoint");
  const example = catalog.find((item) => item.id === article.dataset.example);
  const endpoint = example.endpoints[Number(article.dataset.endpoint)];
  const flowInput = form.querySelector("[data-flow-id-for]");
  if (flowInput && flowInput.value.trim()) {
    setExampleFlowId(example.id, flowInput.value.trim());
  }
  const result = article.querySelector(".result");
  const meta = article.querySelector(".meta");
  const responsePre = article.querySelector(".response");
  const ids = article.querySelector(".ids");
  const links = article.querySelector(".links");
  result.hidden = false;
  meta.textContent = "Calling…";
  responsePre.textContent = "";
  ids.textContent = "";
  links.innerHTML = "";
  let request;
  try {
    request = buildRequest(example, endpoint, form);
  } catch (error) {
    meta.textContent = "Invalid request";
    responsePre.textContent = String(error);
    return;
  }
  meta.textContent = `${request.init.method} ${request.url}`;
  try {
    const response = await fetch(request.url, request.init);
    const text = await response.text();
    let payload = text;
    try {
      payload = JSON.parse(text);
    } catch {
      payload = text;
    }
    responsePre.textContent = typeof payload === "string" ? payload : JSON.stringify(payload, null, 2);
    meta.textContent = `${request.init.method} ${request.url} → ${response.status} ${response.statusText}`;
    const extracted = extractIds(payload, text, endpoint);
    if (extracted.flowId) {
      setExampleFlowId(example.id, extracted.flowId);
    }
    if (extracted.runId) {
      setExampleRunId(example.id, extracted.runId);
    }
    const flowId = extracted.flowId || exampleFlowId(example);
    const runId = extracted.runId || state.runIds[example.id] || "";
    ids.textContent = `flowID: ${flowId}${runId ? ` · runID: ${runId}` : ""}`;
    const flowURL = dexWebFlowURL(flowId, runId);
    const currentURL = dexWebFlowURL(flowId);
    const searchURL = dexWebSearchURL(flowId);
    links.innerHTML = `Dex Web:
      <a href="${escapeAttr(currentURL)}" target="_blank" rel="noreferrer">open flow</a>
      ${runId ? ` · <a href="${escapeAttr(flowURL)}" target="_blank" rel="noreferrer">open run</a>` : ""}
      · <a href="${escapeAttr(searchURL)}" target="_blank" rel="noreferrer">search</a>`;
  } catch (error) {
    meta.textContent = `${request.init.method} ${request.url} failed`;
    responsePre.textContent = String(error);
  }
}

function onClick(event) {
  const newIdButton = event.target.closest("[data-new-id]");
  if (newIdButton) {
    const example = catalog.find((item) => item.id === newIdButton.dataset.newId);
    setExampleFlowId(example.id, newFlowId(example.flowIdPrefix));
    delete state.runIds[example.id];
    renderExampleLinks(example.id);
  }
}

function onInput(event) {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || !input.dataset.flowIdFor) {
    return;
  }
  setExampleFlowId(input.dataset.flowIdFor, input.value.trim());
}

function saveSettings(event) {
  event.preventDefault();
  state.backend = document.getElementById("backend-url").value.trim();
  state.dexWeb = document.getElementById("dex-web-url").value.trim();
  writeStored(STORAGE_BACKEND, state.backend);
  writeStored(STORAGE_DEX_WEB, state.dexWeb);
  catalog.forEach((example) => renderExampleLinks(example.id));
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function escapeAttr(value) {
  return escapeHtml(value);
}

document.addEventListener("DOMContentLoaded", () => {
  render();
  document.getElementById("main").addEventListener("submit", onSubmit);
  document.getElementById("main").addEventListener("click", onClick);
  document.getElementById("main").addEventListener("input", onInput);
  document.getElementById("settings").addEventListener("submit", saveSettings);
});
