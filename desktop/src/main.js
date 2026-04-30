const { app, BrowserWindow, Menu, Tray, ipcMain, nativeImage } = require('electron');
const crypto = require('node:crypto');
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFile, spawn } = require('node:child_process');
const net = require('node:net');

const devRoot = path.resolve(__dirname, '..', '..');
const outpostBin = app.isPackaged ? path.join(process.resourcesPath, 'outpost') : path.join(devRoot, 'outpost');
const outpostCwd = app.isPackaged ? process.resourcesPath : devRoot;
const relaySourceDir = app.isPackaged ? path.join(process.resourcesPath, 'relay-source') : devRoot;
const defaultRelayListen = '127.0.0.1:8787';
const defaultRelayURL = `http://${defaultRelayListen}`;
const defaultRelayPublicAuthHeader = 'X-Outpost-Relay-Token';
const defaultSlug = 'demo';
const defaultModelAlias = 'outpost-default';
const defaultInferencePrompt = 'Reply with the single word: outpost';

let mainWindow;
let tray;
let isQuitting = false;
const processes = {
  api: null,
  relay: null,
  publish: null,
};

function createWindow() {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.show();
    mainWindow.focus();
    return;
  }

  mainWindow = new BrowserWindow({
    width: 920,
    height: 760,
    minWidth: 760,
    minHeight: 620,
    title: 'Outpost',
    backgroundColor: '#f4f6f3',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  mainWindow.on('close', (event) => {
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));
}

app.whenReady().then(() => {
  app.setName('Outpost');
  registerIPC();
  createAppMenu();
  createTray();
  createWindow();

  app.on('activate', () => {
    createWindow();
  });
});

app.on('before-quit', () => {
  isQuitting = true;
  stopProcess('publish');
  stopProcess('relay');
  stopProcess('api');
});

app.on('window-all-closed', () => {
  if (!isQuitting && process.platform !== 'darwin') {
    createWindow();
  }
});

function registerIPC() {
  ipcMain.handle('outpost:overview', async () => {
    const status = await runOutpost(['status']);
    const [identity, registryPath, endpoints, config, desktopState] = await Promise.all([
      runOutpost(['relay', 'identity']),
      runOutpost(['relay', 'endpoint', 'path']),
      runOutpost(['relay', 'endpoint', 'list']),
      readConfig(),
      readDesktopState(),
    ]);
    const relaySettings = await ensureRelaySettings(desktopState);
    const processes = await detectedProcessState(relaySettings, config);

    return {
      binary: outpostBin,
      processes,
      status: status.stdout || status.stderr,
      identity: identity.stdout.trim(),
      registryPath: registryPath.stdout.trim(),
      endpoints: parseEndpointList(endpoints.stdout),
      backend: config.backend,
      modelAlias: {
        name: defaultModelAlias,
        target: config.model_aliases?.[defaultModelAlias] || '',
      },
      relaySettings,
      connection: desktopState.connection || null,
      errors: [status, identity, registryPath, endpoints]
        .filter((result) => !result.ok)
        .map((result) => result.stderr || result.stdout),
    };
  });

  ipcMain.handle('outpost:diagnostics', async (_event, input) => {
    return runDiagnostics(input || {});
  });

  ipcMain.handle('outpost:api-start', async () => {
    return startAPI();
  });

  ipcMain.handle('outpost:backend-save', async (_event, backend) => {
    return saveBackend(backend || {});
  });

  ipcMain.handle('outpost:backend-test', async (_event, backend) => {
    return testBackend(backend || {});
  });

  ipcMain.handle('outpost:models-list', async (_event, backend) => {
    return listModels(backend || {});
  });

  ipcMain.handle('outpost:model-default-save', async (_event, input) => {
    return saveDefaultModel(input || {});
  });

  ipcMain.handle('outpost:inference-test', async (_event, input) => {
    return testInference(input || {});
  });

  ipcMain.handle('outpost:requests-list', async (_event, input) => {
    return listRequestLogs(input || {});
  });

  ipcMain.handle('outpost:endpoint-create', async (_event, input) => {
    const args = ['relay', 'endpoint', 'create', input.slug || 'demo'];
    args.push('--device', input.device || 'local');
    args.push('--public-token', input.publicToken || 'auto');
    if (input.replace) {
      args.push('--replace');
    }
    const result = await runOutpost(args);
    return { ...result, token: extractPublicToken(result.stdout) };
  });

  ipcMain.handle('outpost:endpoint-revoke', async (_event, slug) => {
    return runOutpost(['relay', 'endpoint', 'revoke', slug]);
  });

  ipcMain.handle('outpost:relay-start', async (_event, input) => {
    return startRelay(input || {});
  });

  ipcMain.handle('outpost:publish-start', async (_event, input) => {
    return startPublish(input || {});
  });

  ipcMain.handle('outpost:happy-path-start', async (_event, input) => {
    return startHappyPath(input || {});
  });

  ipcMain.handle('outpost:hosted-prepare', async (_event, input) => {
    return prepareHostedRelay(input || {});
  });

  ipcMain.handle('outpost:process-stop-all', async () => {
    return stopAllProcesses();
  });

  ipcMain.handle('outpost:connection-test', async (_event, connection) => {
    return testConnection(connection || {});
  });

  ipcMain.handle('outpost:process-stop', async (_event, name) => {
    stopProcess(name);
    return processState();
  });

  ipcMain.handle('outpost:process-state', async () => getRuntimeProcessState());
}

function createAppMenu() {
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      {
        label: 'Outpost',
        submenu: [
          { label: 'Show Outpost', click: createWindow },
          { type: 'separator' },
          { label: 'Quit Outpost', role: 'quit' },
        ],
      },
      {
        label: 'Edit',
        submenu: [
          { role: 'undo' },
          { role: 'redo' },
          { type: 'separator' },
          { role: 'cut' },
          { role: 'copy' },
          { role: 'paste' },
          { role: 'selectAll' },
        ],
      },
      {
        label: 'View',
        submenu: [
          { role: 'reload' },
          { role: 'toggleDevTools' },
          { type: 'separator' },
          { role: 'resetZoom' },
          { role: 'zoomIn' },
          { role: 'zoomOut' },
        ],
      },
    ]),
  );
}

function createTray() {
  if (tray) return;

  tray = new Tray(createTrayImage());
  tray.setToolTip('Outpost');
  tray.on('click', createWindow);
  rebuildTrayMenu();
}

function createTrayImage() {
  const assetRoot = app.isPackaged ? process.resourcesPath : path.join(__dirname, '..', 'build');
  const candidates = [
    path.join(assetRoot, 'trayTemplate.png'),
    path.join(assetRoot, 'icon.png'),
  ];

  for (const candidate of candidates) {
    const image = nativeImage.createFromPath(candidate);
    if (!image.isEmpty()) {
      if (process.platform === 'darwin') {
        image.setTemplateImage(candidate.endsWith('Template.png'));
      }
      return image;
    }
  }

  const svg = encodeURIComponent(`
    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 18 18">
      <rect width="18" height="18" rx="4" fill="#17211f"/>
      <path d="M4 9h10M9 4v10" stroke="#f0c36f" stroke-width="2" stroke-linecap="round"/>
      <circle cx="9" cy="9" r="2" fill="#5ec49b"/>
    </svg>
  `);
  return nativeImage.createFromDataURL(`data:image/svg+xml;charset=utf-8,${svg}`);
}

function rebuildTrayMenu() {
  if (!tray) return;

  const state = processState();
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: 'Show Outpost', click: createWindow },
      { type: 'separator' },
      {
        label: state.relay ? 'Relay running' : 'Start relay',
        enabled: !state.relay,
        click: () => startRelay({}),
      },
      {
        label: 'Stop relay',
        enabled: state.relay,
        click: () => stopProcess('relay'),
      },
      {
        label: state.publish ? 'Publish running' : 'Start publish',
        enabled: !state.publish,
        click: () => startPublish({}),
      },
      {
        label: 'Stop publish',
        enabled: state.publish,
        click: () => stopProcess('publish'),
      },
      { type: 'separator' },
      { label: 'Quit Outpost', role: 'quit' },
    ]),
  );
}

function runOutpost(args) {
  return new Promise((resolve) => {
    execFile(outpostBin, args, { cwd: outpostCwd, timeout: 15000 }, (error, stdout, stderr) => {
      resolve({
        ok: !error,
        code: typeof error?.code === 'number' ? error.code : 0,
        stdout,
        stderr,
        args,
      });
    });
  });
}

function startLongProcess(name, args) {
  if (processes[name]) {
    return { ok: false, error: `${name} is already running`, processes: processState() };
  }

  const child = spawn(outpostBin, args, { cwd: outpostCwd });
  child.outpostArgs = [...args];
  processes[name] = child;
  sendLog(name, `> outpost ${args.join(' ')}\n`);

  child.stdout.on('data', (data) => sendLog(name, data.toString()));
  child.stderr.on('data', (data) => sendLog(name, data.toString()));
  child.on('error', (error) => {
    sendLog(name, `${error.message}\n`);
  });
  child.on('exit', (code, signal) => {
    sendLog(name, `\n[${name} exited code=${code ?? '-'} signal=${signal ?? '-'}]\n`);
    processes[name] = null;
    sendState();
  });

  sendState();
  return { ok: true, processes: processState() };
}

function startAPI() {
  return startLongProcess('api', ['start']);
}

async function startRelay(input) {
  const settings = await saveRelaySettings(input);
  if (settings.profile === 'hosted') {
    return {
      ok: true,
      stdout: `Hosted relay selected at ${settings.relayURL}. No local relay server was started.\n`,
      relaySettings: settings,
      processes: processState(),
    };
  }
  const result = startLongProcess('relay', [
    'relay',
    'serve',
    '--listen',
    settings.listen,
    '--token',
    settings.relayToken,
  ]);
  return { ...result, relaySettings: settings };
}

async function startPublish(input) {
  const settings = await saveRelaySettings(input);
  if (!settings.relayURL) {
    return { ok: false, error: 'Relay URL is required before publishing.', processes: processState() };
  }
  const args = [
    'publish',
    '--relay',
    settings.relayURL,
    '--slug',
    settings.slug,
    '--relay-token',
    settings.publishRelayToken,
  ];
  if (input.apiKey) {
    args.push('--api-key', input.apiKey);
  }
  const publicToken = input.publicToken || input.publicRelayToken;
  if (publicToken) {
    args.push('--public-token', publicToken);
  }
  if (input.publicAuthHeader) {
    args.push('--public-auth-header', input.publicAuthHeader);
  }
  const result = startLongProcess('publish', args);
  return { ...result, relaySettings: settings };
}

async function startHappyPath(input) {
  const desktopState = await readDesktopState();
  const relaySettings = await saveRelaySettings({ ...desktopState.relaySettings, ...input });
  const slug = relaySettings.slug;
  const publicTokenMode = input.publicToken || 'auto';
  const savedConnection =
    desktopState.connection && desktopState.connection.slug === slug ? desktopState.connection : null;
  const notes = [];

  const endpoint =
    relaySettings.profile === 'local'
      ? await ensureEndpoint(slug, {
          publicTokenMode,
          replace: Boolean(input.replace),
          knownPublicToken: savedConnection?.publicRelayToken || '',
        })
      : hostedEndpointFromSettings(relaySettings, savedConnection);
  if (!endpoint.ok) {
    return { ...endpoint, processes: processState() };
  }

  const apiKey = await ensureAPIKey(input.apiKey, slug, savedConnection?.apiKey || '');
  if (!apiKey.ok) {
    return { ...apiKey, processes: processState() };
  }

  let relay = { ok: true, stdout: '' };
  if (relaySettings.profile === 'local' && !processes.relay) {
    relay = await startRelay(relaySettings);
    if (relay.ok) {
      await sleep(350);
    }
  } else if (relaySettings.profile === 'hosted') {
    notes.push(`Using hosted relay ${relaySettings.relayURL}.\n`);
  }
  if (relay.error) {
    notes.push(`${relay.error}\n`);
  }

  let publish = { ok: true, stdout: '' };
  if (!processes.publish) {
    publish = await startPublish({
      ...relaySettings,
      apiKey: apiKey.token,
      publicToken: endpoint.publicToken,
    });
  }
  if (publish.error) {
    notes.push(`${publish.error}\n`);
  }

  notes.push(`${endpoint.created ? 'Created' : 'Using'} endpoint ${slug}.\n`);
  notes.push(`${apiKey.created ? 'Created' : 'Using'} API key for ${slug}.\n`);

  const connection = buildConnection({
    relayURL: relaySettings.relayURL,
    slug,
    apiKey: apiKey.token,
    endpoint,
  });
  await saveDesktopState({ ...desktopState, relaySettings, connection });

  return {
    ok: endpoint.ok && relay.ok && publish.ok,
    stdout: notes.join('\n'),
    stderr: endpoint.stderr || '',
    token: endpoint.publicToken || '',
    apiKey: apiKey.token,
    connection,
    relaySettings,
    processes: processState(),
  };
}

async function prepareHostedRelay(input) {
  const current = await saveRelaySettings({
    ...input,
    profile: 'hosted',
    relayURL: input.relayURL || 'https://your-relay.example.com',
  });
  const bundleDir = input.bundleDir || path.join(app.getPath('userData'), 'hosted-relay');
  const args = [
    'relay',
    'hosted',
    'prepare',
    '--dir',
    bundleDir,
    '--platform',
    input.hostedTarget || input.platform || 'railway',
    '--relay',
    current.relayURL || 'https://your-relay.example.com',
    '--slug',
    current.slug,
    '--agent-token',
    current.publishRelayToken || current.relayToken || 'auto',
    '--public-token',
    current.publicRelayToken || 'auto',
    '--public-auth-header',
    current.publicAuthHeader || defaultRelayPublicAuthHeader,
    '--source',
    relaySourceDir,
    '--force',
    '--json',
  ];
  const result = await runOutpost(args);
  if (!result.ok) {
    return result;
  }

  let prepared;
  try {
    prepared = JSON.parse(result.stdout);
  } catch {
    return {
      ...result,
      ok: false,
      stderr: result.stderr || 'Prepared hosted relay, but could not parse the generated settings.',
    };
  }

  const relaySettings = await saveRelaySettings({
    profile: 'hosted',
    hostedTarget: prepared.platform || input.hostedTarget || input.platform || 'railway',
    relayURL: prepared.relay_url,
    relayToken: prepared.agent_token,
    publishRelayToken: prepared.agent_token,
    slug: prepared.slug,
    publicRelayToken: prepared.public_token || '',
    publicAuthHeader: prepared.public_auth_header || defaultRelayPublicAuthHeader,
  });

  return {
    ok: true,
    stdout: [
      `Prepared hosted relay bundle: ${prepared.bundle_dir}`,
      `Relay URL: ${prepared.relay_url}`,
      `OpenAI base URL: ${prepared.base_url}`,
      `Publish slug: ${prepared.slug}`,
      'Deploy the bundle, then use Start all.',
      '',
    ].join('\n'),
    stderr: result.stderr,
    hostedRelay: prepared,
    relaySettings,
    processes: processState(),
  };
}

async function ensureEndpoint(slug, options = {}) {
  const list = await runOutpost(['relay', 'endpoint', 'list']);
  if (!list.ok) {
    return list;
  }

  const existing = parseEndpointList(list.stdout).find((endpoint) => endpoint.slug === slug);
  if (existing && !options.replace) {
    const tokenRequired = Boolean(existing.tokenPrefix && existing.tokenPrefix !== '-');
    const knownToken = String(options.knownPublicToken || '');
    const knownTokenMatches = tokenRequired && knownToken && knownToken.startsWith(existing.tokenPrefix);
    if (!tokenRequired) {
      return {
        ok: true,
        stdout: '',
        stderr: '',
        publicToken: '',
        publicAuthHeader: existing.authHeader || defaultRelayPublicAuthHeader,
        publicTokenRequired: false,
        publicTokenUnavailable: false,
        publicTokenPrefix: '',
        created: false,
        args: list.args,
      };
    }
    if (knownTokenMatches) {
      return {
        ok: true,
        stdout: '',
        stderr: '',
        publicToken: knownToken,
        publicAuthHeader: existing.authHeader || defaultRelayPublicAuthHeader,
        publicTokenRequired: true,
        publicTokenUnavailable: false,
        publicTokenPrefix: existing.tokenPrefix,
        created: false,
        args: list.args,
      };
    }
    options = { ...options, replace: true };
  }

  const args = [
    'relay',
    'endpoint',
    'create',
    slug,
    '--device',
    'local',
    '--public-token',
    options.publicTokenMode || 'auto',
  ];
  if (options.replace) {
    args.push('--replace');
  }

  const created = await runOutpost(args);
  const publicToken = extractPublicToken(created.stdout);
  return {
    ...created,
    publicToken,
    publicAuthHeader: extractClientRelayHeaderName(created.stdout) || defaultRelayPublicAuthHeader,
    publicTokenRequired: Boolean(publicToken),
    publicTokenUnavailable: false,
    publicTokenPrefix: publicToken ? publicToken.slice(0, 10) : '',
    created: true,
  };
}

function hostedEndpointFromSettings(settings, savedConnection = null) {
  return {
    ok: true,
    stdout: '',
    stderr: '',
    publicToken: settings.publicRelayToken || savedConnection?.publicRelayToken || '',
    publicAuthHeader: settings.publicAuthHeader || defaultRelayPublicAuthHeader,
    publicTokenRequired: Boolean(settings.publicRelayToken || savedConnection?.publicRelayToken),
    publicTokenUnavailable: false,
    publicTokenPrefix: settings.publicRelayToken ? settings.publicRelayToken.slice(0, 10) : '',
    created: false,
  };
}

async function ensureAPIKey(apiKey, slug, savedAPIKey = '') {
  const token = String(apiKey || '').trim();
  if (token) {
    return { ok: true, token, created: false, stdout: '', stderr: '' };
  }
  const saved = String(savedAPIKey || '').trim();
  if (saved) {
    return { ok: true, token: saved, created: false, stdout: '', stderr: '' };
  }

  const created = await runOutpost(['keys', 'create', `desktop:${slug}`]);
  const createdToken = extractAPIKey(created.stdout);
  if (!created.ok || !createdToken) {
    return {
      ...created,
      ok: false,
      stderr: created.stderr || 'Created API key, but could not read the token from command output.',
    };
  }
  return { ...created, token: createdToken, created: true };
}

function buildConnection({ relayURL, slug, apiKey, endpoint }) {
  const baseURL = `${String(relayURL || defaultRelayURL).replace(/\/+$/, '')}/${slug}/v1`;
  const publicRelayHeader =
    endpoint.publicToken && endpoint.publicAuthHeader
      ? `${endpoint.publicAuthHeader}: Bearer ${endpoint.publicToken}`
      : '';
  const publicTokenWarning =
    endpoint.publicTokenUnavailable && endpoint.publicTokenPrefix
      ? `Endpoint ${slug} already has a public relay token (${endpoint.publicTokenPrefix}), but that token is only shown once. Replace the endpoint to generate a new one.`
      : '';

  return {
    slug,
    baseURL,
    apiKey,
    authorizationHeader: `Authorization: Bearer ${apiKey}`,
    publicRelayHeaderName: endpoint.publicAuthHeader || defaultRelayPublicAuthHeader,
    publicRelayToken: endpoint.publicToken || '',
    publicRelayHeader,
    publicTokenWarning,
  };
}

async function testConnection(connection) {
  if (!connection.baseURL || !connection.apiKey) {
    return { ok: false, status: 0, detail: 'Connection config is not ready.' };
  }
  if (typeof fetch !== 'function') {
    return { ok: false, status: 0, detail: 'This Electron runtime does not expose fetch.' };
  }

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 6000);
  const url = `${String(connection.baseURL).replace(/\/+$/, '')}/models`;
  const headers = {
    Authorization: `Bearer ${connection.apiKey}`,
  };
  if (connection.publicRelayHeaderName && connection.publicRelayToken) {
    headers[connection.publicRelayHeaderName] = `Bearer ${connection.publicRelayToken}`;
  }

  try {
    const response = await fetch(url, { headers, signal: controller.signal });
    const text = await response.text();
    let detail = text.slice(0, 240);
    try {
      const parsed = JSON.parse(text);
      if (Array.isArray(parsed.data)) {
        detail = `${parsed.data.length} model${parsed.data.length === 1 ? '' : 's'} available`;
      }
    } catch {
      // Keep the response text preview.
    }
    return { ok: response.ok, status: response.status, detail, url };
  } catch (error) {
    return { ok: false, status: 0, detail: error.message, url };
  } finally {
    clearTimeout(timeout);
  }
}

async function readConfig() {
  const result = await runOutpost(['config', 'print']);
  if (!result.ok) {
    return {};
  }
  try {
    return JSON.parse(result.stdout);
  } catch {
    return {};
  }
}

async function saveBackend(input) {
  const configPath = await runOutpost(['config', 'path']);
  if (!configPath.ok) {
    return configPath;
  }
  const config = await readConfig();
  config.backend = normalizeBackend(input);
  const target = configPath.stdout.trim();
  await writeConfigFile(target, config);
  return { ok: true, stdout: `Saved backend: ${config.backend.type} at ${config.backend.base_url}\n`, backend: config.backend };
}

async function listModels(input) {
  const backend = normalizeBackend(input);
  const result = await fetchBackendJSON(resolveBackendPath(backend.base_url, '/v1/models'), backend.api_key, 2500);
  if (!result.ok) {
    return {
      ok: false,
      models: [],
      detail: result.detail || 'Could not read models from /v1/models.',
      backend,
    };
  }

  const models = Array.isArray(result.payload?.data)
    ? result.payload.data
        .map((model) => ({
          id: String(model.id || '').trim(),
          ownedBy: String(model.owned_by || '').trim(),
        }))
        .filter((model) => model.id)
    : [];

  return {
    ok: models.length > 0,
    models,
    detail: models.length
      ? `${models.length} model${models.length === 1 ? '' : 's'} discovered`
      : 'Server is reachable, but no models are loaded or exposed by /v1/models.',
    backend,
  };
}

async function saveDefaultModel(input) {
  const model = String(input.model || '').trim();
  if (!model) {
    return { ok: false, stdout: '', stderr: 'Choose a model before saving the default alias.' };
  }

  const configPath = await runOutpost(['config', 'path']);
  if (!configPath.ok) {
    return configPath;
  }
  const config = await readConfig();
  config.model_aliases = config.model_aliases || {};
  config.model_aliases[defaultModelAlias] = model;
  await writeConfigFile(configPath.stdout.trim(), config);
  const restarted = await restartServingProcesses();
  return {
    ok: true,
    stdout: [
      `Saved model alias: ${defaultModelAlias} -> ${model}`,
      restarted.length ? `Restarted ${restarted.join(' and ')} to load the alias.` : '',
    ].filter(Boolean).join('\n') + '\n',
    alias: defaultModelAlias,
    model,
    restarted,
    processes: processState(),
  };
}

async function testBackend(input) {
  const backend = normalizeBackend(input);
  const modelCheck = await checkOpenAIModels(backend.base_url, backend.api_key);
  if (modelCheck.ok) {
    return {
      ok: true,
      status: 'ok',
      detail: modelCheck.detail,
      backend,
    };
  }

  const versionCheck = await checkBackendVersion(backend.base_url, backend.api_key);
  if (versionCheck.ok) {
    return {
      ok: true,
      status: 'ok',
      detail: versionCheck.detail,
      backend,
    };
  }

  return {
    ok: false,
    status: 'fail',
    detail: modelCheck.detail || versionCheck.detail || 'Backend is not reachable.',
    backend,
  };
}

async function testInference(input) {
  const config = await readConfig();
  const desktopState = await readDesktopState();
  const connection = input.connection || desktopState.connection || null;
  const model = String(input.model || config.model_aliases?.[defaultModelAlias] || '').trim();
  const prompt = String(input.prompt || defaultInferencePrompt).trim() || defaultInferencePrompt;

  if (!model) {
    return {
      ok: false,
      model,
      prompt,
      results: [
        {
          target: 'Prompt test',
          status: 'fail',
          detail: 'Choose a model first.',
        },
      ],
    };
  }
  if (!connection?.apiKey) {
    return {
      ok: false,
      model,
      prompt,
      results: [
        {
          target: 'Local API',
          status: 'fail',
          detail: 'Client API key is not ready. Click Start all first.',
        },
      ],
    };
  }

  const listenAddr = config.listen_addr || '127.0.0.1:7341';
  const localBaseURL = `http://${listenAddr}/v1`;
  const results = [];
  const local = await postChatCompletion(localBaseURL, {
    apiKey: connection.apiKey,
    model,
    prompt,
  });
  results.push({ target: 'Local API', ...local });

  if (connection.baseURL) {
    const relayHeaders = {};
    if (connection.publicRelayHeaderName && connection.publicRelayToken) {
      relayHeaders[connection.publicRelayHeaderName] = `Bearer ${connection.publicRelayToken}`;
    }
    const relay = await postChatCompletion(connection.baseURL, {
      apiKey: connection.apiKey,
      model,
      prompt,
      extraHeaders: relayHeaders,
    });
    results.push({ target: 'Relay URL', ...relay });
  }

  return {
    ok: results.every((result) => result.status === 'ok'),
    model,
    prompt,
    results,
  };
}

async function listRequestLogs(input = {}) {
  const limit = Math.min(Math.max(Number(input.limit) || 12, 1), 50);
  const config = await readConfig();
  const logPath = config.log_path || '';
  if (!logPath) {
    return { ok: true, logPath: '', entries: [], detail: 'Request logging is disabled.' };
  }

  let data = '';
  try {
    data = await readFileTail(logPath, 256 * 1024);
  } catch (error) {
    if (error.code === 'ENOENT') {
      return { ok: true, logPath, entries: [], detail: 'No requests logged yet.' };
    }
    return { ok: false, logPath, entries: [], detail: error.message || String(error) };
  }

  const entries = data
    .split(/\r?\n/)
    .filter(Boolean)
    .slice(-limit)
    .reverse()
    .map(parseRequestLogLine)
    .filter(Boolean);

  return {
    ok: true,
    logPath,
    entries,
    detail: entries.length ? `${entries.length} recent request${entries.length === 1 ? '' : 's'}` : 'No requests logged yet.',
  };
}

async function readFileTail(filePath, maxBytes) {
  const handle = await fs.open(filePath, 'r');
  try {
    const stat = await handle.stat();
    const size = Math.min(stat.size, maxBytes);
    const buffer = Buffer.alloc(size);
    await handle.read(buffer, 0, size, Math.max(0, stat.size - size));
    return buffer.toString('utf8');
  } finally {
    await handle.close();
  }
}

function parseRequestLogLine(line) {
  try {
    const entry = JSON.parse(line);
    return {
      time: entry.time || '',
      method: entry.method || '',
      path: entry.path || '',
      keyID: entry.key_id || '',
      model: entry.model || '',
      backend: entry.backend || '',
      status: Number(entry.status) || 0,
      bytes: Number(entry.bytes) || 0,
      duration: entry.duration || '',
      error: entry.error || '',
    };
  } catch {
    return null;
  }
}

async function postChatCompletion(baseURL, options) {
  if (typeof fetch !== 'function') {
    return { status: 'fail', code: 0, detail: 'This Electron runtime does not expose fetch.' };
  }

  const url = `${String(baseURL || '').replace(/\/+$/, '')}/chat/completions`;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 15000);
  const headers = {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${options.apiKey}`,
    ...(options.extraHeaders || {}),
  };
  const body = JSON.stringify({
    model: options.model,
    messages: [{ role: 'user', content: options.prompt }],
    stream: false,
    temperature: 0,
    max_tokens: 24,
  });

  try {
    const response = await fetch(url, {
      method: 'POST',
      headers,
      body,
      signal: controller.signal,
    });
    const text = await response.text();
    let parsed = null;
    try {
      parsed = JSON.parse(text);
    } catch {
      // Some compatible runtimes return plain text on failure.
    }

    if (!response.ok) {
      return {
        status: 'fail',
        code: response.status,
        detail: compact(parsed?.error?.message || text || `${url} returned ${response.status}`),
        url,
      };
    }

    const content =
      parsed?.choices?.[0]?.message?.content ||
      parsed?.choices?.[0]?.text ||
      compact(text);
    return {
      status: 'ok',
      code: response.status,
      detail: compact(content || 'Received a completion.'),
      url,
    };
  } catch (error) {
    const message = error.name === 'AbortError'
      ? `Timed out connecting to ${url}.`
      : error.message || String(error);
    return {
      status: 'fail',
      code: 0,
      detail: compact(message),
      url,
    };
  } finally {
    clearTimeout(timeout);
  }
}

async function writeConfigFile(target, config) {
  await fs.writeFile(target, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
}

async function ensureRelaySettings(desktopState = null) {
  const state = desktopState || await readDesktopState();
  const settings = normalizeRelaySettings(state.relaySettings || {});
  let changed = false;
  if (!settings.relayToken) {
    settings.relayToken = newDesktopToken('ort');
    changed = true;
  }
  if (!settings.publishRelayToken) {
    settings.publishRelayToken = settings.relayToken;
    changed = true;
  }
  if (settings.profile === 'local') {
    const localURL = `http://${settings.listen}`;
    if (settings.relayURL !== localURL) {
      settings.relayURL = localURL;
      changed = true;
    }
  }
  if (changed) {
    await saveDesktopState({ ...state, relaySettings: settings });
  }
  return settings;
}

async function saveRelaySettings(input = {}) {
  const state = await readDesktopState();
  const settings = normalizeRelaySettings({ ...(state.relaySettings || {}), ...input });
  if (!settings.relayToken) {
    settings.relayToken = newDesktopToken('ort');
  }
  if (!settings.publishRelayToken) {
    settings.publishRelayToken = settings.relayToken;
  }
  if (settings.profile === 'local') {
    settings.relayURL = `http://${settings.listen}`;
    settings.publishRelayToken = settings.relayToken;
  }
  await saveDesktopState({ ...state, relaySettings: settings });
  return settings;
}

function normalizeRelaySettings(input = {}) {
  const profile = input.profile === 'hosted' ? 'hosted' : 'local';
  const listen = String(input.listen || defaultRelayListen).trim() || defaultRelayListen;
  const relayURL = String(input.relayURL || (profile === 'local' ? `http://${listen}` : '')).trim().replace(/\/+$/, '');
  const relayToken = String(input.relayToken || input.token || input.publishRelayToken || '').trim();
  const publishRelayToken = String(input.publishRelayToken || input.relayToken || input.token || '').trim();
  const slug = normalizeRelaySlug(input.slug || defaultSlug);
  const publicRelayToken = String(input.publicRelayToken || '').trim();
  const publicAuthHeader = String(input.publicAuthHeader || defaultRelayPublicAuthHeader).trim() || defaultRelayPublicAuthHeader;
  const hostedTarget = input.hostedTarget === 'docker' || input.platform === 'docker' ? 'docker' : 'railway';

  return {
    profile,
    hostedTarget,
    listen,
    relayURL,
    relayToken,
    publishRelayToken,
    slug,
    publicRelayToken,
    publicAuthHeader,
  };
}

function newDesktopToken(prefix) {
  return `${prefix}_${crypto.randomBytes(24).toString('base64url')}`;
}

function sleep(ms) {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

function stopProcess(name) {
  const child = processes[name];
  if (!child) {
    return processState();
  }
  child.kill('SIGINT');
  return processState();
}

async function stopAllProcesses() {
  for (const name of ['publish', 'relay', 'api']) {
    const child = processes[name];
    if (!child) continue;
    child.kill('SIGINT');
    await waitForExit(child, 2000);
  }
  return processState();
}

async function restartServingProcesses() {
  const restarted = [];
  for (const name of ['api', 'publish']) {
    if (await restartProcess(name)) {
      restarted.push(name);
    }
  }
  return restarted;
}

async function restartProcess(name) {
  const child = processes[name];
  const args = child?.outpostArgs;
  if (!child || !args) {
    return false;
  }

  child.kill('SIGINT');
  await waitForExit(child, 2500);
  if (processes[name]) {
    return false;
  }

  const started = startLongProcess(name, args);
  if (started.ok) {
    await sleep(500);
  }
  return Boolean(started.ok);
}

function waitForExit(child, timeoutMs) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    child.once('exit', finish);
    setTimeout(finish, timeoutMs);
  });
}

function processState() {
  return {
    api: Boolean(processes.api),
    relay: Boolean(processes.relay),
    publish: Boolean(processes.publish),
  };
}

async function getRuntimeProcessState() {
  const [config, desktopState] = await Promise.all([readConfig(), readDesktopState()]);
  const settings = await ensureRelaySettings(desktopState);
  return detectedProcessState(settings, config);
}

async function detectedProcessState(settings, config) {
  const owned = processState();
  const listenAddr = config.listen_addr || '127.0.0.1:7341';
  const apiHealth = await checkHTTP(`http://${listenAddr}/healthz`, { timeoutMs: 500 });
  let relayDetected = false;
  if (settings.profile === 'local' && settings.relayURL) {
    const relayHealth = await checkHTTP(settings.relayURL, { timeoutMs: 500 });
    relayDetected = relayHealth.ok;
  } else if (settings.profile === 'hosted' && settings.relayURL) {
    const relayHealth = await checkHTTP(settings.relayURL, { timeoutMs: 1200 });
    relayDetected = relayHealth.ok;
  }

  return {
    ...owned,
    apiDetected: owned.api || apiHealth.ok,
    relayDetected: owned.relay || relayDetected,
    publishDetected: owned.publish,
  };
}

function sendLog(name, message) {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('outpost:log', { name, message });
  }
}

function sendState() {
  rebuildTrayMenu();
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('outpost:state', processState());
  }
}

function parseEndpointList(output) {
  const lines = output.trim().split(/\r?\n/).filter(Boolean);
  return lines.slice(1).map((line) => {
    const [slug, device, tokenPrefix, authHeader, created, revoked] = line.split('\t');
    return { slug, device, tokenPrefix, authHeader, created, revoked };
  });
}

function extractAPIKey(output) {
  const lines = output.split(/\r?\n/);
  const marker = lines.findIndex((line) => line.includes('Token, shown once'));
  if (marker >= 0 && lines[marker + 1]) {
    return lines[marker + 1].trim();
  }
  return '';
}

function extractPublicToken(output) {
  const lines = output.split(/\r?\n/);
  const marker = lines.findIndex((line) => line.includes('Public relay token, shown once'));
  if (marker >= 0 && lines[marker + 1]) {
    return lines[marker + 1].trim();
  }
  return '';
}

function extractClientRelayHeaderName(output) {
  const lines = output.split(/\r?\n/);
  const marker = lines.findIndex((line) => line.includes('Client relay header'));
  if (marker >= 0 && lines[marker + 1]) {
    const [name] = lines[marker + 1].split(':');
    return name.trim();
  }
  return '';
}

function normalizeRelaySlug(value) {
  return (
    String(value || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9-]/g, '')
      .replace(/^-+|-+$/g, '') || defaultSlug
  );
}

async function runDiagnostics(input = {}) {
  const status = await runOutpost(['status']);
  const statusText = status.stdout || status.stderr || '';
  const listenAddr = parseStatusValue(statusText, 'Listen') || '127.0.0.1:7341';
  const config = await readConfig();
  const backend = normalizeBackend(config.backend || {});
  const relaySettings = normalizeRelaySettings(input.relaySettings || input || {});
  const checks = [];

  checks.push({
    id: 'binary',
    label: 'Binary',
    status: status.ok ? 'ok' : 'fail',
    detail: status.ok ? `Ready at ${outpostBin}` : compact(status.stderr || status.stdout || 'Outpost binary failed.'),
  });

  const apiHealth = await checkHTTP(`http://${listenAddr}/healthz`, { timeoutMs: 1200 });
  if (apiHealth.ok) {
    checks.push({
      id: 'api',
      label: 'Local API',
      status: 'ok',
      detail: `Running on ${listenAddr}`,
    });
  } else if (processes.api) {
    checks.push({
      id: 'api',
      label: 'Local API',
      status: 'warn',
      detail: `Starting on ${listenAddr}`,
    });
  } else {
    const port = splitHostPort(listenAddr);
    const portState = port ? await checkPortAvailable(port.host, port.port) : { available: false, error: 'invalid listen address' };
    checks.push({
      id: 'api',
      label: 'Local API',
      status: portState.available ? 'warn' : 'fail',
      detail: portState.available
        ? `Not running. It can be started now or by Publish.`
        : `Port ${listenAddr} is busy and health did not respond.`,
      action: portState.available ? { type: 'start-api', label: 'Start' } : null,
    });
  }

  if (!backend.base_url) {
    checks.push({
      id: 'backend',
      label: 'Ollama',
      status: 'fail',
      detail: 'No backend URL found in Outpost config.',
    });
  } else {
    const backendHealth = await testBackend(backend);
    const backendLabel = backendLabelForType(backend.type);
    checks.push({
      id: 'backend',
      label: backendLabel,
      status: backendHealth.ok ? 'ok' : 'fail',
      detail: backendHealth.ok ? `${backendHealth.detail}` : `Not reachable at ${backend.base_url}`,
    });

    const modelCheck = await checkOpenAIModels(backend.base_url, backend.api_key);
    checks.push({
      id: 'models',
      label: 'Models',
      status: modelCheck.ok ? 'ok' : 'warn',
      detail: modelCheck.detail,
    });
  }

  if (relaySettings.profile === 'hosted') {
    const relayHealth = relaySettings.relayURL
      ? await checkHTTP(relaySettings.relayURL, { timeoutMs: 1200 })
      : { ok: false, status: 0 };
    checks.push({
      id: 'relay-port',
      label: 'Hosted relay',
      status: relayHealth.ok ? 'ok' : 'warn',
      detail: relayHealth.ok
        ? `Reachable at ${relaySettings.relayURL}`
        : `Configure or start the hosted relay at ${relaySettings.relayURL || 'a relay URL'}.`,
    });
  } else if (processes.relay) {
    checks.push({
      id: 'relay-port',
      label: 'Relay port',
      status: 'ok',
      detail: `Relay running on ${relaySettings.listen}`,
    });
  } else {
    const relay = splitHostPort(relaySettings.listen);
    const relayPort = relay ? await checkPortAvailable(relay.host, relay.port) : { available: false, error: 'invalid listen address' };
    checks.push({
      id: 'relay-port',
      label: 'Relay port',
      status: relayPort.available ? 'ok' : 'fail',
      detail: relayPort.available ? `Available at ${relaySettings.listen}` : `Port ${relaySettings.listen} is already in use.`,
    });
  }

  return {
    ok: checks.every((check) => check.status !== 'fail'),
    checks,
  };
}

function parseStatusValue(output, key) {
  const prefix = `${key}:`;
  const line = output.split(/\r?\n/).find((candidate) => candidate.startsWith(prefix));
  return line ? line.slice(prefix.length).trim() : '';
}

async function checkHTTP(url, options = {}) {
  if (typeof fetch !== 'function') {
    return { ok: false, detail: 'fetch unavailable' };
  }
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeoutMs || 1500);
  try {
    const response = await fetch(url, { signal: controller.signal });
    const text = await response.text();
    let parsed = null;
    try {
      parsed = JSON.parse(text);
    } catch {
      // Plain health responses are fine.
    }
    return {
      ok: response.ok,
      status: response.status,
      text,
      version: parsed?.version || '',
    };
  } catch (error) {
    return { ok: false, error: error.message };
  } finally {
    clearTimeout(timeout);
  }
}

async function checkOpenAIModels(backendURL, apiKey = '') {
  const url = resolveBackendPath(backendURL, '/v1/models');
  const result = await fetchBackendJSON(url, apiKey, 1800);
  if (!result.ok) {
    return { ok: false, detail: result.detail || 'Could not read OpenAI-compatible /v1/models.' };
  }

  const models = Array.isArray(result.payload?.data) ? result.payload.data : [];
  if (!models.length) {
    return { ok: false, detail: 'Server is reachable, but no models are loaded or exposed by /v1/models.' };
  }
  const names = models
    .map((model) => model.id)
    .filter(Boolean)
    .slice(0, 3)
    .join(', ');
  return {
    ok: true,
    detail: `${models.length} model${models.length === 1 ? '' : 's'} available${names ? `: ${names}` : ''}`,
  };
}

async function checkBackendVersion(backendURL, apiKey = '') {
  const result = await fetchBackendJSON(resolveBackendPath(backendURL, '/api/version'), apiKey, 1600);
  if (!result.ok) {
    return { ok: false, detail: result.detail || 'Backend version check failed.' };
  }
  return {
    ok: true,
    detail: `Reachable at ${backendURL}${result.payload?.version ? `, version ${result.payload.version}` : ''}`,
  };
}

async function fetchBackendJSON(url, apiKey, timeoutMs) {
  if (typeof fetch !== 'function') {
    return { ok: false, detail: 'This Electron runtime does not expose fetch.' };
  }
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  const headers = {};
  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }
  try {
    const response = await fetch(url, { headers, signal: controller.signal });
    const text = await response.text();
    if (!response.ok) {
      if (response.status === 404 && url.includes('/v1/models')) {
        return {
          ok: false,
          detail: `${url} returned 404. Check that the backend base URL exposes OpenAI-compatible /v1 routes.`,
        };
      }
      return { ok: false, detail: `${url} returned ${response.status}` };
    }
    try {
      return { ok: true, payload: JSON.parse(text) };
    } catch {
      return { ok: false, detail: `${url} did not return JSON.` };
    }
  } catch (error) {
    const message = error.message || String(error);
    if (error.name === 'AbortError') {
      return { ok: false, detail: `Timed out connecting to ${url}. Make sure the local server is started.` };
    }
    if (/fetch failed|ECONNREFUSED|ECONNRESET|ENOTFOUND|EHOSTUNREACH|NetworkError/i.test(message)) {
      return { ok: false, detail: `Could not connect to ${url}. Make sure the local server is started.` };
    }
    return { ok: false, detail: message };
  } finally {
    clearTimeout(timeout);
  }
}

function resolveBackendPath(baseURL, backendPath) {
  const trimmed = String(baseURL || '').trim().replace(/\/+$/, '');
  if (!trimmed) return backendPath;
  if (backendPath.startsWith('/v1/') && /\/v1$/i.test(trimmed)) {
    return `${trimmed}${backendPath.slice(3)}`;
  }
  if (backendPath.startsWith('/api/') && /\/v1$/i.test(trimmed)) {
    return `${trimmed.slice(0, -3)}${backendPath}`;
  }
  return `${trimmed}${backendPath}`;
}

function normalizeBackend(input = {}) {
  const type = String(input.type || 'ollama').trim() || 'ollama';
  const baseURL = String(input.base_url || input.baseURL || defaultBackendURL(type)).trim().replace(/\/+$/, '');
  const apiKey = String(input.api_key || input.apiKey || '').trim();
  return {
    type,
    base_url: baseURL,
    ...(apiKey ? { api_key: apiKey } : {}),
  };
}

function defaultBackendURL(type) {
  if (type === 'lmstudio') return 'http://127.0.0.1:1234/v1';
  if (type === 'openai-compatible') return 'http://127.0.0.1:1234/v1';
  return 'http://127.0.0.1:11434';
}

function backendLabelForType(type) {
  if (type === 'lmstudio') return 'LM Studio';
  if (type === 'openai-compatible') return 'Backend';
  return 'Ollama';
}

async function readDesktopState() {
  try {
    const data = await fs.readFile(desktopStatePath(), 'utf8');
    return JSON.parse(data);
  } catch {
    return {};
  }
}

async function saveDesktopState(state) {
  const file = desktopStatePath();
  await fs.mkdir(path.dirname(file), { recursive: true });
  await fs.writeFile(file, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

function desktopStatePath() {
  return path.join(app.getPath('userData'), 'state.json');
}

function splitHostPort(value) {
  const match = String(value || '').match(/^(.+):(\d+)$/);
  if (!match) return null;
  return { host: match[1], port: Number(match[2]) };
}

function checkPortAvailable(host, port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once('error', (error) => {
      resolve({ available: false, error: error.code || error.message });
    });
    server.once('listening', () => {
      server.close(() => resolve({ available: true }));
    });
    server.listen(port, host);
  });
}

function compact(value) {
  return String(value || '').replace(/\s+/g, ' ').trim().slice(0, 180);
}
