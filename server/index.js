'use strict';

const express = require('express');
const cors = require('cors');
const path = require('path');
const fs = require('fs');
const os = require('os');
const { spawn } = require('child_process');
const yaml = require('js-yaml');
const {
  getAllAssets,
  getAsset,
  createAsset,
  updateAsset,
  deleteAsset,
  transferAsset,
  getAssetHistory,
  queryChaincode,
  invokeChaincode,
  CHANNEL_CHAINCODE_MAP,
  getAllChaincodesForChannel,
} = require('./fabric');
const config = require('./config');

const app = express();
const PORT = process.env.PORT || 3000;

// --- Middleware ---
app.use(cors());
app.use(express.json({ limit: '10mb' }));
app.use(express.urlencoded({ extended: true, limit: '10mb' }));
app.use(express.static(path.join(__dirname, '../public')));

if (process.env.NODE_ENV !== 'production') {
  app.use((req, res, next) => {
    console.log(`[${new Date().toISOString()}] ${req.method} ${req.path}`);
    next();
  });
}

// --- Test tooling configuration ---
const TEST_TOOLS_DIR =
  process.env.TEST_TOOLS_DIR || path.resolve(__dirname, '..', 'test-tools');
const TAPE_CONFIG_DIR = process.env.TAPE_CONFIG_DIR || os.tmpdir();
const CALIPER_WORKLOAD_DIR =
  process.env.CALIPER_WORKLOAD_DIR ||
  path.join(TEST_TOOLS_DIR, 'caliper', 'workloads');
const CALIPER_BIN = process.env.CALIPER_BIN || 'npx';

// Maps a UI scenario name to the chaincode function to invoke.
// Falls back to the scenario string itself if not listed.
const SCENARIO_FN = {
  iotRegister: 'RegisterDevice',
  iotUpdate: 'UpdateDeviceState',
  iotQuery: 'QueryDevice',
  userRegister: 'RegisterUser',
  userQuery: 'QueryUser',
  handover: 'RecordHandover',
  createAsset: 'CreateAsset',
  readAsset: 'ReadAsset',
};

function resolveFunction(scenario) {
  if (!scenario) return null;
  return SCENARIO_FN[scenario] || scenario;
}

// --- Health & network routes ---
app.get('/api/health', (req, res) => {
  res.json({
    status: 'ok',
    timestamp: new Date().toISOString(),
    network: {
      organizations: config.organizations.length,
      channels: Object.keys(CHANNEL_CHAINCODE_MAP).length,
    },
  });
});

app.get('/api/network/info', (req, res) => {
  const orgs = config.organizations.map((org) => ({
    orgNumber: org.orgNum,
    name: org.name,
    mspId: org.mspId,
    domain: org.domain,
    peerEndpoint: org.peerEndpoint,
  }));

  const channels = Object.keys(CHANNEL_CHAINCODE_MAP);

  res.json({
    organizations: orgs,
    channels,
    orderer: {
      address: config.orderer.address,
      port: config.orderer.port,
      endpoint: config.orderer.endpoint,
    },
    tlsEnabled: config.tlsEnabled,
  });
});

app.get('/api/channels/:channelName', (req, res) => {
  const { channelName } = req.params;
  const chaincodes = CHANNEL_CHAINCODE_MAP[channelName];

  if (!chaincodes) {
    return res.status(404).json({ error: `Channel ${channelName} not found` });
  }

  const members = config.organizations
    .filter((org) => (org.channels || []).includes(channelName))
    .map((org) => org.mspId);

  res.json({
    channelName,
    chaincodes,
    members,
  });
});

// --- Asset routes ---
app.get('/api/assets', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const assets = await getAllAssets(orgNum, channel, chaincode || null);
    res.json(assets);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.get('/api/assets/:id', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    const { id } = req.params;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const asset = await getAsset(orgNum, channel, chaincode || null, id);
    res.json(asset);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.post('/api/assets', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    if (!req.body.ID) {
      return res.status(400).json({ error: 'Missing required field: ID' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await createAsset(orgNum, channel, chaincode || null, req.body);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.put('/api/assets/:id', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    const { id } = req.params;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await updateAsset(orgNum, channel, chaincode || null, id, req.body);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.delete('/api/assets/:id', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    const { id } = req.params;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await deleteAsset(orgNum, channel, chaincode || null, id);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.post('/api/assets/:id/transfer', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    const { id } = req.params;
    const { newOwner } = req.body;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    if (!newOwner) {
      return res.status(400).json({ error: 'Missing required field: newOwner' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await transferAsset(orgNum, channel, chaincode || null, id, newOwner);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.get('/api/assets/:id/history', async (req, res) => {
  try {
    const { org, channel, chaincode } = req.query;
    const { id } = req.params;
    if (!org || !channel) {
      return res.status(400).json({ error: 'Missing required query: org, channel' });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const history = await getAssetHistory(orgNum, channel, chaincode || null, id);
    res.json(history);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

// --- Query & invoke routes ---
app.post('/api/query', async (req, res) => {
  try {
    const { org, channel, chaincode, function: fn, args } = req.body;
    if (!org || !channel || !chaincode || !fn) {
      return res.status(400).json({
        error: 'Missing required fields: org, channel, chaincode, function',
      });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await queryChaincode(orgNum, channel, chaincode, fn, args || []);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

app.post('/api/invoke', async (req, res) => {
  try {
    const { org, channel, chaincode, function: fn, args } = req.body;
    if (!org || !channel || !chaincode || !fn) {
      return res.status(400).json({
        error: 'Missing required fields: org, channel, chaincode, function',
      });
    }
    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }
    const result = await invokeChaincode(orgNum, channel, chaincode, fn, args || []);
    res.json(result);
  } catch (error) {
    res.status(500).json({ error: error.message });
  }
});

// --- Tape helpers ---
function getFilePath(dirOrFile) {
  try {
    const stat = fs.statSync(dirOrFile);
    if (stat.isDirectory()) {
      const files = fs.readdirSync(dirOrFile).filter((f) => !f.startsWith('.'));
      if (files.length === 0) {
        throw new Error(`No files found in directory: ${dirOrFile}`);
      }
      return path.join(dirOrFile, files[0]);
    }
    return dirOrFile;
  } catch (err) {
    throw new Error(`Cannot resolve path "${dirOrFile}": ${err.message}`);
  }
}

function generateTapeConfig({ org, channel, chaincode, fn, args }) {
  const endorsers = [
    {
      addr: org.peerEndpoint,
      tls_ca_cert: org.tlsRootCert,
      org: org.mspId,
    },
  ];

  const committers = [
    {
      addr: config.orderer.endpoint,
      tls_ca_cert: config.orderer.tlsCaCert,
      org: 'OrdererMSP',
    },
  ];

  return {
    endorsers,
    committers,
    commitThreshold: 1,
    mspid: org.mspId,
    private_key: getFilePath(org.adminKeyDir),
    sign_cert: getFilePath(org.adminCertDir),
    channel,
    chaincode,
    args: [fn, ...(args || [])],
    num_of_conn: 10,
    client_per_conn: 10,
  };
}

// --- Result normalization & metric parsing ---
// Normalizes any test result into the exact shape the UI expects.
// UI reads: result.tps, result.latency.avg, result.successCount, result.failedCount
function toUiShape(partial = {}) {
  const m = partial.metrics || {};
  return {
    success: partial.success === true,
    exitCode: typeof partial.exitCode === 'number' ? partial.exitCode : -1,
    tool: partial.tool || 'unknown',
    tps: Number.isFinite(m.tps) ? m.tps : 0,
    latency: {
      avg: Number.isFinite(m.latencyAvg) ? m.latencyAvg : 0,
      min: Number.isFinite(m.latencyMin) ? m.latencyMin : 0,
      max: Number.isFinite(m.latencyMax) ? m.latencyMax : 0,
    },
    successCount: Number.isFinite(m.successCount) ? m.successCount : 0,
    failedCount: Number.isFinite(m.failedCount) ? m.failedCount : 0,
    // keep raw output for debugging in the UI console
    stdout: partial.stdout || '',
    stderr: partial.stderr || '',
    meta: partial.meta || {},
  };
}

// Best-effort extraction of metrics from tape stdout/stderr.
function parseTapeOutput(stdout = '', stderr = '') {
  const text = `${stdout}\n${stderr}`;
  const num = (re) => {
    const match = text.match(re);
    return match ? parseFloat(match[1]) : NaN;
  };
  return {
    tps: num(/tps[:\s]+([\d.]+)/i),
    latencyAvg: num(/(?:avg|average)\s*latency[:\s]+([\d.]+)/i),
    latencyMin: num(/min\s*latency[:\s]+([\d.]+)/i),
    latencyMax: num(/max\s*latency[:\s]+([\d.]+)/i),
    successCount: num(/success(?:ful)?[:\s]+(\d+)/i),
    failedCount: num(/fail(?:ed|ures)?[:\s]+(\d+)/i),
  };
}

// Caliper prints a summary table; pull throughput + latency from it.
function parseCaliperOutput(stdout = '', stderr = '') {
  const text = `${stdout}\n${stderr}`;
  const num = (re) => {
    const match = text.match(re);
    return match ? parseFloat(match[1]) : NaN;
  };
  const tps = num(/Throughput\s*\(TPS\)[|\s]+([\d.]+)/i);
  return {
    tps: Number.isFinite(tps) ? tps : num(/Send Rate[|\s]+([\d.]+)/i),
    latencyAvg: num(/Avg Latency[^\d]*([\d.]+)/i),
    latencyMin: num(/Min Latency[^\d]*([\d.]+)/i),
    latencyMax: num(/Max Latency[^\d]*([\d.]+)/i),
    successCount: num(/Succ[^\d]*(\d+)/i),
    failedCount: num(/Fail[^\d]*(\d+)/i),
  };
}

// --- Test runners ---
// Runs a tape test and resolves with parsed metrics (UI shape).
function runTapeTest({ orgDef, channel, chaincode, fn, args, tps, duration, signers, burst }) {
  return new Promise((resolve) => {
    let tempConfigPath;
    const cleanup = () => {
      if (tempConfigPath && fs.existsSync(tempConfigPath)) {
        try {
          fs.unlinkSync(tempConfigPath);
        } catch (_) {
          /* ignore */
        }
      }
    };

    try {
      const tapeConfig = generateTapeConfig({
        org: orgDef,
        channel,
        chaincode,
        fn,
        args: Array.isArray(args) ? args : [],
      });

      tempConfigPath = path.join(
        TAPE_CONFIG_DIR,
        `tape-config-${orgDef.orgNum}-${Date.now()}.yaml`
      );
      fs.writeFileSync(tempConfigPath, yaml.dump(tapeConfig), 'utf8');

      const rate = tps || 100;
      const total = duration ? Math.max(1, Math.round(duration * rate)) : 1000;

      const tapeArgs = ['-c', tempConfigPath, '--rate', String(rate), '-n', String(total)];
      if (burst) tapeArgs.push('--burst', String(burst));
      if (signers) tapeArgs.push('--signers', String(signers));

      const tapeBin = process.env.TAPE_BIN || 'tape';
      const child = spawn(tapeBin, tapeArgs, {
        env: { ...process.env, CORE_PEER_TLS_ENABLED: String(config.tlsEnabled) },
      });

      let stdout = '';
      let stderr = '';
      child.stdout.on('data', (d) => (stdout += d.toString()));
      child.stderr.on('data', (d) => (stderr += d.toString()));

      child.on('error', (err) => {
        cleanup();
        resolve(
          toUiShape({
            tool: 'tape',
            success: false,
            exitCode: -1,
            stderr: `Failed to launch tape: ${err.message}`,
          })
        );
      });

      child.on('close', (code) => {
        cleanup();
        resolve(
          toUiShape({
            tool: 'tape',
            success: code === 0,
            exitCode: code,
            metrics: parseTapeOutput(stdout, stderr),
            stdout,
            stderr,
            meta: { channel, chaincode, function: fn, rate, total },
          })
        );
      });
    } catch (err) {
      cleanup();
      resolve(toUiShape({ tool: 'tape', success: false, stderr: err.message }));
    }
  });
}

// Runs a Caliper test via `npx caliper launch manager`.
function runCaliperTest({ orgDef, channel, chaincode, fn, tps, duration, iotCount, userCount }) {
  return new Promise((resolve) => {
    const workload = path.join(CALIPER_WORKLOAD_DIR, `${fn}.js`);
    if (!fs.existsSync(workload)) {
      return resolve(
        toUiShape({
          tool: 'caliper',
          success: false,
          exitCode: -1,
          stderr: `Caliper workload not found: ${workload}`,
        })
      );
    }

    const args = [
      'caliper',
      'launch',
      'manager',
      '--caliper-workspace',
      TEST_TOOLS_DIR,
      '--caliper-networkconfig',
      path.join(TEST_TOOLS_DIR, 'caliper', 'networks', `org${orgDef.orgNum}.yaml`),
      '--caliper-benchconfig',
      path.join(TEST_TOOLS_DIR, 'caliper', 'benchmarks', `${fn}.yaml`),
      '--caliper-flow-only-test',
    ];

    const child = spawn(CALIPER_BIN, args, {
      cwd: TEST_TOOLS_DIR,
      env: {
        ...process.env,
        CORE_PEER_TLS_ENABLED: String(config.tlsEnabled),
        // pass UI params to the workload module
        CALIPER_TPS: String(tps || 100),
        CALIPER_DURATION: String(duration || 30),
        CALIPER_IOT_COUNT: String(iotCount || 0),
        CALIPER_USER_COUNT: String(userCount || 0),
        CALIPER_CHANNEL: channel,
        CALIPER_CHAINCODE: chaincode,
      },
    });

    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d.toString()));
    child.stderr.on('data', (d) => (stderr += d.toString()));

    child.on('error', (err) => {
      resolve(
        toUiShape({
          tool: 'caliper',
          success: false,
          exitCode: -1,
          stderr: `Failed to launch caliper: ${err.message}`,
        })
      );
    });

    child.on('close', (code) => {
      resolve(
        toUiShape({
          tool: 'caliper',
          success: code === 0,
          exitCode: code,
          metrics: parseCaliperOutput(stdout, stderr),
          stdout,
          stderr,
          meta: { channel, chaincode, function: fn, iotCount, userCount },
        })
      );
    });
  });
}

// --- Performance test route ---
app.post('/api/test/execute', async (req, res) => {
  try {
    const {
      tool = 'tape',
      scenario,
      channel,
      chaincode,
      iotCount = 0,
      userCount = 0,
      tps,
      duration,
      org,
      // still accept low-level tape fields if the caller sends them directly
      function: fnDirect,
      args,
      signers,
      burst,
    } = req.body;

    if (!org || !channel || !chaincode || (!scenario && !fnDirect)) {
      return res.status(400).json({
        error: 'Missing required fields: org, channel, chaincode, scenario',
      });
    }

    const orgNum = parseInt(org, 10);
    if (isNaN(orgNum) || orgNum < 1 || orgNum > 8) {
      return res.status(400).json({ error: 'Invalid org number (must be 1-8)' });
    }

    const orgDef = config.getOrg(orgNum);
    if (!orgDef) {
      return res.status(404).json({ error: `Org ${orgNum} not found in config` });
    }

    const fn = fnDirect || resolveFunction(scenario);

    let result;
    if (tool === 'caliper') {
      result = await runCaliperTest({
        orgDef,
        channel,
        chaincode,
        fn,
        tps,
        duration,
        iotCount,
        userCount,
      });
    } else {
      // build args for tape from the scenario params when not given explicitly
      const tapeArgs = Array.isArray(args)
        ? args
        : [String(iotCount || userCount || 1)];
      result = await runTapeTest({
        orgDef,
        channel,
        chaincode,
        fn,
        args: tapeArgs,
        tps,
        duration,
        signers,
        burst,
      });
    }

    // response is already in UI shape; add echo fields for context
    return res.json({ ...result, org: orgNum, scenario: scenario || null });
  } catch (error) {
    console.error('Error executing performance test:', error);
    if (!res.headersSent) {
      return res.status(500).json(toUiShape({ success: false, stderr: error.message }));
    }
  }
});

// --- Error handlers & startup ---
app.use((err, req, res, next) => {
  console.error('Unhandled error:', err);
  res.status(500).json({
    error: 'Internal server error',
    message: process.env.NODE_ENV !== 'production' ? err.message : undefined,
  });
});

app.use('/api/*', (req, res) => {
  res.status(404).json({ error: 'API endpoint not found' });
});

app.get('*', (req, res) => {
  res.sendFile(path.join(__dirname, '../public/index.html'));
});

app.listen(PORT, () => {
  console.log(`✅ Dashboard API server running on http://localhost:${PORT}`);
  console.log(
    `📊 Network: ${config.organizations.length} organizations, ${Object.keys(CHANNEL_CHAINCODE_MAP).length} channels`
  );
  console.log(`🔒 TLS: ${config.tlsEnabled ? 'Enabled' : 'Disabled'}`);
  console.log(`🌐 Environment: ${process.env.NODE_ENV || 'development'}`);
});

module.exports = app;
