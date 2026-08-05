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

const state = {
  role: 'seller',
  process: null,
  actions: [],
  executions: [],
  selectedFlowID: '',
  messageData: {},
};

const elements = {
  sellerWorkspace: document.querySelector('#seller-workspace'),
  buyerWorkspace: document.querySelector('#buyer-workspace'),
  buyerTitle: document.querySelector('#buyer-title'),
  startPanel: document.querySelector('#start-execution').closest('.action-panel'),
  processID: document.querySelector('#process-id'),
  initialState: document.querySelector('#initial-state'),
  initialData: document.querySelector('#initial-data'),
  states: document.querySelector('#states'),
  startProcessID: document.querySelector('#start-process-id'),
  messageFlowID: document.querySelector('#message-flow-id'),
  messageCondition: document.querySelector('#message-condition'),
  messageData: document.querySelector('#message-data'),
  executions: document.querySelector('#executions'),
  executionTitle: document.querySelector('#execution-title'),
  toast: document.querySelector('#toast'),
  stateDataKeys: document.querySelector('#state-data-keys'),
};

async function initialize() {
  const [process, actionResponse] = await Promise.all([
    api('/dataset-deal/comprehensive-process.json'),
    api('/api/dataset-deal/actions'),
  ]);
  state.process = normalizeProcess(process);
  state.actions = actionResponse.actions;
  bindStaticControls();
  renderProcess();
  selectRole('seller');
  await refreshExecutions();
}

function normalizeProcess(process) {
  process.initialStateData ??= {};
  process.states ??= [];
  process.states.forEach((dealState) => {
    dealState.preActions ??= [];
    dealState.postActions ??= [];
    if (dealState.postCondition) {
      dealState.postCondition.decision ??= {
        key: '',
        cases: [],
        elseState: process.initialState,
      };
      dealState.postCondition.decision.cases ??= [];
    }
  });
  return process;
}

function bindStaticControls() {
  document.querySelectorAll('.role').forEach((button) => {
    button.addEventListener('click', () => selectRole(button.dataset.role));
  });
  document.querySelector('#create-process').addEventListener('click', createProcess);
  document.querySelector('#add-state').addEventListener('click', addState);
  document.querySelector('#add-initial-data').addEventListener('click', () => {
    addUniqueKey(state.process.initialStateData, 'newKey', '');
    renderProcess();
  });
  document.querySelector('#start-execution').addEventListener('click', startExecution);
  document.querySelector('#add-message-data').addEventListener('click', () => {
    addUniqueKey(state.messageData, 'newKey', '');
    renderMessageData();
  });
  document.querySelector('#send-message').addEventListener('click', sendMessage);
  document.querySelector('#refresh-executions').addEventListener('click', refreshExecutions);
  elements.processID.addEventListener('input', () => {
    state.process.processID = elements.processID.value;
    elements.startProcessID.value = elements.processID.value;
  });
  elements.initialState.addEventListener('change', () => {
    state.process.initialState = elements.initialState.value;
  });
  elements.messageFlowID.addEventListener('change', () => {
    state.selectedFlowID = elements.messageFlowID.value;
    renderConditionOptions();
  });
  elements.messageCondition.addEventListener('change', () => {
    state.messageData = messageDefaults(elements.messageCondition.value);
    renderMessageData();
  });
}

function selectRole(role) {
  state.role = role;
  document.querySelectorAll('.role').forEach((button) => {
    button.classList.toggle('active', button.dataset.role === role);
  });
  const seller = role === 'seller';
  elements.sellerWorkspace.classList.toggle('hidden', !seller);
  elements.buyerWorkspace.classList.remove('hidden');
  elements.startPanel.classList.toggle('hidden', seller);
  elements.buyerTitle.textContent = seller ? 'Respond across buyer deals' : `Run a deal as ${buyerLabel(role)}`;
  elements.executionTitle.textContent = seller ? 'All buyer executions' : `${buyerLabel(role)} executions`;
  refreshExecutions();
}

function renderProcess() {
  elements.processID.value = state.process.processID;
  elements.startProcessID.value = state.process.processID;
  renderInitialStateOptions();
  renderKeyValues(elements.initialData, state.process.initialStateData, () => renderProcess());
  renderStateDataKeys();
  renderStates();
  renderConditionOptions();
}

function renderInitialStateOptions() {
  elements.initialState.innerHTML = stateOptions(state.process.initialState);
}

function renderStates() {
  elements.states.replaceChildren();
  state.process.states.forEach((dealState, stateIndex) => {
    const card = document.createElement('article');
    card.className = 'state-card';
    card.innerHTML = stateCardHTML(dealState, stateIndex);
    bindStateCard(card, dealState, stateIndex);
    elements.states.append(card);
  });
}

function stateCardHTML(dealState, stateIndex) {
  const hasPostCondition = Boolean(dealState.postCondition);
  const decision = dealState.postCondition?.decision ?? { key: '', cases: [], elseState: state.process.initialState };
  return `
    <div class="state-header">
      <span class="state-index">${stateIndex + 1}</span>
      <input data-field="name" value="${escapeHTML(dealState.name)}" aria-label="State name" />
      <button class="icon-button" data-command="remove-state" aria-label="Remove state">×</button>
    </div>
    <div class="state-body">
      <section class="state-section">
        <h4>Pre-condition · optional external wait</h4>
        <input data-field="pre-condition" value="${escapeHTML(dealState.preCondition?.name ?? '')}" placeholder="Channel instance name, or leave empty" />
      </section>
      ${actionSectionHTML('Pre-actions', 'pre', dealState.preActions)}
      ${actionSectionHTML('Post-actions', 'post', dealState.postActions)}
      <section class="state-section">
        <label class="condition-toggle">
          <input type="checkbox" data-field="has-post-condition" ${hasPostCondition ? 'checked' : ''} />
          Continue to another state
        </label>
        <div class="post-condition ${hasPostCondition ? '' : 'hidden'}">
          <label>Wait channel · optional<input data-field="post-wait" value="${escapeHTML(dealState.postCondition?.waitFor?.name ?? '')}" placeholder="Immediate when empty" /></label>
          <div class="decision-grid">
            <label>Compare stateData key<select data-field="decision-key">${keyOptions(decision.key)}</select></label>
            <label>Else go to<select data-field="else-state">${stateOptions(decision.elseState)}</select></label>
          </div>
          <div class="field-label" style="margin-top:14px">Equal cases</div>
          <div class="case-list">${decision.cases.map((entry, index) => caseRowHTML(entry, index)).join('')}</div>
          <button class="ghost small" data-command="add-case">+ equality case</button>
        </div>
      </section>
    </div>`;
}

function actionSectionHTML(title, phase, actionNames) {
  return `<section class="state-section">
    <h4>${title} · ordered</h4>
    <div class="action-list" data-phase="${phase}">
      ${actionNames.map((name, index) => actionRowHTML(name, index)).join('')}
    </div>
    <button class="ghost small" data-command="add-action" data-phase="${phase}">+ action</button>
  </section>`;
}

function actionRowHTML(name, index) {
  return `<div class="action-row" data-index="${index}">
    <select data-field="action-name">${actionOptions(name)}</select>
    <button class="icon-button" data-command="move-action-up" aria-label="Move up">↑</button>
    <button class="icon-button" data-command="move-action-down" aria-label="Move down">↓</button>
    <button class="icon-button" data-command="remove-action" aria-label="Remove action">×</button>
  </div>`;
}

function caseRowHTML(entry, index) {
  return `<div class="case-row" data-index="${index}">
    <input data-field="case-equals" value="${escapeHTML(entry.equals)}" placeholder="equals value" />
    <select data-field="case-target">${stateOptions(entry.goToState)}</select>
    <button class="icon-button" data-command="remove-case" aria-label="Remove case">×</button>
  </div>`;
}

function bindStateCard(card, dealState, stateIndex) {
  card.querySelector('[data-field="name"]').addEventListener('change', (event) => {
    renameState(dealState.name, event.target.value.trim());
  });
  card.querySelector('[data-command="remove-state"]').addEventListener('click', () => {
    if (state.process.states.length === 1) return showToast('A process needs at least one state.', true);
    state.process.states.splice(stateIndex, 1);
    if (state.process.initialState === dealState.name) state.process.initialState = state.process.states[0].name;
    renderProcess();
  });
  card.querySelector('[data-field="pre-condition"]').addEventListener('input', (event) => {
    const name = event.target.value.trim();
    dealState.preCondition = name ? { name } : undefined;
  });
  bindActionRows(card, dealState, 'pre');
  bindActionRows(card, dealState, 'post');
  bindPostCondition(card, dealState);
}

function bindActionRows(card, dealState, phase) {
  const property = phase === 'pre' ? 'preActions' : 'postActions';
  card.querySelector(`[data-command="add-action"][data-phase="${phase}"]`).addEventListener('click', () => {
    dealState[property].push(state.actions[0]);
    renderStates();
  });
  card.querySelectorAll(`.action-list[data-phase="${phase}"] .action-row`).forEach((row) => {
    const index = Number(row.dataset.index);
    row.querySelector('[data-field="action-name"]').addEventListener('change', (event) => {
      dealState[property][index] = event.target.value;
    });
    row.querySelector('[data-command="remove-action"]').addEventListener('click', () => {
      dealState[property].splice(index, 1);
      renderStates();
    });
    row.querySelector('[data-command="move-action-up"]').addEventListener('click', () => moveItem(dealState[property], index, index - 1));
    row.querySelector('[data-command="move-action-down"]').addEventListener('click', () => moveItem(dealState[property], index, index + 1));
  });
}

function bindPostCondition(card, dealState) {
  card.querySelector('[data-field="has-post-condition"]').addEventListener('change', (event) => {
    dealState.postCondition = event.target.checked ? {
      decision: { key: '', cases: [], elseState: state.process.initialState },
    } : undefined;
    renderStates();
  });
  if (!dealState.postCondition) return;
  card.querySelector('[data-field="post-wait"]').addEventListener('input', (event) => {
    const name = event.target.value.trim();
    dealState.postCondition.waitFor = name ? { name } : undefined;
  });
  card.querySelector('[data-field="decision-key"]').addEventListener('change', (event) => {
    dealState.postCondition.decision.key = event.target.value;
  });
  card.querySelector('[data-field="else-state"]').addEventListener('change', (event) => {
    dealState.postCondition.decision.elseState = event.target.value;
  });
  card.querySelector('[data-command="add-case"]').addEventListener('click', () => {
    dealState.postCondition.decision.cases.push({ equals: '', goToState: state.process.initialState });
    renderStates();
  });
  card.querySelectorAll('.case-row').forEach((row) => {
    const index = Number(row.dataset.index);
    row.querySelector('[data-field="case-equals"]').addEventListener('input', (event) => {
      dealState.postCondition.decision.cases[index].equals = event.target.value;
    });
    row.querySelector('[data-field="case-target"]').addEventListener('change', (event) => {
      dealState.postCondition.decision.cases[index].goToState = event.target.value;
    });
    row.querySelector('[data-command="remove-case"]').addEventListener('click', () => {
      dealState.postCondition.decision.cases.splice(index, 1);
      renderStates();
    });
  });
}

function addState() {
  const names = new Set(state.process.states.map((entry) => entry.name));
  let number = state.process.states.length + 1;
  while (names.has(`state-${number}`)) number += 1;
  state.process.states.push({ name: `state-${number}`, preActions: [], postActions: [] });
  renderProcess();
}

function renameState(previous, next) {
  if (!next || state.process.states.some((entry) => entry.name === next && entry.name !== previous)) {
    showToast('State names must be non-empty and unique.', true);
    return renderProcess();
  }
  state.process.states.forEach((entry) => {
    if (entry.name === previous) entry.name = next;
    if (entry.postCondition?.decision.elseState === previous) entry.postCondition.decision.elseState = next;
    entry.postCondition?.decision.cases.forEach((conditionCase) => {
      if (conditionCase.goToState === previous) conditionCase.goToState = next;
    });
  });
  if (state.process.initialState === previous) state.process.initialState = next;
  renderProcess();
}

function moveItem(items, from, to) {
  if (to < 0 || to >= items.length) return;
  const [item] = items.splice(from, 1);
  items.splice(to, 0, item);
  renderStates();
}

async function createProcess() {
  try {
    await api('/api/dataset-deal/processes', {
      method: 'POST',
      body: JSON.stringify(state.process),
    });
    showToast(`Process ${state.process.processID} created.`);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function startExecution() {
  try {
    const response = await api('/api/dataset-deal/executions', {
      method: 'POST',
      body: JSON.stringify({
        processID: elements.startProcessID.value.trim(),
        buyerID: state.role,
      }),
    });
    state.selectedFlowID = response.flowID;
    showToast(`Started ${response.flowID}.`);
    await waitForExecutionVisible(response.flowID);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function waitForExecutionVisible(flowID) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await refreshExecutions();
    if (state.executions.some((execution) => execution.flowID === flowID)) return;
    await new Promise((resolve) => window.setTimeout(resolve, 250));
  }
  throw new Error(`Execution ${flowID} is not visible yet. Refresh to try again.`);
}

async function sendMessage() {
  const flowID = elements.messageFlowID.value;
  const conditionName = elements.messageCondition.value;
  if (!flowID || !conditionName) return showToast('Select an execution and channel instance.', true);
  try {
    await api(`/api/dataset-deal/executions/${encodeURIComponent(flowID)}/channels/${encodeURIComponent(conditionName)}`, {
      method: 'POST',
      body: JSON.stringify({ data: state.messageData }),
    });
    showToast(`Sent ${conditionName}.`);
    window.setTimeout(refreshExecutions, 500);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function refreshExecutions() {
  try {
    const query = state.role === 'seller' ? '' : `?buyerID=${encodeURIComponent(state.role)}`;
    const response = await api(`/api/dataset-deal/executions${query}`);
    state.executions = response.executions;
    renderExecutions();
    renderExecutionOptions();
  } catch (error) {
    showToast(error.message, true);
  }
}

function renderExecutions() {
  if (state.executions.length === 0) {
    elements.executions.innerHTML = '<tr><td colspan="7" class="empty-row">No deal executions yet.</td></tr>';
    return;
  }
  elements.executions.innerHTML = state.executions.map((execution) => `
    <tr>
      <td><strong>${escapeHTML(execution.buyerID)}</strong></td>
      <td>${escapeHTML(execution.processID)}<br><small>${escapeHTML(execution.flowID)}</small></td>
      <td>${escapeHTML(execution.currentState || 'entering first state')}</td>
      <td>${escapeHTML(execution.pendingPreConditionName || '—')}</td>
      <td><span class="status ${execution.status === 'RUNNING' ? '' : 'terminal'} ${execution.status === 'COMPLETED' ? 'completed' : ''}">${escapeHTML(execution.status)}</span></td>
      <td><pre class="data-preview">${escapeHTML(JSON.stringify(execution.stateData, null, 2))}</pre></td>
      <td><button class="ghost small" data-use-flow="${escapeHTML(execution.flowID)}">Use</button></td>
    </tr>`).join('');
  elements.executions.querySelectorAll('[data-use-flow]').forEach((button) => {
    button.addEventListener('click', () => {
      state.selectedFlowID = button.dataset.useFlow;
      renderExecutionOptions();
      elements.buyerWorkspace.scrollIntoView({ behavior: 'smooth' });
    });
  });
}

function renderExecutionOptions() {
  if (!state.executions.some((execution) => execution.flowID === state.selectedFlowID)) {
    state.selectedFlowID = state.executions[0]?.flowID ?? '';
  }
  elements.messageFlowID.innerHTML = state.executions.map((execution) =>
    `<option value="${escapeHTML(execution.flowID)}" ${execution.flowID === state.selectedFlowID ? 'selected' : ''}>${escapeHTML(execution.buyerID)} · ${escapeHTML(execution.currentState || 'initializing')}</option>`
  ).join('');
  renderConditionOptions();
}

function renderConditionOptions() {
  if (!state.process) return;
  const names = conditionNames();
  const execution = state.executions.find((entry) => entry.flowID === state.selectedFlowID);
  const suggested = suggestedCondition(execution);
  const selected = names.includes(elements.messageCondition.value) ? elements.messageCondition.value : suggested || names[0];
  elements.messageCondition.innerHTML = names.map((name) =>
    `<option value="${escapeHTML(name)}" ${name === selected ? 'selected' : ''}>${escapeHTML(name)}</option>`
  ).join('');
  if (Object.keys(state.messageData).length === 0) state.messageData = messageDefaults(selected);
  renderMessageData();
}

function suggestedCondition(execution) {
  if (!execution) return '';
  if (execution.pendingPreConditionName) return execution.pendingPreConditionName;
  const dealState = state.process.states.find((entry) => entry.name === execution.currentState);
  return dealState?.postCondition?.waitFor?.name ?? '';
}

function conditionNames() {
  const names = [];
  state.process.states.forEach((dealState) => {
    if (dealState.preCondition?.name) names.push(dealState.preCondition.name);
    if (dealState.postCondition?.waitFor?.name) names.push(dealState.postCondition.waitFor.name);
  });
  return [...new Set(names)];
}

function messageDefaults(conditionName) {
  switch (conditionName) {
    case 'buyer-proposal':
      return { proposedSamplePrice: '10', proposedFullPrice: '100', proposedSampleRefundPrice: '5' };
    case 'seller-price-response':
      return { acceptedProposedPrice: 'true' };
    case 'sample-feedback':
      return { proceedToFullDataset: 'true' };
    default:
      return {};
  }
}

function renderMessageData() {
  renderKeyValues(elements.messageData, state.messageData, renderMessageData);
}

function renderKeyValues(container, values, rerender) {
  container.replaceChildren();
  Object.entries(values).forEach(([key, value]) => {
    const row = document.createElement('div');
    row.className = 'kv-row';
    row.innerHTML = `<input data-part="key" value="${escapeHTML(key)}" placeholder="key" /><input data-part="value" value="${escapeHTML(value)}" placeholder="value" /><button class="icon-button" aria-label="Remove">×</button>`;
    row.querySelector('[data-part="key"]').addEventListener('change', (event) => {
      const nextKey = event.target.value.trim();
      if (!nextKey || (nextKey !== key && Object.hasOwn(values, nextKey))) return showToast('Keys must be non-empty and unique.', true);
      const currentValue = values[key];
      delete values[key];
      values[nextKey] = currentValue;
      rerender();
    });
    row.querySelector('[data-part="value"]').addEventListener('input', (event) => {
      values[key] = event.target.value;
    });
    row.querySelector('button').addEventListener('click', () => {
      delete values[key];
      rerender();
    });
    container.append(row);
  });
}

function renderStateDataKeys() {
  elements.stateDataKeys.innerHTML = Object.keys(state.process.initialStateData)
    .map((key) => `<option value="${escapeHTML(key)}"></option>`).join('');
}

function stateOptions(selected) {
  return state.process.states.map((entry) =>
    `<option value="${escapeHTML(entry.name)}" ${entry.name === selected ? 'selected' : ''}>${escapeHTML(entry.name)}</option>`
  ).join('');
}

function keyOptions(selected) {
  const keys = ['', ...Object.keys(state.process.initialStateData)];
  return keys.map((key) =>
    `<option value="${escapeHTML(key)}" ${key === selected ? 'selected' : ''}>${escapeHTML(key || 'No comparison · default only')}</option>`
  ).join('');
}

function actionOptions(selected) {
  return state.actions.map((name) =>
    `<option value="${escapeHTML(name)}" ${name === selected ? 'selected' : ''}>${escapeHTML(name)}</option>`
  ).join('');
}

function addUniqueKey(target, base, value) {
  let key = base;
  let index = 2;
  while (Object.hasOwn(target, key)) key = `${base}${index++}`;
  target[key] = value;
}

function buyerLabel(role) {
  return role.replace('buyer', 'Buyer ');
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { 'Content-Type': 'application/json', ...(options.headers ?? {}) },
    ...options,
  });
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error ?? `Request failed with HTTP ${response.status}`);
  return payload;
}

function showToast(message, error = false) {
  elements.toast.textContent = message;
  elements.toast.classList.toggle('error', error);
  elements.toast.classList.add('visible');
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => elements.toast.classList.remove('visible'), 3500);
}

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;',
  })[character]);
}

initialize().catch((error) => showToast(error.message, true));
