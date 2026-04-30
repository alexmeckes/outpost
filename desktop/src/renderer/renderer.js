const outpostAPI = window.outpost || createPreviewOutpost();

const state = {
  endpoints: [],
  models: [],
  modelAlias: { name: 'outpost-default', target: '' },
  trace: [],
  processes: {},
  connection: null,
  copyValues: {},
  processLog: '',
};

const els = {
  binaryPath: document.querySelector('#binaryPath'),
  relayState: document.querySelector('#relayState'),
  publishState: document.querySelector('#publishState'),
  refreshButton: document.querySelector('#refreshButton'),
  backendType: document.querySelector('#backendType'),
  backendBaseUrl: document.querySelector('#backendBaseUrl'),
  backendApiKey: document.querySelector('#backendApiKey'),
  backendStatus: document.querySelector('#backendStatus'),
  saveBackendButton: document.querySelector('#saveBackendButton'),
  testBackendButton: document.querySelector('#testBackendButton'),
  refreshModelsButton: document.querySelector('#refreshModelsButton'),
  saveModelAliasButton: document.querySelector('#saveModelAliasButton'),
  modelSelect: document.querySelector('#modelSelect'),
  modelAliasName: document.querySelector('#modelAliasName'),
  testPrompt: document.querySelector('#testPrompt'),
  testInferenceButton: document.querySelector('#testInferenceButton'),
  modelStatus: document.querySelector('#modelStatus'),
  inferenceRows: document.querySelector('#inferenceRows'),
  refreshTraceButton: document.querySelector('#refreshTraceButton'),
  traceAutoRefresh: document.querySelector('#traceAutoRefresh'),
  traceRows: document.querySelector('#traceRows'),
  traceStatus: document.querySelector('#traceStatus'),
  runDiagnosticsButton: document.querySelector('#runDiagnosticsButton'),
  diagnosticsRows: document.querySelector('#diagnosticsRows'),
  deviceId: document.querySelector('#deviceId'),
  registryPath: document.querySelector('#registryPath'),
  endpointSlug: document.querySelector('#endpointSlug'),
  endpointDevice: document.querySelector('#endpointDevice'),
  endpointToken: document.querySelector('#endpointToken'),
  endpointReplace: document.querySelector('#endpointReplace'),
  endpointTokenOutput: document.querySelector('#endpointTokenOutput'),
  endpointRows: document.querySelector('#endpointRows'),
  createEndpointButton: document.querySelector('#createEndpointButton'),
  relayListen: document.querySelector('#relayListen'),
  relayToken: document.querySelector('#relayToken'),
  startRelayButton: document.querySelector('#startRelayButton'),
  stopRelayButton: document.querySelector('#stopRelayButton'),
  startEverythingButton: document.querySelector('#startEverythingButton'),
  testConnectionButton: document.querySelector('#testConnectionButton'),
  connectionBaseUrl: document.querySelector('#connectionBaseUrl'),
  connectionApiKey: document.querySelector('#connectionApiKey'),
  connectionAuthorization: document.querySelector('#connectionAuthorization'),
  connectionRelayHeaderRow: document.querySelector('#connectionRelayHeaderRow'),
  connectionRelayHeader: document.querySelector('#connectionRelayHeader'),
  connectionWarning: document.querySelector('#connectionWarning'),
  connectionStatus: document.querySelector('#connectionStatus'),
  copyButtons: document.querySelectorAll('[data-copy-key]'),
  relayProfile: document.querySelector('#relayProfile'),
  publishRelayUrl: document.querySelector('#publishRelayUrl'),
  publishSlug: document.querySelector('#publishSlug'),
  publishRelayToken: document.querySelector('#publishRelayToken'),
  publicRelayToken: document.querySelector('#publicRelayToken'),
  publicAuthHeader: document.querySelector('#publicAuthHeader'),
  publishApiKey: document.querySelector('#publishApiKey'),
  prepareHostedRelayButton: document.querySelector('#prepareHostedRelayButton'),
  hostedBundleStatus: document.querySelector('#hostedBundleStatus'),
  hostedFields: document.querySelectorAll('.hosted-field'),
  startPublishButton: document.querySelector('#startPublishButton'),
  stopPublishButton: document.querySelector('#stopPublishButton'),
  stopEverythingButton: document.querySelector('#stopEverythingButton'),
  statusOutput: document.querySelector('#statusOutput'),
  processLog: document.querySelector('#processLog'),
  clearLogButton: document.querySelector('#clearLogButton'),
};

outpostAPI.onLog(({ name, message }) => {
  appendLog(`[${name}] ${message}`);
});

outpostAPI.onState((processes) => {
  renderProcessState(processes);
});

els.refreshButton.addEventListener('click', refreshAll);
els.backendType.addEventListener('change', applyBackendPreset);
els.saveBackendButton.addEventListener('click', saveBackend);
els.testBackendButton.addEventListener('click', testBackend);
els.refreshModelsButton.addEventListener('click', () => refreshModels());
els.saveModelAliasButton.addEventListener('click', saveModelAlias);
els.testInferenceButton.addEventListener('click', testInference);
els.refreshTraceButton.addEventListener('click', () => refreshTrace());
els.modelSelect.addEventListener('change', () => {
  renderModelStatus(selectedModel() ? `Selected ${selectedModel()}` : 'No model selected', 'neutral');
  renderInferenceRows([]);
});
els.runDiagnosticsButton.addEventListener('click', refreshDiagnostics);
els.diagnosticsRows.addEventListener('click', handleDiagnosticAction);
els.createEndpointButton.addEventListener('click', createEndpoint);
els.startEverythingButton.addEventListener('click', startEverything);
els.stopEverythingButton.addEventListener('click', stopEverything);
els.testConnectionButton.addEventListener('click', testConnection);
els.startRelayButton.addEventListener('click', startRelay);
els.stopRelayButton.addEventListener('click', () => stopProcess('relay'));
els.startPublishButton.addEventListener('click', startPublish);
els.stopPublishButton.addEventListener('click', () => stopProcess('publish'));
els.clearLogButton.addEventListener('click', () => {
  state.processLog = '';
  els.processLog.textContent = '';
});
els.copyButtons.forEach((button) => {
  button.addEventListener('click', () => copyConfigValue(button));
});
els.relayProfile.addEventListener('change', applyRelayProfile);
els.relayListen.addEventListener('input', syncRelayURL);
els.relayToken.addEventListener('input', syncRelayToken);
els.prepareHostedRelayButton.addEventListener('click', prepareHostedRelay);

renderConnection(null);
refreshAll();
setInterval(() => {
  if (els.traceAutoRefresh.checked) {
    refreshTrace({ silent: true });
  }
}, 2500);

async function refreshAll() {
  await refresh();
  await refreshModels({ silent: true });
  await refreshTrace({ silent: true });
  await refreshDiagnostics();
}

async function refresh() {
  setBusy(true);
  try {
    const overview = await outpostAPI.overview();
    state.endpoints = overview.endpoints || [];
    els.binaryPath.textContent = overview.binary;
    els.deviceId.textContent = overview.identity || '-';
    els.registryPath.textContent = overview.registryPath || '-';
    els.statusOutput.textContent = overview.status || '';
    renderBackend(overview.backend || {});
    renderModelAlias(overview.modelAlias || {});
    renderRelaySettings(overview.relaySettings || {});
    if (overview.connection && !state.connection) {
      renderConnection(overview.connection);
    }
    renderEndpoints();
    renderProcessState(overview.processes || {});
    if (overview.errors?.length) {
      appendLog(overview.errors.join('\n'));
    }
  } finally {
    setBusy(false);
  }
}

async function saveBackend() {
  setBackendBusy(true);
  renderBackendStatus('Saving', 'neutral');
  try {
    const result = await outpostAPI.saveBackend(readBackendForm());
    appendCommandResult(result);
    if (result.backend) {
      renderBackend(result.backend);
    }
    renderBackendStatus('Saved', 'ok');
    await refreshDiagnostics();
  } catch (error) {
    renderBackendStatus(error.message, 'fail');
  } finally {
    setBackendBusy(false);
  }
}

async function testBackend() {
  setBackendBusy(true);
  renderBackendStatus('Testing', 'neutral');
  try {
    const result = await outpostAPI.testBackend(readBackendForm());
    renderBackendStatus(result.detail || (result.ok ? 'Reachable' : 'Not reachable'), result.ok ? 'ok' : 'fail');
    if (result.ok) {
      await refreshModels({ silent: true });
    }
  } catch (error) {
    renderBackendStatus(error.message, 'fail');
  } finally {
    setBackendBusy(false);
  }
}

async function refreshModels(options = {}) {
  setModelsBusy(true);
  if (!options.silent) {
    renderModelStatus('Loading models', 'neutral');
  }
  try {
    const result = await outpostAPI.listModels(readBackendForm());
    state.models = result.models || [];
    renderModelSelect();
    renderModelStatus(result.detail || (result.ok ? 'Models loaded' : 'No models found'), result.ok ? 'ok' : 'fail');
  } catch (error) {
    state.models = [];
    renderModelSelect();
    renderModelStatus(error.message, 'fail');
  } finally {
    setModelsBusy(false);
  }
}

async function saveModelAlias() {
  const model = selectedModel();
  if (!model) {
    renderModelStatus('Choose a model first', 'fail');
    return;
  }

  setModelsBusy(true);
  renderModelStatus('Saving alias', 'neutral');
  try {
    const result = await outpostAPI.saveDefaultModel({ model });
    appendCommandResult(result);
    if (result.ok) {
      renderModelAlias({ name: result.alias, target: result.model });
      if (result.processes) {
        renderProcessState(result.processes);
      }
      const restartDetail = result.restarted?.length ? `, restarted ${result.restarted.join(' and ')}` : '';
      renderModelStatus(`${result.alias} -> ${result.model}${restartDetail}`, 'ok');
      await refreshDiagnostics();
    } else {
      renderModelStatus(result.stderr || 'Could not save alias', 'fail');
    }
  } catch (error) {
    renderModelStatus(error.message, 'fail');
  } finally {
    setModelsBusy(false);
  }
}

async function testInference() {
  const model = modelForInference();
  if (!model) {
    renderModelStatus('Choose a model first', 'fail');
    return;
  }

  setModelsBusy(true);
  renderModelStatus('Testing prompt', 'neutral');
  renderInferenceRows([{ target: 'Prompt test', status: 'neutral', detail: 'Running...' }]);
  try {
    const result = await outpostAPI.testInference({
      model,
      prompt: els.testPrompt.value,
      connection: state.connection,
    });
    renderModelStatus(result.ok ? `Prompt passed with ${result.model}` : `Prompt failed with ${result.model || model}`, result.ok ? 'ok' : 'fail');
    renderInferenceRows(result.results || []);
    await refreshTrace({ silent: true });
  } catch (error) {
    renderModelStatus(error.message, 'fail');
    renderInferenceRows([{ target: 'Prompt test', status: 'fail', detail: error.message }]);
  } finally {
    setModelsBusy(false);
  }
}

async function refreshTrace(options = {}) {
  if (!options.silent) {
    renderTraceStatus('Refreshing', 'neutral');
  }
  try {
    const result = await outpostAPI.listRequests({ limit: 12 });
    state.trace = result.entries || [];
    renderTraceRows(state.trace);
    renderTraceStatus(result.detail || 'No requests yet', result.ok ? 'neutral' : 'fail');
  } catch (error) {
    state.trace = [];
    renderTraceRows([]);
    renderTraceStatus(error.message, 'fail');
  }
}

async function refreshDiagnostics() {
  setDiagnosticsBusy(true);
  try {
    const result = await outpostAPI.diagnostics({
      relaySettings: readRelayForm(),
    });
    renderDiagnostics(result.checks || []);
  } catch (error) {
    renderDiagnostics([
      {
        id: 'diagnostics',
        label: 'Diagnostics',
        status: 'fail',
        detail: error.message,
      },
    ]);
  } finally {
    setDiagnosticsBusy(false);
  }
}

function readBackendForm() {
  return {
    type: els.backendType.value,
    base_url: els.backendBaseUrl.value.trim(),
    api_key: els.backendApiKey.value.trim(),
  };
}

function readRelayForm() {
  const relayToken = els.relayToken.value.trim();
  return {
    profile: els.relayProfile.value,
    listen: els.relayListen.value.trim(),
    relayURL: els.publishRelayUrl.value.trim(),
    relayToken,
    publishRelayToken: els.publishRelayToken.value.trim() || relayToken,
    slug: els.publishSlug.value || els.endpointSlug.value,
    publicRelayToken: els.publicRelayToken.value.trim(),
    publicAuthHeader: els.publicAuthHeader.value.trim(),
  };
}

function renderBackend(backend) {
  const type = backend.type || 'ollama';
  els.backendType.value = backendTypeKnown(type) ? type : 'openai-compatible';
  els.backendBaseUrl.value = backend.base_url || backendDefaultURL(els.backendType.value);
  els.backendApiKey.value = backend.api_key || '';
}

function renderRelaySettings(settings) {
  const profile = settings.profile === 'hosted' ? 'hosted' : 'local';
  els.relayProfile.value = profile;
  els.relayListen.value = settings.listen || '127.0.0.1:8787';
  els.relayToken.value = settings.relayToken || settings.publishRelayToken || '';
  els.publishRelayToken.value = settings.publishRelayToken || settings.relayToken || '';
  els.publishRelayUrl.value = settings.relayURL || `http://${els.relayListen.value}`;
  els.publishSlug.value = settings.slug || els.publishSlug.value || 'demo';
  els.publicRelayToken.value = settings.publicRelayToken || '';
  els.publicAuthHeader.value = settings.publicAuthHeader || 'X-Outpost-Relay-Token';
  applyRelayProfile();
}

function renderModelAlias(alias) {
  state.modelAlias = {
    name: alias.name || 'outpost-default',
    target: alias.target || '',
  };
  els.modelAliasName.value = state.modelAlias.name;
  renderModelSelect();
}

function renderModelSelect() {
  const previous = els.modelSelect.value;
  const preferred = state.modelAlias.target || previous;
  els.modelSelect.innerHTML = '';

  if (!state.models.length) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No models loaded';
    els.modelSelect.appendChild(option);
    els.modelSelect.disabled = true;
    els.saveModelAliasButton.disabled = true;
    els.testInferenceButton.disabled = true;
    return;
  }

  for (const model of state.models) {
    const option = document.createElement('option');
    option.value = model.id;
    option.textContent = model.ownedBy ? `${model.id} (${model.ownedBy})` : model.id;
    els.modelSelect.appendChild(option);
  }

  const values = state.models.map((model) => model.id);
  els.modelSelect.value = values.includes(preferred) ? preferred : values[0];
  els.modelSelect.disabled = false;
  els.saveModelAliasButton.disabled = false;
  els.testInferenceButton.disabled = false;
}

function applyBackendPreset() {
  els.backendBaseUrl.value = backendDefaultURL(els.backendType.value);
  els.backendApiKey.value = '';
  renderBackendStatus('Not checked', 'neutral');
  state.models = [];
  renderModelSelect();
  renderModelStatus('Not loaded', 'neutral');
  renderInferenceRows([]);
}

function applyRelayProfile() {
  const local = els.relayProfile.value !== 'hosted';
  els.relayListen.disabled = !local;
  els.publishRelayUrl.readOnly = local;
  els.prepareHostedRelayButton.disabled = local;
  els.hostedFields.forEach((field) => {
    field.hidden = local;
  });
  if (local) {
    syncRelayURL();
  } else if (!els.publishRelayUrl.value || els.publishRelayUrl.value === `http://${els.relayListen.value}`) {
    els.publishRelayUrl.value = 'https://your-relay.example.com';
  }
  syncRelayToken();
  renderProcessState(state.processes || {});
}

function syncRelayURL() {
  if (els.relayProfile.value !== 'hosted') {
    els.publishRelayUrl.value = `http://${els.relayListen.value || '127.0.0.1:8787'}`;
  }
}

function syncRelayToken() {
  els.publishRelayToken.value = els.relayToken.value;
}

async function createEndpoint() {
  setBusy(true);
  try {
    const result = await outpostAPI.createEndpoint({
      slug: els.endpointSlug.value,
      device: els.endpointDevice.value,
      publicToken: els.endpointToken.value,
      replace: els.endpointReplace.checked,
    });
    appendCommandResult(result);
    if (result.token) {
      els.endpointTokenOutput.textContent = `${els.endpointSlug.value}: ${result.token}`;
    }
    await refresh();
  } finally {
    setBusy(false);
  }
}

async function revokeEndpoint(slug) {
  setBusy(true);
  try {
    const result = await outpostAPI.revokeEndpoint(slug);
    appendCommandResult(result);
    await refresh();
  } finally {
    setBusy(false);
  }
}

async function startRelay() {
  const result = await outpostAPI.startRelay(readRelayForm());
  appendCommandResult(result);
  if (result.relaySettings) {
    renderRelaySettings(result.relaySettings);
  }
  renderProcessState(result.processes || {});
  await refreshDiagnostics();
}

async function startPublish() {
  const result = await outpostAPI.startPublish({
    ...readRelayForm(),
    apiKey: els.publishApiKey.value.trim(),
  });
  appendCommandResult(result);
  if (result.relaySettings) {
    renderRelaySettings(result.relaySettings);
  }
  renderProcessState(result.processes || {});
  await refreshDiagnostics();
}

async function startEverything() {
  setBusy(true);
  try {
    const result = await outpostAPI.startHappyPath({
      ...readRelayForm(),
      apiKey: els.publishApiKey.value.trim(),
      publicToken: els.endpointToken.value,
      replace: els.endpointReplace.checked,
    });
    appendCommandResult(result);
    if (result.relaySettings) {
      renderRelaySettings(result.relaySettings);
    }
    if (result.token) {
      els.endpointTokenOutput.textContent = `${els.publishSlug.value || els.endpointSlug.value}: ${result.token}`;
    }
    renderConnection(result.connection || null);
    renderProcessState(result.processes || {});
    await refresh();
    await refreshDiagnostics();
  } finally {
    setBusy(false);
  }
}

async function prepareHostedRelay() {
  setBusy(true);
  els.prepareHostedRelayButton.disabled = true;
  renderHostedStatus('Preparing hosted relay', 'neutral');
  try {
    const result = await outpostAPI.prepareHostedRelay(readRelayForm());
    appendCommandResult(result);
    if (result.relaySettings) {
      renderRelaySettings(result.relaySettings);
    }
    renderHostedStatus(result.hostedRelay?.bundle_dir || 'Prepared hosted relay', result.ok ? 'ok' : 'fail');
    await refreshDiagnostics();
  } catch (error) {
    renderHostedStatus(error.message, 'fail');
  } finally {
    els.prepareHostedRelayButton.disabled = false;
    setBusy(false);
  }
}

async function stopEverything() {
  setBusy(true);
  try {
    const processes = await outpostAPI.stopAll();
    renderProcessState(processes || {});
    await refreshDiagnostics();
  } finally {
    setBusy(false);
  }
}

async function testConnection() {
  if (!state.connection) return;
  renderTestStatus('Testing', 'neutral');
  els.testConnectionButton.disabled = true;
  try {
    const result = await outpostAPI.testConnection(state.connection);
    const detail = result.detail ? ` - ${result.detail}` : '';
    renderTestStatus(`${result.ok ? 'Passed' : 'Failed'}${result.status ? ` (${result.status})` : ''}${detail}`, result.ok ? 'ok' : 'fail');
    await refreshTrace({ silent: true });
  } finally {
    els.testConnectionButton.disabled = !state.connection;
  }
}

async function stopProcess(name) {
  const processes = await outpostAPI.stopProcess(name);
  renderProcessState(processes || {});
  await refreshDiagnostics();
}

async function handleDiagnosticAction(event) {
  const button = event.target.closest('[data-diagnostic-action]');
  if (!button) return;

  if (button.dataset.diagnosticAction === 'start-api') {
    button.disabled = true;
    const result = await outpostAPI.startAPI();
    appendCommandResult(result);
    renderProcessState(result.processes || {});
    await refreshDiagnostics();
  }
}

function renderEndpoints() {
  els.endpointRows.innerHTML = '';
  if (!state.endpoints.length) {
    const row = document.createElement('tr');
    row.innerHTML = '<td colspan="4">No endpoints</td>';
    els.endpointRows.appendChild(row);
    return;
  }

  for (const endpoint of state.endpoints) {
    const row = document.createElement('tr');
    const token = endpoint.tokenPrefix && endpoint.tokenPrefix !== '-' ? endpoint.tokenPrefix : 'none';
    row.innerHTML = `
      <td>${escapeHTML(endpoint.slug)}</td>
      <td>${escapeHTML(token)}</td>
      <td>${escapeHTML(endpoint.device)}</td>
      <td><button class="mini-button" type="button">Revoke</button></td>
    `;
    row.querySelector('button').addEventListener('click', () => revokeEndpoint(endpoint.slug));
    els.endpointRows.appendChild(row);
  }
}

function renderDiagnostics(checks) {
  els.diagnosticsRows.innerHTML = '';
  if (!checks.length) {
    const row = document.createElement('div');
    row.className = 'diagnostic-row';
    row.innerHTML = `
      <span class="status-badge neutral">Pending</span>
      <div>
        <strong>Diagnostics</strong>
        <p>Not checked yet</p>
      </div>
    `;
    els.diagnosticsRows.appendChild(row);
    return;
  }

  for (const check of checks) {
    const row = document.createElement('div');
    const action = check.action
      ? `<button type="button" class="quiet" data-diagnostic-action="${escapeHTML(check.action.type)}">${escapeHTML(check.action.label)}</button>`
      : '';
    row.className = 'diagnostic-row';
    row.innerHTML = `
      <span class="status-badge ${escapeHTML(check.status)}">${statusLabel(check.status)}</span>
      <div>
        <strong>${escapeHTML(check.label)}</strong>
        <p>${escapeHTML(check.detail)}</p>
      </div>
      ${action}
    `;
    els.diagnosticsRows.appendChild(row);
  }
}

function renderProcessState(processes) {
  processes = processes || {};
  state.processes = processes || {};
  const relayActive = Boolean(processes.relay || processes.relayDetected);
  const relayOwned = Boolean(processes.relay);
  const publishActive = Boolean(processes.publish || processes.publishDetected);
  const publishOwned = Boolean(processes.publish);
  const hostedRelay = els.relayProfile.value === 'hosted';

  setPill(els.relayState, relayActive, processes.relay ? 'Relay running' : 'Relay detected', 'Relay stopped');
  setPill(els.publishState, publishActive, 'Publish running', 'Publish stopped');
  els.startRelayButton.disabled = relayActive || hostedRelay;
  els.stopRelayButton.disabled = !processes.relay;
  els.startPublishButton.disabled = publishActive;
  els.stopPublishButton.disabled = !publishOwned;
  els.startEverythingButton.disabled = Boolean(publishActive && (relayActive || hostedRelay));
  els.stopEverythingButton.disabled = !relayOwned && !publishOwned && !processes.api;
}

function renderConnection(connection) {
  state.connection = connection;
  state.copyValues = {};

  if (!connection) {
    els.connectionBaseUrl.textContent = 'Not ready';
    els.connectionApiKey.textContent = 'Not ready';
    els.connectionAuthorization.textContent = 'Not ready';
    els.connectionRelayHeaderRow.hidden = true;
    els.connectionRelayHeader.textContent = 'Not ready';
    els.connectionWarning.hidden = true;
    els.connectionWarning.textContent = '';
    els.copyButtons.forEach((button) => {
      button.disabled = true;
      button.textContent = 'Copy';
    });
    els.testConnectionButton.disabled = true;
    renderTestStatus('Not ready', 'neutral');
    return;
  }

  state.copyValues = {
    baseURL: connection.baseURL,
    apiKey: connection.apiKey,
    authorizationHeader: connection.authorizationHeader,
    publicRelayHeader: connection.publicRelayHeader,
  };

  els.connectionBaseUrl.textContent = connection.baseURL;
  els.connectionApiKey.textContent = connection.apiKey;
  els.connectionAuthorization.textContent = connection.authorizationHeader;
  els.connectionRelayHeaderRow.hidden = !connection.publicRelayHeader;
  els.connectionRelayHeader.textContent = connection.publicRelayHeader || 'Not required';
  els.connectionWarning.hidden = !connection.publicTokenWarning;
  els.connectionWarning.textContent = connection.publicTokenWarning || '';
  els.copyButtons.forEach((button) => {
    button.disabled = !state.copyValues[button.dataset.copyKey];
    button.textContent = 'Copy';
  });
  els.testConnectionButton.disabled = false;
  renderTestStatus('Ready', 'neutral');
}

function renderTestStatus(message, tone) {
  els.connectionStatus.textContent = message;
  els.connectionStatus.className = `test-status ${tone || 'neutral'}`;
}

function renderBackendStatus(message, tone) {
  els.backendStatus.textContent = message;
  els.backendStatus.className = `test-status ${tone || 'neutral'}`;
}

function renderModelStatus(message, tone) {
  els.modelStatus.textContent = message;
  els.modelStatus.className = `test-status ${tone || 'neutral'}`;
}

function renderHostedStatus(message, tone) {
  els.hostedBundleStatus.textContent = message;
  els.hostedBundleStatus.className = `test-status ${tone || 'neutral'}`;
}

function renderInferenceRows(results) {
  els.inferenceRows.innerHTML = '';
  for (const result of results) {
    const row = document.createElement('div');
    row.className = 'inference-row';
    row.innerHTML = `
      <span class="status-badge ${escapeHTML(result.status)}">${statusLabel(result.status)}</span>
      <div>
        <strong>${escapeHTML(result.target || 'Test')}</strong>
        <p>${escapeHTML(result.detail || '')}</p>
      </div>
    `;
    els.inferenceRows.appendChild(row);
  }
}

function renderTraceStatus(message, tone) {
  els.traceStatus.textContent = message;
  els.traceStatus.className = `test-status ${tone || 'neutral'}`;
}

function renderTraceRows(entries) {
  els.traceRows.innerHTML = '';
  if (!entries.length) {
    const row = document.createElement('div');
    row.className = 'trace-row';
    row.innerHTML = `
      <span class="status-badge neutral">...</span>
      <span class="trace-time">--:--:--</span>
      <div class="trace-main">
        <strong>No requests</strong>
        <p>Prompt tests and client calls will appear here.</p>
      </div>
      <span class="trace-duration">-</span>
    `;
    els.traceRows.appendChild(row);
    return;
  }

  for (const entry of entries) {
    const row = document.createElement('div');
    row.className = 'trace-row';
    const tone = statusTone(entry.status);
    const detailParts = [
      entry.model,
      entry.backend,
      entry.bytes ? `${entry.bytes} B` : '',
      entry.error,
    ].filter(Boolean);
    row.innerHTML = `
      <span class="status-badge ${tone}">${entry.status || '-'}</span>
      <span class="trace-time">${escapeHTML(formatTraceTime(entry.time))}</span>
      <div class="trace-main">
        <strong>${escapeHTML(`${entry.method || '-'} ${entry.path || '-'}`)}</strong>
        <p>${escapeHTML(detailParts.join(' - ') || 'No details')}</p>
      </div>
      <span class="trace-duration">${escapeHTML(entry.duration || '-')}</span>
    `;
    els.traceRows.appendChild(row);
  }
}

function setPill(el, running, yes, no) {
  el.textContent = running ? yes : no;
  el.classList.toggle('running', Boolean(running));
}

function appendCommandResult(result) {
  if (!result) return;
  if (result.error) appendLog(`${result.error}\n`);
  if (result.stdout) appendLog(result.stdout);
  if (result.stderr) appendLog(result.stderr);
}

function appendLog(message) {
  state.processLog += message.endsWith('\n') ? message : `${message}\n`;
  els.processLog.textContent = state.processLog;
  els.processLog.scrollTop = els.processLog.scrollHeight;
}

function setBusy(busy) {
  els.refreshButton.disabled = busy;
  els.createEndpointButton.disabled = busy;
  els.startEverythingButton.disabled = busy || Boolean(state.processes.relay && state.processes.publish);
  els.stopEverythingButton.disabled = busy || (!state.processes.relay && !state.processes.publish && !state.processes.api);
  els.testConnectionButton.disabled = busy || !state.connection;
  els.prepareHostedRelayButton.disabled = busy || els.relayProfile.value !== 'hosted';
}

function setBackendBusy(busy) {
  els.saveBackendButton.disabled = busy;
  els.testBackendButton.disabled = busy;
}

function setModelsBusy(busy) {
  els.refreshModelsButton.disabled = busy;
  els.saveModelAliasButton.disabled = busy || !selectedModel();
  els.testInferenceButton.disabled = busy || !selectedModel();
  els.modelSelect.disabled = busy || !state.models.length;
}

function setDiagnosticsBusy(busy) {
  els.runDiagnosticsButton.disabled = busy;
  if (busy && !els.diagnosticsRows.children.length) {
    renderDiagnostics([
      {
        id: 'diagnostics',
        label: 'Diagnostics',
        status: 'neutral',
        detail: 'Checking...',
      },
    ]);
  }
}

function selectedModel() {
  return String(els.modelSelect.value || '').trim();
}

function modelForInference() {
  const selected = selectedModel();
  if (state.modelAlias.target && selected === state.modelAlias.target) {
    return state.modelAlias.name;
  }
  return selected || state.modelAlias.name;
}

function statusLabel(status) {
  switch (status) {
    case 'ok':
      return 'OK';
    case 'warn':
      return 'Check';
    case 'fail':
      return 'Fix';
    default:
      return '...';
  }
}

function statusTone(status) {
  if (status >= 500 || status === 0) return 'fail';
  if (status >= 400) return 'warn';
  return 'ok';
}

function formatTraceTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--:--';
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

async function copyConfigValue(button) {
  const value = state.copyValues[button.dataset.copyKey];
  if (!value) return;
  try {
    await outpostAPI.copyText(value);
    button.textContent = 'Copied';
    setTimeout(() => {
      button.textContent = 'Copy';
    }, 1200);
  } catch (error) {
    appendLog(`Copy failed: ${error.message}\n`);
  }
}

function createPreviewOutpost() {
  const preview = {
    processes: { relay: false, publish: false },
    relaySettings: {
      profile: 'local',
      listen: '127.0.0.1:8787',
      relayURL: 'http://127.0.0.1:8787',
      relayToken: 'ort_preview',
      publishRelayToken: 'ort_preview',
      slug: 'demo',
      publicRelayToken: '',
      publicAuthHeader: 'X-Outpost-Relay-Token',
    },
    modelAlias: { name: 'outpost-default', target: 'llama3.2:1b' },
    endpoints: [
      {
        slug: 'demo',
        device: 'macbook-local',
        tokenPrefix: 'opub_7f2c',
      },
    ],
  };

  return {
    overview: async () => ({
      binary: '/path/to/outpost/outpost',
      processes: preview.processes,
      status: 'Outpost API: preview\nBackend: Ollama local\n',
      identity: 'device_local_preview',
      registryPath: '~/Library/Application Support/outpost/relay_endpoints.json',
      backend: {
        type: 'ollama',
        base_url: 'http://127.0.0.1:11434',
      },
      relaySettings: preview.relaySettings,
      modelAlias: preview.modelAlias,
      endpoints: preview.endpoints,
      errors: [],
    }),
    diagnostics: async () => ({
      ok: true,
      checks: [
        { id: 'binary', label: 'Binary', status: 'ok', detail: 'Ready at /path/to/outpost/outpost' },
        { id: 'api', label: 'Local API', status: 'warn', detail: 'Not running. It can be started now or by Publish.', action: { type: 'start-api', label: 'Start' } },
        { id: 'backend', label: 'Ollama', status: 'ok', detail: 'Reachable at http://127.0.0.1:11434, version 0.12.4' },
        { id: 'models', label: 'Models', status: 'ok', detail: '1 model installed: llama3.2:1b' },
        { id: 'relay-port', label: 'Relay port', status: 'ok', detail: 'Available at 127.0.0.1:8787' },
      ],
    }),
    saveBackend: async (backend) => ({
      ok: true,
      stdout: `Saved backend: ${backend.type} at ${backend.base_url}\n`,
      backend,
    }),
    testBackend: async (backend) => ({
      ok: true,
      status: 'ok',
      detail: `${backendLabel(backend.type)} reachable: 1 model available`,
      backend,
    }),
    listModels: async () => ({
      ok: true,
      detail: '2 models discovered',
      models: [
        { id: 'llama3.2:1b', ownedBy: 'ollama' },
        { id: 'qwen2.5-coder:7b', ownedBy: 'ollama' },
      ],
    }),
    saveDefaultModel: async ({ model }) => {
      preview.modelAlias = { name: 'outpost-default', target: model };
      return {
        ok: true,
        stdout: `Saved model alias: outpost-default -> ${model}\n`,
        alias: 'outpost-default',
        model,
      };
    },
    testInference: async ({ model }) => ({
      ok: true,
      model,
      results: [
        { target: 'Local API', status: 'ok', code: 200, detail: 'outpost' },
        { target: 'Relay URL', status: 'ok', code: 200, detail: 'outpost' },
      ],
    }),
    listRequests: async () => ({
      ok: true,
      detail: '3 recent requests',
      entries: [
        {
          time: new Date().toISOString(),
          method: 'POST',
          path: '/v1/chat/completions',
          model: 'outpost-default -> llama3.2:1b',
          backend: 'http://127.0.0.1:11434',
          status: 200,
          bytes: 196,
          duration: '84ms',
        },
        {
          time: new Date(Date.now() - 8000).toISOString(),
          method: 'GET',
          path: '/v1/models',
          model: '',
          backend: 'http://127.0.0.1:11434',
          status: 200,
          bytes: 94,
          duration: '13ms',
        },
        {
          time: new Date(Date.now() - 22000).toISOString(),
          method: 'GET',
          path: '/v1/models',
          model: '',
          backend: 'http://127.0.0.1:11434',
          status: 401,
          bytes: 0,
          duration: '0s',
          error: 'missing or invalid bearer token',
        },
      ],
    }),
    createEndpoint: async (input) => {
      const slug = input.slug || 'demo';
      preview.endpoints = preview.endpoints.filter((endpoint) => endpoint.slug !== slug);
      preview.endpoints.push({
        slug,
        device: input.device || 'local',
        tokenPrefix: input.publicToken === 'none' ? '-' : 'opub_preview',
      });
      return {
        ok: true,
        stdout: `Created relay endpoint: ${slug}\n`,
        stderr: '',
        token: input.publicToken === 'none' ? '' : 'opub_preview_token',
        processes: preview.processes,
      };
    },
    revokeEndpoint: async (slug) => {
      preview.endpoints = preview.endpoints.filter((endpoint) => endpoint.slug !== slug);
      return { ok: true, stdout: `Revoked relay endpoint: ${slug}\n`, stderr: '' };
    },
    startRelay: async () => {
      preview.processes = { ...preview.processes, relay: true };
      return { ok: true, stdout: 'Relay listening on 127.0.0.1:8787\n', relaySettings: preview.relaySettings, processes: preview.processes };
    },
    startAPI: async () => {
      preview.processes = { ...preview.processes, api: true };
      return { ok: true, stdout: 'Outpost API listening on 127.0.0.1:7341\n', processes: preview.processes };
    },
    startPublish: async (input = {}) => {
      preview.relaySettings = { ...preview.relaySettings, ...input };
      preview.processes = { ...preview.processes, publish: true };
      return { ok: true, stdout: 'Outpost publish is running\nOpenAI base URL: http://127.0.0.1:8787/demo/v1\n', relaySettings: preview.relaySettings, processes: preview.processes };
    },
    startHappyPath: async (input) => {
      preview.relaySettings = { ...preview.relaySettings, ...input };
      preview.processes = {
        relay: preview.relaySettings.profile === 'hosted' ? false : true,
        publish: true,
      };
      const slug = input.slug || 'demo';
      const baseURL = `${input.relayURL || 'http://127.0.0.1:8787'}/${slug}/v1`;
      const apiKey = input.apiKey || 'op_live_preview';
      const publicRelayToken = input.publicRelayToken || 'opub_preview_token';
      const publicRelayHeader = `X-Outpost-Relay-Token: Bearer ${publicRelayToken}`;
      return {
        ok: true,
        stdout: `Using endpoint ${slug}.\nCreated API key for ${slug}.\n`,
        stderr: '',
        token: publicRelayToken,
        apiKey,
        relaySettings: preview.relaySettings,
        connection: {
          slug,
          baseURL,
          apiKey,
          authorizationHeader: `Authorization: Bearer ${apiKey}`,
          publicRelayHeaderName: 'X-Outpost-Relay-Token',
          publicRelayToken,
          publicRelayHeader,
          publicTokenWarning: '',
        },
        processes: preview.processes,
      };
    },
    prepareHostedRelay: async (input) => {
      preview.relaySettings = {
        ...preview.relaySettings,
        ...input,
        profile: 'hosted',
        relayURL: input.relayURL || 'https://your-relay.example.com',
        relayToken: input.relayToken || 'ort_preview_hosted',
        publishRelayToken: input.relayToken || 'ort_preview_hosted',
        publicRelayToken: input.publicRelayToken || 'orp_preview_public',
        publicAuthHeader: input.publicAuthHeader || 'X-Outpost-Relay-Token',
      };
      return {
        ok: true,
        stdout: 'Prepared hosted relay bundle: ~/Library/Application Support/outpost-desktop/hosted-relay\n',
        hostedRelay: {
          bundle_dir: '~/Library/Application Support/outpost-desktop/hosted-relay',
          relay_url: preview.relaySettings.relayURL,
          slug: preview.relaySettings.slug,
          agent_token: preview.relaySettings.relayToken,
          public_token: preview.relaySettings.publicRelayToken,
          public_auth_header: preview.relaySettings.publicAuthHeader,
        },
        relaySettings: preview.relaySettings,
        processes: preview.processes,
      };
    },
    testConnection: async () => ({
      ok: true,
      status: 200,
      detail: '2 models available',
      url: 'http://127.0.0.1:8787/demo/v1/models',
    }),
    stopProcess: async (name) => {
      preview.processes = { ...preview.processes, [name]: false };
      return preview.processes;
    },
    stopAll: async () => {
      preview.processes = { relay: false, publish: false, api: false };
      return preview.processes;
    },
    processState: async () => preview.processes,
    copyText: async (text) => {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(String(text ?? ''));
      }
    },
    onLog: () => () => {},
    onState: () => () => {},
  };
}

function backendTypeKnown(type) {
  return ['ollama', 'lmstudio', 'openai-compatible'].includes(type);
}

function backendDefaultURL(type) {
  if (type === 'lmstudio') return 'http://127.0.0.1:1234/v1';
  if (type === 'openai-compatible') return 'http://127.0.0.1:1234/v1';
  return 'http://127.0.0.1:11434';
}

function backendLabel(type) {
  if (type === 'lmstudio') return 'LM Studio';
  if (type === 'openai-compatible') return 'Backend';
  return 'Ollama';
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}
