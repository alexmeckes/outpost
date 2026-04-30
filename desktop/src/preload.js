const { clipboard, contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('outpost', {
  overview: () => ipcRenderer.invoke('outpost:overview'),
  diagnostics: (input) => ipcRenderer.invoke('outpost:diagnostics', input),
  startAPI: () => ipcRenderer.invoke('outpost:api-start'),
  saveBackend: (backend) => ipcRenderer.invoke('outpost:backend-save', backend),
  testBackend: (backend) => ipcRenderer.invoke('outpost:backend-test', backend),
  listModels: (backend) => ipcRenderer.invoke('outpost:models-list', backend),
  saveDefaultModel: (input) => ipcRenderer.invoke('outpost:model-default-save', input),
  testInference: (input) => ipcRenderer.invoke('outpost:inference-test', input),
  listRequests: (input) => ipcRenderer.invoke('outpost:requests-list', input),
  createEndpoint: (input) => ipcRenderer.invoke('outpost:endpoint-create', input),
  revokeEndpoint: (slug) => ipcRenderer.invoke('outpost:endpoint-revoke', slug),
  startRelay: (input) => ipcRenderer.invoke('outpost:relay-start', input),
  startPublish: (input) => ipcRenderer.invoke('outpost:publish-start', input),
  startHappyPath: (input) => ipcRenderer.invoke('outpost:happy-path-start', input),
  prepareHostedRelay: (input) => ipcRenderer.invoke('outpost:hosted-prepare', input),
  testConnection: (connection) => ipcRenderer.invoke('outpost:connection-test', connection),
  stopProcess: (name) => ipcRenderer.invoke('outpost:process-stop', name),
  stopAll: () => ipcRenderer.invoke('outpost:process-stop-all'),
  processState: () => ipcRenderer.invoke('outpost:process-state'),
  copyText: (text) => clipboard.writeText(String(text ?? '')),
  onLog: (callback) => {
    const listener = (_event, payload) => callback(payload);
    ipcRenderer.on('outpost:log', listener);
    return () => ipcRenderer.off('outpost:log', listener);
  },
  onState: (callback) => {
    const listener = (_event, payload) => callback(payload);
    ipcRenderer.on('outpost:state', listener);
    return () => ipcRenderer.off('outpost:state', listener);
  },
});
