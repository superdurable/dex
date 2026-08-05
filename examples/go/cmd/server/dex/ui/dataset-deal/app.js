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
  role: new URLSearchParams(window.location.search).get('role') || 'seller',
  actions: [],
  processes: [],
  executions: [],
  filterProcessID: '',
  filterStatus: '',
  filterCurrentState: '',
  filterPendingConditionName: '',
  process: null,
  processIsNew: false,
  execution: null,
  selectedStateName: '',
  messageCondition: '',
  messageData: {},
};

const app = document.querySelector('#app');
const toast = document.querySelector('#toast');

async function initialize() {
  bindNavigation();
  const actionResponse = await api('/api/dataset-deal/actions');
  state.actions = actionResponse.actions;
  await renderRoute();
}

function bindNavigation() {
  document.querySelectorAll('.role').forEach((button) => {
    button.addEventListener('click', () => navigate('/dataset-deal', button.dataset.role));
  });
  document.querySelector('.brand').addEventListener('click', (event) => {
    event.preventDefault();
    navigate('/dataset-deal');
  });
  window.addEventListener('popstate', () => renderRoute());
}

async function renderRoute() {
  const role = new URLSearchParams(window.location.search).get('role');
  if (role) state.role = role;
  updateRoleSwitcher();
  app.innerHTML = '<div class="loading">Loading Dataset Deal…</div>';
  const route = currentRoute();
  try {
    if (route.kind === 'process') {
      await loadProcessPage(route.id);
      return;
    }
    if (route.kind === 'execution') {
      await loadExecutionPage(route.id);
      return;
    }
    await loadDashboard();
  } catch (error) {
    renderError(error);
  }
}

function currentRoute() {
  const parts = window.location.pathname.split('/').filter(Boolean);
  if (parts[0] !== 'dataset-deal') return { kind: 'dashboard' };
  if (parts[1] === 'processes' && parts[2]) {
    return { kind: 'process', id: decodeURIComponent(parts.slice(2).join('/')) };
  }
  if (parts[1] === 'executions' && parts[2]) {
    return { kind: 'execution', id: decodeURIComponent(parts.slice(2).join('/')) };
  }
  return { kind: 'dashboard' };
}

function navigate(path, role = state.role) {
  const destination = new URL(path, window.location.origin);
  destination.searchParams.set('role', role);
  window.history.pushState({}, '', destination);
  state.role = role;
  state.filterProcessID = '';
  state.filterStatus = '';
  state.filterCurrentState = '';
  state.filterPendingConditionName = '';
  renderRoute();
}

function updateRoleSwitcher() {
  document.querySelectorAll('.role').forEach((button) => {
    button.classList.toggle('active', button.dataset.role === state.role);
  });
}

async function loadDashboard() {
  state.process = null;
  state.execution = null;
  const [processResponse, executionResponse] = await Promise.all([
    api('/api/dataset-deal/processes'),
    api(executionListURL()),
  ]);
  state.processes = processResponse.processes;
  state.executions = executionResponse.executions;
  renderDashboard();
}

function executionListURL() {
  const parameters = new URLSearchParams();
  if (state.role !== 'seller') parameters.set('buyerID', state.role);
  if (state.filterProcessID) parameters.set('processID', state.filterProcessID);
  if (state.filterStatus) parameters.set('status', state.filterStatus);
  if (state.filterCurrentState) parameters.set('currentState', state.filterCurrentState);
  if (state.filterPendingConditionName) parameters.set('pendingConditionName', state.filterPendingConditionName);
  const query = parameters.toString();
  return `/api/dataset-deal/executions${query ? `?${query}` : ''}`;
}

function renderDashboard() {
  const seller = state.role === 'seller';
  app.innerHTML = `
    <div class="page">
      <header class="page-heading">
        <div>
          <p class="eyebrow">${seller ? 'SELLER CONTROL ROOM' : `${buyerLabel(state.role).toUpperCase()} PORTFOLIO`}</p>
          <h1>${seller ? 'Processes & executions' : 'My deal executions'}</h1>
          <p>${seller
            ? 'Choose a process to edit its state machine, or inspect any durable execution.'
            : 'Start and inspect your dataset deals. Execution filters run directly against PostgreSQL.'}</p>
        </div>
        ${seller ? '<button id="new-process" class="primary">+ New process</button>' : ''}
      </header>
      <div class="dashboard ${seller ? '' : 'buyer'}">
        ${seller ? processListPanelHTML() : ''}
        ${executionListPanelHTML(seller)}
      </div>
    </div>`;
  bindDashboard();
}

function processListPanelHTML() {
  const contents = state.processes.length === 0
    ? '<div class="empty">No process definitions yet.</div>'
    : state.processes.map((process) => `
      <button class="list-card process-card" data-process-id="${escapeHTML(process.processID)}">
        <strong>${escapeHTML(process.processID)}</strong>
        <small>${process.states.length} states · starts at ${escapeHTML(process.initialState)}</small>
      </button>`).join('');
  return `
    <section class="panel">
      <div class="panel-header"><h2>Processes</h2><small>${state.processes.length} definitions</small></div>
      <div class="panel-body process-list">${contents}</div>
    </section>`;
}

function executionListPanelHTML(seller) {
  const processOptions = [
    '<option value="">All processes</option>',
    ...state.processes.map((process) => `<option value="${escapeHTML(process.processID)}" ${process.processID === state.filterProcessID ? 'selected' : ''}>${escapeHTML(process.processID)}</option>`),
  ].join('');
  const statusOptions = ['', 'PROCESSING', 'WAITING', 'COMPLETED']
    .map((status) => `<option value="${status}" ${status === state.filterStatus ? 'selected' : ''}>${status || 'All statuses'}</option>`)
    .join('');
  const contents = state.executions.length === 0
    ? '<div class="empty">No matching deal executions.</div>'
    : state.executions.map(executionCardHTML).join('');
  return `
    <section class="panel">
      <div class="panel-header">
        <div><h2>${seller ? 'Executions' : `${buyerLabel(state.role)} executions`}</h2><small>${state.executions.length} stored executions</small></div>
        <div class="filter-bar">
          <label>Filter by ProcessID<select id="process-filter">${processOptions}</select></label>
          <label>Status<select id="status-filter">${statusOptions}</select></label>
          <label>Current state<input id="current-state-filter" value="${escapeHTML(state.filterCurrentState)}" placeholder="Any state" /></label>
          <label>Pending condition<input id="pending-condition-filter" value="${escapeHTML(state.filterPendingConditionName)}" placeholder="Any condition" /></label>
          ${seller ? '' : `<button id="start-execution" class="primary" ${state.filterProcessID ? '' : 'disabled'}>Start selected process</button>`}
          <button id="refresh-executions" class="ghost">Refresh</button>
        </div>
      </div>
      <div class="panel-body execution-list">${contents}</div>
    </section>`;
}

function executionCardHTML(execution) {
  return `
    <button class="list-card execution-card" data-flow-id="${escapeHTML(execution.flowID)}">
      <span class="identity"><strong>${escapeHTML(execution.processID)}</strong><small>${escapeHTML(execution.flowID)}</small></span>
      <span><span class="cell-label">Buyer</span>${escapeHTML(execution.buyerID)}</span>
      <span><span class="cell-label">Current state</span>${escapeHTML(execution.currentState || 'initializing')}</span>
      <span class="pending"><span class="cell-label">Pending condition</span>${escapeHTML(execution.pendingConditionName || '—')}</span>
      <span class="status ${statusClass(execution.status)}">${escapeHTML(execution.status)}</span>
    </button>`;
}

function bindDashboard() {
  document.querySelector('#new-process')?.addEventListener('click', () => navigate('/dataset-deal/processes/new'));
  document.querySelector('#process-filter').addEventListener('change', async (event) => {
    state.filterProcessID = event.target.value;
    await refreshDashboardExecutions();
  });
  document.querySelector('#status-filter').addEventListener('change', async (event) => {
    state.filterStatus = event.target.value;
    await refreshDashboardExecutions();
  });
  document.querySelector('#current-state-filter').addEventListener('change', async (event) => {
    state.filterCurrentState = event.target.value.trim();
    await refreshDashboardExecutions();
  });
  document.querySelector('#pending-condition-filter').addEventListener('change', async (event) => {
    state.filterPendingConditionName = event.target.value.trim();
    await refreshDashboardExecutions();
  });
  document.querySelector('#refresh-executions').addEventListener('click', refreshDashboardExecutions);
  document.querySelector('#start-execution')?.addEventListener('click', startExecution);
  document.querySelectorAll('.process-card').forEach((button) => {
    button.addEventListener('click', () => navigate(`/dataset-deal/processes/${encodeURIComponent(button.dataset.processId)}`));
  });
  document.querySelectorAll('.execution-card').forEach((button) => {
    button.addEventListener('click', () => navigate(`/dataset-deal/executions/${encodeURIComponent(button.dataset.flowId)}`));
  });
}

async function refreshDashboardExecutions() {
  try {
    const response = await api(executionListURL());
    state.executions = response.executions;
    renderDashboard();
  } catch (error) {
    showToast(error.message, true);
  }
}

async function startExecution() {
  if (!state.filterProcessID) return showToast('Select a ProcessID first.', true);
  try {
    const response = await api('/api/dataset-deal/executions', {
      method: 'POST',
      body: JSON.stringify({ processID: state.filterProcessID, buyerID: state.role }),
    });
    showToast(`Started ${response.flowID}.`);
    navigate(`/dataset-deal/executions/${encodeURIComponent(response.flowID)}`);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function loadProcessPage(processID) {
  state.execution = null;
  state.processIsNew = processID === 'new';
  const process = state.processIsNew
    ? await api('/dataset-deal/comprehensive-process.json')
    : await api(`/api/dataset-deal/processes/${encodeURIComponent(processID)}`);
  state.process = normalizeProcess(structuredClone(process));
  state.selectedStateName = state.process.initialState;
  renderProcessPage();
}

function renderProcessPage() {
  const selectedState = selectedProcessState();
  app.innerHTML = `
    <div class="page">
      <a class="back-link" href="${dashboardURL()}" data-back>← Processes & executions</a>
      <header class="page-heading">
        <div>
          <p class="eyebrow">PROCESS DESIGNER</p>
          <h1>${state.processIsNew ? 'Create process' : escapeHTML(state.process.processID)}</h1>
          <p>Edit the graph definition stored in PostgreSQL. Existing executions continue using their immutable Dex snapshots.</p>
        </div>
        <div class="heading-actions">
          <button id="add-state" class="ghost">+ Add state</button>
          <button id="save-process" class="primary">${state.processIsNew ? 'Create process' : 'Save changes'}</button>
        </div>
      </header>
      <div class="process-shell">
        ${graphPanelHTML('Process graph')}
        <aside class="panel editor-panel">
          ${processMetaHTML()}
          ${stateEditorHTML(selectedState)}
        </aside>
      </div>
    </div>`;
  bindBackLink();
  bindProcessEditor(selectedState);
  renderGraph(state.process, state.selectedStateName, '', true);
}

function processMetaHTML() {
  return `
    <section class="editor-section stack">
      <h3>Process settings</h3>
      <label>Process ID<input id="process-id" value="${escapeHTML(state.process.processID)}" ${state.processIsNew ? '' : 'disabled'} /></label>
      <label>Initial state<select id="initial-state">${stateOptions(state.process.initialState)}</select></label>
      <div>
        <div class="section-title"><h4>Initial stateData</h4><button id="add-initial-data" class="ghost small">+ key/value</button></div>
        <div id="initial-data" class="row-list">${keyValueRowsHTML(state.process.initialStateData)}</div>
      </div>
    </section>`;
}

function stateEditorHTML(dealState) {
  if (!dealState) return '<section class="editor-section"><div class="empty">Select a state in the graph.</div></section>';
  const postCondition = dealState.postCondition;
  const decision = postCondition?.decision ?? { key: '', cases: [], elseState: state.process.initialState };
  return `
    <section class="editor-section stack">
      <div class="section-title"><h3>Selected state</h3><button id="remove-state" class="ghost small danger">Remove</button></div>
      <label>State name<input id="state-name" value="${escapeHTML(dealState.name)}" /></label>
      <label>Pre-condition channel<input id="pre-condition" value="${escapeHTML(dealState.preCondition?.name ?? '')}" placeholder="Enter immediately when empty" /></label>
    </section>
    ${actionEditorHTML('Pre-actions', 'pre', dealState.preActions)}
    ${actionEditorHTML('Post-actions', 'post', dealState.postActions)}
    <section class="editor-section stack">
      <label class="toggle"><input id="has-post-condition" type="checkbox" ${postCondition ? 'checked' : ''} /> Continue to another state</label>
      ${postCondition ? `
        <label>Post-condition channel<input id="post-condition-wait" value="${escapeHTML(postCondition.waitFor?.name ?? '')}" placeholder="Evaluate immediately when empty" /></label>
        <div class="form-grid">
          <label>Compare stateData key<input id="decision-key" value="${escapeHTML(decision.key)}" list="state-data-keys" placeholder="Empty for unconditional" /></label>
          <label>Else go to<select id="else-state">${stateOptions(decision.elseState)}</select></label>
        </div>
        <datalist id="state-data-keys">${Object.keys(state.process.initialStateData).map((key) => `<option value="${escapeHTML(key)}"></option>`).join('')}</datalist>
        <div class="section-title"><h4>Equality cases</h4><button id="add-case" class="ghost small">+ case</button></div>
        <div id="case-list" class="row-list">${decision.cases.map(caseRowHTML).join('')}</div>` : ''}
    </section>`;
}

function actionEditorHTML(title, phase, actions) {
  return `
    <section class="editor-section">
      <div class="section-title"><h4>${title} · ordered</h4><button class="ghost small" data-add-action="${phase}">+ action</button></div>
      <div class="row-list" data-action-list="${phase}">${actions.map(actionRowHTML).join('')}</div>
    </section>`;
}

function actionRowHTML(action, index) {
  return `
    <div class="action-row" data-index="${index}">
      <select data-action-name>${actionOptions(action)}</select>
      <button class="icon-button" data-move-up aria-label="Move up">↑</button>
      <button class="icon-button" data-move-down aria-label="Move down">↓</button>
      <button class="icon-button" data-remove aria-label="Remove">×</button>
    </div>`;
}

function caseRowHTML(conditionCase, index) {
  return `
    <div class="case-row" data-index="${index}">
      <input data-case-equals value="${escapeHTML(conditionCase.equals)}" placeholder="equals value" />
      <select data-case-target>${stateOptions(conditionCase.goToState)}</select>
      <button class="icon-button" data-remove-case aria-label="Remove">×</button>
    </div>`;
}

function bindProcessEditor(dealState) {
  document.querySelector('#save-process').addEventListener('click', saveProcess);
  document.querySelector('#add-state').addEventListener('click', addState);
  document.querySelector('#process-id').addEventListener('input', (event) => {
    state.process.processID = event.target.value.trim();
  });
  document.querySelector('#initial-state').addEventListener('change', (event) => {
    state.process.initialState = event.target.value;
    renderProcessPage();
  });
  document.querySelector('#add-initial-data').addEventListener('click', () => {
    addUniqueKey(state.process.initialStateData, 'newKey', '');
    renderProcessPage();
  });
  bindKeyValueRows('#initial-data', state.process.initialStateData, renderProcessPage);
  if (!dealState) return;
  document.querySelector('#remove-state').addEventListener('click', removeSelectedState);
  document.querySelector('#state-name').addEventListener('change', (event) => renameState(dealState.name, event.target.value.trim()));
  document.querySelector('#pre-condition').addEventListener('input', (event) => {
    const name = event.target.value.trim();
    dealState.preCondition = name ? { name } : undefined;
  });
  bindActionEditor(dealState, 'pre');
  bindActionEditor(dealState, 'post');
  bindPostConditionEditor(dealState);
}

function bindActionEditor(dealState, phase) {
  const property = phase === 'pre' ? 'preActions' : 'postActions';
  document.querySelector(`[data-add-action="${phase}"]`).addEventListener('click', () => {
    dealState[property].push(state.actions[0]);
    renderProcessPage();
  });
  document.querySelectorAll(`[data-action-list="${phase}"] .action-row`).forEach((row) => {
    const index = Number(row.dataset.index);
    row.querySelector('[data-action-name]').addEventListener('change', (event) => {
      dealState[property][index] = event.target.value;
    });
    row.querySelector('[data-move-up]').addEventListener('click', () => moveItem(dealState[property], index, index - 1));
    row.querySelector('[data-move-down]').addEventListener('click', () => moveItem(dealState[property], index, index + 1));
    row.querySelector('[data-remove]').addEventListener('click', () => {
      dealState[property].splice(index, 1);
      renderProcessPage();
    });
  });
}

function bindPostConditionEditor(dealState) {
  document.querySelector('#has-post-condition').addEventListener('change', (event) => {
    dealState.postCondition = event.target.checked
      ? { decision: { key: '', cases: [], elseState: state.process.initialState } }
      : undefined;
    renderProcessPage();
  });
  if (!dealState.postCondition) return;
  document.querySelector('#post-condition-wait').addEventListener('input', (event) => {
    const name = event.target.value.trim();
    dealState.postCondition.waitFor = name ? { name } : undefined;
  });
  document.querySelector('#decision-key').addEventListener('input', (event) => {
    dealState.postCondition.decision.key = event.target.value.trim();
  });
  document.querySelector('#else-state').addEventListener('change', (event) => {
    dealState.postCondition.decision.elseState = event.target.value;
    renderProcessPage();
  });
  document.querySelector('#add-case').addEventListener('click', () => {
    dealState.postCondition.decision.cases.push({ equals: '', goToState: state.process.initialState });
    renderProcessPage();
  });
  document.querySelectorAll('#case-list .case-row').forEach((row) => {
    const index = Number(row.dataset.index);
    row.querySelector('[data-case-equals]').addEventListener('input', (event) => {
      dealState.postCondition.decision.cases[index].equals = event.target.value;
    });
    row.querySelector('[data-case-target]').addEventListener('change', (event) => {
      dealState.postCondition.decision.cases[index].goToState = event.target.value;
      renderProcessPage();
    });
    row.querySelector('[data-remove-case]').addEventListener('click', () => {
      dealState.postCondition.decision.cases.splice(index, 1);
      renderProcessPage();
    });
  });
}

async function saveProcess() {
  try {
    const wasNew = state.processIsNew;
    const path = wasNew
      ? '/api/dataset-deal/processes'
      : `/api/dataset-deal/processes/${encodeURIComponent(state.process.processID)}`;
    await api(path, { method: wasNew ? 'POST' : 'PUT', body: JSON.stringify(state.process) });
    state.processIsNew = false;
    window.history.replaceState({}, '', detailURL('processes', state.process.processID));
    showToast(`Process ${state.process.processID} ${wasNew ? 'created' : 'updated'}.`);
    renderProcessPage();
  } catch (error) {
    showToast(error.message, true);
  }
}

function addState() {
  const names = new Set(state.process.states.map((entry) => entry.name));
  let index = state.process.states.length + 1;
  while (names.has(`state-${index}`)) index += 1;
  const dealState = { name: `state-${index}`, preActions: [], postActions: [] };
  state.process.states.push(dealState);
  state.selectedStateName = dealState.name;
  renderProcessPage();
}

function removeSelectedState() {
  if (state.process.states.length === 1) return showToast('A process needs at least one state.', true);
  const removed = selectedProcessState();
  state.process.states = state.process.states.filter((entry) => entry.name !== removed.name);
  const fallback = state.process.states[0].name;
  state.process.states.forEach((entry) => {
    if (entry.postCondition?.decision.elseState === removed.name) entry.postCondition.decision.elseState = fallback;
    entry.postCondition?.decision.cases.forEach((conditionCase) => {
      if (conditionCase.goToState === removed.name) conditionCase.goToState = fallback;
    });
  });
  if (state.process.initialState === removed.name) state.process.initialState = fallback;
  state.selectedStateName = fallback;
  renderProcessPage();
}

function renameState(previous, next) {
  if (!next || state.process.states.some((entry) => entry.name === next && entry.name !== previous)) {
    showToast('State names must be non-empty and unique.', true);
    return renderProcessPage();
  }
  state.process.states.forEach((entry) => {
    if (entry.name === previous) entry.name = next;
    if (entry.postCondition?.decision.elseState === previous) entry.postCondition.decision.elseState = next;
    entry.postCondition?.decision.cases.forEach((conditionCase) => {
      if (conditionCase.goToState === previous) conditionCase.goToState = next;
    });
  });
  if (state.process.initialState === previous) state.process.initialState = next;
  state.selectedStateName = next;
  renderProcessPage();
}

function moveItem(items, from, to) {
  if (to < 0 || to >= items.length) return;
  const [item] = items.splice(from, 1);
  items.splice(to, 0, item);
  renderProcessPage();
}

async function loadExecutionPage(flowID) {
  state.execution = await api(`/api/dataset-deal/executions/${encodeURIComponent(flowID)}`);
  state.process = normalizeProcess(structuredClone(state.execution.processDefinition));
  state.selectedStateName = state.execution.currentState || state.process.initialState;
  state.messageCondition = suggestedCondition(state.execution);
  state.messageData = messageDefaults(state.messageCondition);
  renderExecutionPage();
  if (state.execution.status === 'PROCESSING') {
    window.setTimeout(() => {
      const route = currentRoute();
      if (route.kind === 'execution' && route.id === flowID) loadExecutionPage(flowID);
    }, 500);
  }
}

function renderExecutionPage() {
  const execution = state.execution;
  const selectedState = selectedProcessState();
  app.innerHTML = `
    <div class="page">
      <a class="back-link" href="${dashboardURL()}" data-back>← Processes & executions</a>
      <header class="page-heading">
        <div>
          <p class="eyebrow">DURABLE EXECUTION</p>
          <h1>${escapeHTML(execution.processID)}</h1>
          <p>${escapeHTML(execution.flowID)}</p>
        </div>
        <div class="heading-actions"><span class="status ${statusClass(execution.status)}">${escapeHTML(execution.status)}</span><button id="refresh-execution" class="ghost">Refresh</button></div>
      </header>
      ${executionPropertiesHTML(execution)}
      <div class="execution-shell">
        ${graphPanelHTML('Execution graph')}
        <aside class="panel editor-panel">
          ${readOnlyStateHTML(selectedState)}
          ${stateDataHTML(execution.stateData)}
          ${conditionMessageHTML(execution)}
        </aside>
      </div>
    </div>`;
  bindBackLink();
  document.querySelector('#refresh-execution').addEventListener('click', () => loadExecutionPage(execution.flowID));
  bindConditionMessage(execution);
  renderGraph(state.process, state.selectedStateName, execution.currentState, true);
}

function executionPropertiesHTML(execution) {
  const values = [
    ['Buyer', execution.buyerID],
    ['Latest trigger RunID', execution.latestRunID],
    ['Current state', execution.currentState || 'Initializing'],
    ['Target state', execution.targetState || '—'],
    ['Pending condition', execution.pendingConditionName || '—'],
    ['Pending phase', execution.pendingConditionPhase || '—'],
    ['Action phase', execution.currentActionPhase || '—'],
    ['Action index', execution.currentActionIndexToExecute],
    ['Created', formatDate(execution.createdAt)],
    ['Updated', formatDate(execution.updatedAt)],
    ['Completed', execution.completedAt ? formatDate(execution.completedAt) : '—'],
  ];
  return `<section class="property-grid">${values.map(([label, value]) => `<div class="property"><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join('')}</section>`;
}

function readOnlyStateHTML(dealState) {
  if (!dealState) return '<section class="editor-section"><div class="empty">Select a state in the graph.</div></section>';
  const decision = dealState.postCondition?.decision;
  const decisionText = !dealState.postCondition
    ? 'Complete execution'
    : decision.cases.length === 0
      ? `Go to ${decision.elseState}`
      : `${decision.key}: ${decision.cases.map((entry) => `${entry.equals} → ${entry.goToState}`).join(', ')}; else → ${decision.elseState}`;
  return `
    <section class="editor-section">
      <h3>${escapeHTML(dealState.name)}</h3>
      <div class="definition-summary">
        <div><strong>Pre-condition:</strong> ${escapeHTML(dealState.preCondition?.name || 'immediate')}</div>
        <div><strong>Pre-actions:</strong> ${escapeHTML(dealState.preActions.join(' → ') || 'none')}</div>
        <div><strong>Post-actions:</strong> ${escapeHTML(dealState.postActions.join(' → ') || 'none')}</div>
        <div><strong>Post-condition wait:</strong> ${escapeHTML(dealState.postCondition?.waitFor?.name || 'none')}</div>
        <div><strong>Decision:</strong> ${escapeHTML(decisionText)}</div>
      </div>
    </section>`;
}

function stateDataHTML(stateData) {
  const rows = Object.entries(stateData).sort(([first], [second]) => first.localeCompare(second));
  return `
    <section class="editor-section">
      <h3>Current stateData</h3>
      <table class="data-table"><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody>
        ${rows.map(([key, value]) => `<tr><td><code>${escapeHTML(key)}</code></td><td>${escapeHTML(value)}</td></tr>`).join('')}
      </tbody></table>
    </section>`;
}

function conditionMessageHTML(execution) {
  const selected = execution.pendingConditionName || '';
  state.messageCondition = selected;
  return `
    <section class="editor-section message-panel">
      <h3>Send condition message</h3>
      <label>Pending condition<select id="message-condition" disabled>${selected ? `<option value="${escapeHTML(selected)}">${escapeHTML(selected)}</option>` : '<option value="">No pending condition</option>'}</select></label>
      <div>
        <div class="section-title"><h4>stateData updates</h4><button id="add-message-data" class="ghost small">+ key/value</button></div>
        <div id="message-data" class="row-list">${keyValueRowsHTML(state.messageData)}</div>
      </div>
      <div class="message-actions"><button id="send-message" class="primary" ${execution.status === 'WAITING' && selected ? '' : 'disabled'}>Send message</button></div>
    </section>`;
}

function bindConditionMessage(execution) {
  document.querySelector('#add-message-data').addEventListener('click', () => {
    addUniqueKey(state.messageData, 'newKey', '');
    renderExecutionPage();
  });
  bindKeyValueRows('#message-data', state.messageData, renderExecutionPage);
  document.querySelector('#send-message').addEventListener('click', async () => {
    try {
      state.execution = await api(`/api/dataset-deal/executions/${encodeURIComponent(execution.flowID)}/conditions/${encodeURIComponent(state.messageCondition)}`, {
        method: 'POST',
        body: JSON.stringify({ data: state.messageData }),
      });
      showToast(`Triggered ${state.messageCondition}.`);
      state.messageCondition = suggestedCondition(state.execution);
      state.messageData = messageDefaults(state.messageCondition);
      renderExecutionPage();
    } catch (error) {
      showToast(error.message, true);
    }
  });
}

function graphPanelHTML(title) {
  return `
    <section class="panel graph-panel">
      <div class="graph-toolbar">
        <h2>${title}</h2>
        <div class="graph-legend"><span><i class="legend-line"></i>single choice</span><span><i class="legend-line dashed"></i>branching choice</span></div>
      </div>
      <div class="graph-scroll"><div id="graph"></div></div>
    </section>`;
}

function renderGraph(process, selectedState, currentState, interactive) {
  const container = document.querySelector('#graph');
  const nodeWidth = 220;
  const nodeHeight = 82;
  const availableWidth = Math.max(nodeWidth, container.parentElement.clientWidth - 16);
  const layout = buildGraphLayout(process, nodeWidth, nodeHeight, availableWidth);
  container.innerHTML = `
    <svg class="process-graph" viewBox="0 0 ${layout.width} ${layout.height}" width="${layout.width}" height="${layout.height}" role="img" aria-label="Deal process graph">
      <defs>
        <marker id="arrow-solid" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#557274"></path></marker>
        <marker id="arrow-branch" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#ef5b2a"></path></marker>
      </defs>
      ${layout.edges.map((edge) => graphEdgeHTML(edge, layout.positions, nodeWidth, nodeHeight)).join('')}
      ${process.states.map((dealState) => graphNodeHTML(dealState, layout.positions.get(dealState.name), nodeWidth, nodeHeight, selectedState, currentState, process.initialState)).join('')}
    </svg>`;
  if (!interactive) return;
  container.querySelectorAll('.graph-node').forEach((node) => {
    node.addEventListener('click', () => {
      state.selectedStateName = node.dataset.stateName;
      if (state.execution) renderExecutionPage();
      else renderProcessPage();
    });
  });
}

function buildGraphLayout(process, nodeWidth, nodeHeight, availableWidth) {
  const horizontalGap = 70;
  const rankGap = 120;
  const rowGap = 60;
  const horizontalPadding = 54;
  const verticalPadding = 54;
  const edges = [];
  const outgoingByState = new Map();
  process.states.forEach((dealState) => {
    const choices = outgoingChoices(dealState);
    const outgoing = choices.map((choice) => ({
      source: dealState.name,
      ...choice,
      branch: choices.length > 1,
    }));
    outgoingByState.set(dealState.name, outgoing);
    edges.push(...outgoing);
  });

  const ranks = new Map();
  const discoveryOrder = [];
  const initialState = process.states.some((dealState) => dealState.name === process.initialState)
    ? process.initialState
    : process.states[0]?.name;
  if (initialState) {
    ranks.set(initialState, 0);
    discoveryOrder.push(initialState);
  }
  for (let cursor = 0; cursor < discoveryOrder.length; cursor += 1) {
    const source = discoveryOrder[cursor];
    const nextRank = ranks.get(source) + 1;
    (outgoingByState.get(source) ?? []).forEach((edge) => {
      if (!ranks.has(edge.target)) {
        ranks.set(edge.target, nextRank);
        discoveryOrder.push(edge.target);
      }
    });
  }

  const reachableMaxRank = Math.max(0, ...ranks.values());
  process.states.forEach((dealState) => {
    if (!ranks.has(dealState.name)) {
      ranks.set(dealState.name, reachableMaxRank + 1);
      discoveryOrder.push(dealState.name);
    }
  });

  const statesByRank = new Map();
  discoveryOrder.forEach((stateName) => {
    const rank = ranks.get(stateName);
    if (!statesByRank.has(rank)) statesByRank.set(rank, []);
    statesByRank.get(rank).push(stateName);
  });
  const maximumRank = Math.max(0, ...ranks.values());
  const graphWidth = Math.max(nodeWidth + horizontalPadding * 2, Math.floor(availableWidth));
  const usableWidth = graphWidth - horizontalPadding * 2;
  const maximumColumns = Math.max(1, Math.floor((usableWidth + horizontalGap) / (nodeWidth + horizontalGap)));
  const positions = new Map();
  let nextRankY = verticalPadding;
  for (let rank = 0; rank <= maximumRank; rank += 1) {
    const stateNames = statesByRank.get(rank) ?? [];
    const rowCount = Math.max(1, Math.ceil(stateNames.length / maximumColumns));
    for (let row = 0; row < rowCount; row += 1) {
      const rowStates = stateNames.slice(row * maximumColumns, (row + 1) * maximumColumns);
      const rowWidth = rowStates.length * nodeWidth + Math.max(0, rowStates.length - 1) * horizontalGap;
      const startX = (graphWidth - rowWidth) / 2;
      rowStates.forEach((stateName, column) => {
        positions.set(stateName, {
          x: startX + column * (nodeWidth + horizontalGap),
          y: nextRankY + row * (nodeHeight + rowGap),
        });
      });
    }
    nextRankY += rowCount * nodeHeight + Math.max(0, rowCount - 1) * rowGap + rankGap;
  }

  let backwardIndex = 0;
  const routedEdges = edges.map((edge) => {
    if (ranks.get(edge.target) > ranks.get(edge.source)) return { ...edge, route: 'forward' };
    const routed = { ...edge, route: 'backward', routeIndex: backwardIndex };
    backwardIndex += 1;
    return routed;
  });
  return {
    positions,
    edges: routedEdges,
    width: graphWidth,
    height: nextRankY - rankGap + verticalPadding,
  };
}

function outgoingChoices(dealState) {
  if (!dealState.postCondition) return [];
  const decision = dealState.postCondition.decision;
  const choices = decision.cases.map((conditionCase) => ({
    target: conditionCase.goToState,
    label: `${decision.key} = ${conditionCase.equals}`,
  }));
  choices.push({ target: decision.elseState, label: decision.cases.length === 0 ? 'next' : 'else' });
  return choices;
}

function graphEdgeHTML(edge, positions, nodeWidth, nodeHeight) {
  const source = positions.get(edge.source);
  const target = positions.get(edge.target);
  if (!source || !target) return '';
  let path;
  let labelX;
  let labelY;
  if (edge.source === edge.target) {
    path = `M ${source.x + nodeWidth} ${source.y + nodeHeight / 2} C ${source.x + nodeWidth + 80} ${source.y - 45}, ${source.x + nodeWidth + 80} ${source.y + nodeHeight + 45}, ${source.x + nodeWidth} ${source.y + nodeHeight / 2 + 10}`;
    labelX = source.x + nodeWidth + 54;
    labelY = source.y + nodeHeight / 2;
  } else if (edge.route === 'backward') {
    const startX = source.x;
    const startY = source.y + nodeHeight / 2;
    const endX = target.x;
    const endY = target.y + nodeHeight / 2;
    const laneX = 20 + edge.routeIndex * 22;
    path = `M ${startX} ${startY} C ${laneX} ${startY}, ${laneX} ${endY}, ${endX} ${endY}`;
    labelX = laneX + 8;
    labelY = (startY + endY) / 2 - 8;
  } else {
    const startX = source.x + nodeWidth / 2;
    const startY = source.y + nodeHeight;
    const endX = target.x + nodeWidth / 2;
    const endY = target.y;
    const bend = Math.max(55, (endY - startY) * 0.35);
    path = `M ${startX} ${startY} C ${startX} ${startY + bend}, ${endX} ${endY - bend}, ${endX} ${endY}`;
    labelX = (startX + endX) / 2;
    labelY = (startY + endY) / 2 - 8;
  }
  return `<path class="graph-edge ${edge.branch ? 'branch' : ''}" d="${path}" marker-end="url(#arrow-${edge.branch ? 'branch' : 'solid'})"></path><text class="edge-label" x="${labelX}" y="${labelY}" text-anchor="middle">${escapeHTML(edge.label)}</text>`;
}

function graphNodeHTML(dealState, position, width, height, selectedState, currentState, initialState) {
  const classes = ['graph-node'];
  if (dealState.name === selectedState) classes.push('selected');
  if (dealState.name === currentState) classes.push('current');
  const detail = dealState.name === currentState
    ? 'CURRENT STATE'
    : dealState.name === initialState
      ? 'INITIAL STATE'
      : dealState.postCondition ? 'TRANSITION STATE' : 'TERMINAL STATE';
  return `
    <g class="${classes.join(' ')}" data-state-name="${escapeHTML(dealState.name)}" transform="translate(${position.x} ${position.y})">
      <rect width="${width}" height="${height}" rx="13"></rect>
      <text x="18" y="34">${escapeHTML(dealState.name)}</text>
      <text class="node-detail" x="18" y="57">${detail}</text>
    </g>`;
}

function normalizeProcess(process) {
  process.initialStateData ??= {};
  process.states ??= [];
  process.states.forEach((dealState) => {
    dealState.preActions ??= [];
    dealState.postActions ??= [];
    if (dealState.postCondition) {
      dealState.postCondition.decision ??= { key: '', cases: [], elseState: process.initialState };
      dealState.postCondition.decision.cases ??= [];
    }
  });
  return process;
}

function selectedProcessState() {
  return state.process?.states.find((entry) => entry.name === state.selectedStateName);
}

function suggestedCondition(execution) {
  return execution.pendingConditionName || '';
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

function keyValueRowsHTML(values) {
  return Object.entries(values).map(([key, value], index) => `
    <div class="kv-row" data-index="${index}" data-key="${escapeHTML(key)}">
      <input data-kv-key value="${escapeHTML(key)}" placeholder="key" />
      <input data-kv-value value="${escapeHTML(value)}" placeholder="value" />
      <button class="icon-button" data-kv-remove aria-label="Remove">×</button>
    </div>`).join('');
}

function bindKeyValueRows(selector, values, rerender) {
  document.querySelectorAll(`${selector} .kv-row`).forEach((row) => {
    const key = row.dataset.key;
    row.querySelector('[data-kv-key]').addEventListener('change', (event) => {
      const nextKey = event.target.value.trim();
      if (!nextKey || (nextKey !== key && Object.hasOwn(values, nextKey))) {
        showToast('Keys must be non-empty and unique.', true);
        return rerender();
      }
      const value = values[key];
      delete values[key];
      values[nextKey] = value;
      rerender();
    });
    row.querySelector('[data-kv-value]').addEventListener('input', (event) => {
      values[key] = event.target.value;
    });
    row.querySelector('[data-kv-remove]').addEventListener('click', () => {
      delete values[key];
      rerender();
    });
  });
}

function stateOptions(selected) {
  return state.process.states.map((dealState) => `<option value="${escapeHTML(dealState.name)}" ${dealState.name === selected ? 'selected' : ''}>${escapeHTML(dealState.name)}</option>`).join('');
}

function actionOptions(selected) {
  return state.actions.map((action) => `<option value="${escapeHTML(action)}" ${action === selected ? 'selected' : ''}>${escapeHTML(action)}</option>`).join('');
}

function addUniqueKey(target, base, value) {
  let key = base;
  let index = 2;
  while (Object.hasOwn(target, key)) key = `${base}${index++}`;
  target[key] = value;
}

function bindBackLink() {
  document.querySelector('[data-back]').addEventListener('click', (event) => {
    event.preventDefault();
    navigate('/dataset-deal');
  });
}

function dashboardURL() {
  return `/dataset-deal?role=${encodeURIComponent(state.role)}`;
}

function detailURL(kind, id) {
  return `/dataset-deal/${kind}/${encodeURIComponent(id)}?role=${encodeURIComponent(state.role)}`;
}

function buyerLabel(role) {
  return role.replace('buyer', 'Buyer ');
}

function statusClass(status) {
  if (status === 'COMPLETED') return 'completed';
  if (status === 'PROCESSING' || status === 'WAITING') return '';
  return 'terminal';
}

function formatDate(value) {
  return new Date(value).toLocaleString();
}

function renderError(error) {
  app.innerHTML = `<div class="page"><div class="panel panel-body"><h2>Unable to load this page</h2><p>${escapeHTML(error.message)}</p><a class="ghost" href="${dashboardURL()}">Back to dashboard</a></div></div>`;
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
  toast.textContent = message;
  toast.classList.toggle('error', error);
  toast.classList.add('visible');
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => toast.classList.remove('visible'), 3500);
}

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;',
  })[character]);
}

initialize().catch(renderError);
