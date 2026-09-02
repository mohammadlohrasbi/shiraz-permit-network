'use strict';

/* ═══════════════════════════════════════════════════════════════
   explorer-routes.js — ledger explorer.

   Reads blocks straight from the peer through qscc, the system
   chaincode every channel exposes:
       GetChainInfo        → height, current and previous block hash
       GetBlockByNumber    → the block itself
       GetBlockByTxID      → the block holding a transaction

   Blocks arrive as protobuf and are decoded with the same library the
   gateway SDK already depends on, so nothing new has to be installed
   and no extra service has to run.

   Routes
     GET /api/explorer/summary               every channel with its height
     GET /api/explorer/blocks?channel&limit&before
     GET /api/explorer/block?channel&number
     GET /api/explorer/tx?channel&id
   ═══════════════════════════════════════════════════════════════ */

const express = require('express');
const { execFile } = require('child_process');
const { withGateway } = require('./connection');
const { CHANNEL_CHAINCODE_MAP } = require('./fabric');

const router = express.Router();

/* ── protobuf layer ───────────────────────────────────────────── */
let protosLib = null;
let protosError = null;
try {
  protosLib = require('@hyperledger/fabric-protos');
} catch (err) {
  protosError = `@hyperledger/fabric-protos is not installed — run: cd server && npm install @hyperledger/fabric-protos`;
}

// jspb hands back either base64 strings or Uint8Array depending on build
function buf(v) {
  if (v == null) return Buffer.alloc(0);
  if (typeof v === 'string') return Buffer.from(v, 'base64');
  return Buffer.from(v);
}
const hex = (v) => buf(v).toString('hex');

const TX_TYPE = {
  0: 'MESSAGE', 1: 'CONFIG', 2: 'CONFIG_UPDATE', 3: 'ENDORSER_TRANSACTION',
  4: 'ORDERER_TRANSACTION', 5: 'DELIVER_SEEK_INFO', 6: 'CHAINCODE_PACKAGE',
};
// index 0 is VALID; everything else is a rejection reason
const VALIDATION = {
  0: 'VALID', 1: 'NIL_ENVELOPE', 2: 'BAD_PAYLOAD', 3: 'BAD_COMMON_HEADER',
  4: 'BAD_CREATOR_SIGNATURE', 5: 'INVALID_ENDORSER_TRANSACTION',
  6: 'INVALID_CONFIG_TRANSACTION', 7: 'UNSUPPORTED_TX_PAYLOAD',
  8: 'BAD_PROPOSAL_TXID', 9: 'DUPLICATE_TXID', 10: 'ENDORSEMENT_POLICY_FAILURE',
  11: 'MVCC_READ_CONFLICT', 12: 'PHANTOM_READ_CONFLICT', 13: 'UNKNOWN_TX_TYPE',
  14: 'TARGET_CHAIN_NOT_FOUND', 15: 'MARSHAL_TX_ERROR', 16: 'NIL_TXACTION',
  17: 'EXPIRED_CHAINCODE', 18: 'CHAINCODE_VERSION_CONFLICT',
  19: 'BAD_HEADER_EXTENSION', 20: 'BAD_CHANNEL_HEADER',
  21: 'BAD_RESPONSE_PAYLOAD', 22: 'ILLEGAL_WRITESET', 23: 'INVALID_WRITESET',
  24: 'INVALID_CHAINCODE', 254: 'NOT_VALIDATED', 255: 'INVALID_OTHER_REASON',
};

/* Decode one envelope into a flat transaction summary. Never throws:
   whatever cannot be parsed is reported instead of losing the block. */
function decodeEnvelope(envBytes, validationCode) {
  const { common, protos, msp } = protosLib;
  const out = { valid: validationCode === 0, validation: VALIDATION[validationCode] ?? String(validationCode) };

  try {
    const env = common.Envelope.deserializeBinary(buf(envBytes));
    const payload = common.Payload.deserializeBinary(buf(env.getPayload_asU8 ? env.getPayload_asU8() : env.getPayload()));
    const header = payload.getHeader();

    const ch = common.ChannelHeader.deserializeBinary(
      buf(header.getChannelHeader_asU8 ? header.getChannelHeader_asU8() : header.getChannelHeader()));
    out.txId = ch.getTxId();
    out.type = TX_TYPE[ch.getType()] || `TYPE_${ch.getType()}`;
    out.channel = ch.getChannelId();
    const ts = ch.getTimestamp();
    if (ts) out.timestamp = new Date(Number(ts.getSeconds()) * 1000 + Math.floor(Number(ts.getNanos()) / 1e6)).toISOString();

    try {
      const sig = common.SignatureHeader.deserializeBinary(
        buf(header.getSignatureHeader_asU8 ? header.getSignatureHeader_asU8() : header.getSignatureHeader()));
      const ident = msp.SerializedIdentity.deserializeBinary(
        buf(sig.getCreator_asU8 ? sig.getCreator_asU8() : sig.getCreator()));
      out.submitter = ident.getMspid();
    } catch { /* creator stays unknown */ }

    if (ch.getType() === 3) {
      const tx = protos.Transaction.deserializeBinary(
        buf(payload.getData_asU8 ? payload.getData_asU8() : payload.getData()));
      const action = tx.getActionsList()[0];
      if (action) {
        const cap = protos.ChaincodeActionPayload.deserializeBinary(
          buf(action.getPayload_asU8 ? action.getPayload_asU8() : action.getPayload()));

        // proposal side: chaincode name, function and arguments
        try {
          const prop = protos.ChaincodeProposalPayload.deserializeBinary(
            buf(cap.getChaincodeProposalPayload_asU8 ? cap.getChaincodeProposalPayload_asU8() : cap.getChaincodeProposalPayload()));
          const spec = protos.ChaincodeInvocationSpec.deserializeBinary(
            buf(prop.getInput_asU8 ? prop.getInput_asU8() : prop.getInput())).getChaincodeSpec();
          if (spec) {
            if (spec.getChaincodeId()) out.chaincode = spec.getChaincodeId().getName();
            const input = spec.getInput();
            if (input) {
              const args = (input.getArgsList_asU8 ? input.getArgsList_asU8() : input.getArgsList())
                .map((a) => buf(a).toString('utf8'));
              out.function = args[0];
              out.args = args.slice(1);
            }
          }
        } catch { /* args stay unknown */ }

        // response side: status and endorsement count
        try {
          const cae = cap.getAction();
          if (cae) {
            out.endorsements = cae.getEndorsementsList().length;
            const prp = protos.ProposalResponsePayload.deserializeBinary(
              buf(cae.getProposalResponsePayload_asU8 ? cae.getProposalResponsePayload_asU8() : cae.getProposalResponsePayload()));
            const ext = protos.ChaincodeAction.deserializeBinary(
              buf(prp.getExtension_asU8 ? prp.getExtension_asU8() : prp.getExtension()));
            if (!out.chaincode && ext.getChaincodeId()) out.chaincode = ext.getChaincodeId().getName();
            const resp = ext.getResponse();
            if (resp) out.status = resp.getStatus();
          }
        } catch { /* response stays unknown */ }
      }
    }
  } catch (err) {
    out.error = String(err.message || err);
  }
  return out;
}

function decodeBlock(bytes) {
  const { common } = protosLib;
  const block = common.Block.deserializeBinary(buf(bytes));
  const header = block.getHeader();

  const meta = block.getMetadata();
  const list = meta ? meta.getMetadataList() : [];
  const filter = buf(list[2] || []);           // TRANSACTIONS_FILTER

  const envelopes = block.getData() ? block.getData().getDataList() : [];
  const txs = envelopes.map((e, i) => decodeEnvelope(e, filter[i] ?? 255));

  return {
    number: Number(callAny(header, ['getNumber']) ?? 0),
    dataHash: hex(callAny(header, ['getDataHash_asU8', 'getDatahash_asU8', 'getDataHash', 'getDatahash'])),
    previousHash: hex(callAny(header, ['getPreviousHash_asU8', 'getPrevioushash_asU8', 'getPreviousHash', 'getPrevioushash'])),
    txCount: txs.length,
    committedCount: txs.filter((t) => t.valid).length,
    timestamp: txs.find((t) => t.timestamp)?.timestamp || null,
    transactions: txs,
  };
}

/* ── qscc access ──────────────────────────────────────────────── */
async function qscc(channel, fn, ...args) {
  return withGateway(1, async (gateway) => {
    const network = gateway.getNetwork(channel);
    const contract = network.getContract('qscc');
    return contract.evaluateTransaction(fn, ...args);
  });
}

/* protoc-gen-js derives getter names from the raw proto field name. Fabric's
   BlockchainInfo declares `currentBlockHash` in camelCase rather than the usual
   snake_case, so the generated getter is getCurrentblockhash() — lowercase b
   and h. Try every plausible spelling instead of guessing one. */
function callAny(obj, names) {
  for (const n of names) {
    if (obj && typeof obj[n] === 'function') return obj[n]();
  }
  return null;
}

async function chainInfo(channel) {
  let bytes;
  try {
    bytes = await qscc(channel, 'GetChainInfo', channel);
  } catch (gatewayErr) {
    return cliChainInfo(channel);   // discovery cannot see qscc — read it from the peer
  }
  const { common } = protosLib;
  const info = common.BlockchainInfo.deserializeBinary(buf(bytes));
  return {
    height: Number(callAny(info, ['getHeight']) ?? 0),
    currentBlockHash: hex(callAny(info, [
      'getCurrentblockhash_asU8', 'getCurrentBlockHash_asU8',
      'getCurrentblockhash', 'getCurrentBlockHash',
    ])),
    previousBlockHash: hex(callAny(info, [
      'getPreviousblockhash_asU8', 'getPreviousBlockHash_asU8',
      'getPreviousblockhash', 'getPreviousBlockHash',
    ])),
  };
}

// blocks change only by being appended, so a decoded block is cacheable
const blockCache = new Map();
const CACHE_MAX = 400;
async function getBlock(channel, number) {
  const key = `${channel}:${number}`;
  if (blockCache.has(key)) return blockCache.get(key);
  let bytes;
  try {
    bytes = await qscc(channel, 'GetBlockByNumber', channel, String(number));
  } catch (gatewayErr) {
    bytes = await cliBlockBytes(channel, number);
  }
  const decoded = decodeBlock(bytes);
  if (blockCache.size >= CACHE_MAX) blockCache.delete(blockCache.keys().next().value);
  blockCache.set(key, decoded);
  return decoded;
}

/* ── CLI fallback ─────────────────────────────────────────────────
   Fabric Gateway resolves an endorsing peer through service discovery.
   Discovery has no entry for system chaincodes, so on some networks
   `qscc` evaluates fail with "no peers available to evaluate chaincode
   qscc". The peer binary talks to its own ledger directly and has no
   such limitation, so it is used whenever the gateway path fails.
   ──────────────────────────────────────────────────────────────── */
const PEER_CONTAINER = process.env.EXPLORER_PEER || 'peer0.org1.example.com';
const PEER_ADDRESS   = process.env.EXPLORER_PEER_ADDRESS || 'peer0.org1.example.com:7051';
const PEER_MSP       = process.env.EXPLORER_PEER_MSP || 'org1MSP';
const ORDERER        = process.env.EXPLORER_ORDERER || 'orderer.example.com:7050';

function dockerExec(args, opts = {}) {
  return new Promise((resolve, reject) => {
    execFile('docker', args, { maxBuffer: 64 * 1024 * 1024, ...opts }, (err, stdout, stderr) => {
      if (err) return reject(new Error(String(stderr || err.message).slice(0, 300)));
      resolve(stdout);
    });
  });
}

const peerEnv = [
  'exec',
  '-e', `CORE_PEER_LOCALMSPID=${PEER_MSP}`,
  '-e', 'CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp',
  '-e', `CORE_PEER_ADDRESS=${PEER_ADDRESS}`,
  '-e', 'CORE_PEER_TLS_ENABLED=false',
  PEER_CONTAINER,
];

// peer channel getinfo → Blockchain info: {"height":N,"currentBlockHash":"b64",...}
async function cliChainInfo(channel) {
  const out = await dockerExec([...peerEnv, 'peer', 'channel', 'getinfo', '-c', channel]);
  const m = out.match(/Blockchain info:\s*(\{.*\})/);
  if (!m) throw new Error(`could not read chain info for ${channel}`);
  const j = JSON.parse(m[1]);
  const toHex = (b64) => (b64 ? Buffer.from(b64, 'base64').toString('hex') : '');
  return {
    height: Number(j.height),
    currentBlockHash: toHex(j.currentBlockHash),
    previousBlockHash: toHex(j.previousBlockHash),
  };
}

// peer channel fetch <n> → protobuf block, handed back base64 so the
// decoder below can treat it exactly like a qscc response
async function cliBlockBytes(channel, number) {
  const file = `/tmp/explorer_${channel}_${number}.block`;
  const script = `peer channel fetch ${number} ${file} -c ${channel} -o ${ORDERER} >/dev/null 2>&1 `
               + `&& base64 -w0 ${file} && rm -f ${file}`;
  const out = await dockerExec([...peerEnv, 'sh', '-c', script]);
  const b64 = out.trim();
  if (!b64) throw new Error(`block ${number} could not be fetched from ${channel}`);
  return Buffer.from(b64, 'base64');
}

function guard(req, res) {
  if (protosLib) return false;
  res.status(500).json({ error: protosError });
  return true;
}

/* ── routes ───────────────────────────────────────────────────── */
router.get('/summary', async (req, res) => {
  if (guard(req, res)) return;
  const names = Object.keys(CHANNEL_CHAINCODE_MAP);
  const channels = await Promise.all(names.map(async (channel) => {
    try {
      const info = await chainInfo(channel);
      return { channel, deployed: true, ...info };
    } catch (err) {
      const raw = String(err.message || err);
      const missing = /bad response|does not exist|no such channel|LedgerID .* does not exist/i.test(raw);
      return {
        channel,
        deployed: false,
        height: 0,
        reason: missing
          ? 'Channel has not been created on this peer yet.'
          : raw.replace(/\u001b\[[0-9;]*m/g, '').replace(/\s+/g, ' ').trim().slice(0, 160),
      };
    }
  }));
  const live = channels.filter((c) => c.deployed);
  res.json({
    channels,
    totals: {
      channelsDeployed: live.length,
      channelsTotal: channels.length,
      blocks: live.reduce((s, c) => s + c.height, 0),
    },
  });
});

router.get('/blocks', async (req, res) => {
  if (guard(req, res)) return;
  try {
    const channel = String(req.query.channel || '');
    if (!channel) return res.status(400).json({ error: 'channel is required' });
    const limit = Math.max(1, Math.min(50, Number(req.query.limit) || 12));

    const info = await chainInfo(channel);
    if (info.height === 0) return res.json({ channel, ...info, blocks: [] });

    const top = req.query.before != null && req.query.before !== ''
      ? Math.min(Number(req.query.before) - 1, info.height - 1)
      : info.height - 1;
    if (top < 0) return res.json({ channel, ...info, blocks: [] });

    const numbers = [];
    for (let n = top; n > top - limit && n >= 0; n--) numbers.push(n);

    const blocks = await Promise.all(numbers.map(async (n) => {
      const b = await getBlock(channel, n);
      const { transactions, ...summary } = b;
      return {
        ...summary,
        chaincodes: [...new Set(transactions.map((t) => t.chaincode).filter(Boolean))],
        submitters: [...new Set(transactions.map((t) => t.submitter).filter(Boolean))],
      };
    }));

    res.json({ channel, ...info, oldest: numbers[numbers.length - 1], blocks });
  } catch (err) {
    res.status(500).json({ error: String(err.message || err) });
  }
});

router.get('/block', async (req, res) => {
  if (guard(req, res)) return;
  try {
    const channel = String(req.query.channel || '');
    const number = Number(req.query.number);
    if (!channel || !Number.isFinite(number)) {
      return res.status(400).json({ error: 'channel and number are required' });
    }
    res.json(await getBlock(channel, number));
  } catch (err) {
    res.status(500).json({ error: String(err.message || err) });
  }
});

router.get('/tx', async (req, res) => {
  if (guard(req, res)) return;
  try {
    const channel = String(req.query.channel || '');
    const id = String(req.query.id || '');
    if (!channel || !id) return res.status(400).json({ error: 'channel and id are required' });

    let block;
    try {
      block = decodeBlock(await qscc(channel, 'GetBlockByTxID', channel, id));
    } catch (gatewayErr) {
      // no qscc: scan back from the head until the id turns up
      const info = await chainInfo(channel);
      block = null;
      for (let n = info.height - 1; n >= 0 && n > info.height - 1 - 200; n--) {
        const b = await getBlock(channel, n);
        if (b.transactions.some((t) => t.txId === id)) { block = b; break; }
      }
      if (!block) return res.status(404).json({ error: 'transaction not found in the last 200 blocks' });
    }
    const tx = block.transactions.find((t) => t.txId === id);
    if (!tx) return res.status(404).json({ error: 'transaction not found in the returned block' });
    res.json({ ...tx, blockNumber: block.number });
  } catch (err) {
    res.status(500).json({ error: String(err.message || err) });
  }
});

module.exports = router;
