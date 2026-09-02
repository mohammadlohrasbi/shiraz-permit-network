'use strict';

const grpc = require('@grpc/grpc-js');
const { connect, signers } = require('@hyperledger/fabric-gateway');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const config = require('./config');

// Reads the single PEM file inside a Fabric-managed directory
// (signcerts / keystore each contain one generated filename).
function readSingleFileInDir(dir) {
  let files;
  try {
    files = fs.readdirSync(dir).filter((f) => !f.startsWith('.'));
  } catch (err) {
    throw new Error(`Cannot read directory ${dir}: ${err.message}`);
  }
  if (files.length === 0) {
    throw new Error(`No files found in directory: ${dir}`);
  }
  // Sort for determinism (Fabric sometimes regenerates keys with new names).
  files.sort();
  return fs.readFileSync(path.join(dir, files[0]));
}

// Creates a gRPC client for a given organization's peer.
// TLS is disabled by default (CORE_PEER_TLS_ENABLED=false in network.sh).
function newGrpcConnection(org) {
  const channelOptions = {
    // Increase max message size to32 MB for large ledger responses.
    'grpc.max_receive_message_length': 32 * 1024 * 1024,
    'grpc.max_send_message_length': 32 * 1024 * 1024,
    // Keepalive: detect dead connections.
    'grpc.keepalive_time_ms': 120_000,
    'grpc.keepalive_timeout_ms': 20_000,
    'grpc.keepalive_permit_without_calls': 1,
  };

  if (config.tlsEnabled) {
    const tlsRootCert = fs.readFileSync(org.tlsRootCert);
    const tlsCredentials = grpc.credentials.createSsl(tlsRootCert);
    return new grpc.Client(org.peerEndpoint, tlsCredentials, {
      ...channelOptions,
      'grpc.ssl_target_name_override': org.peerHostAlias,
    });
  }

  return new grpc.Client(
    org.peerEndpoint,
    grpc.credentials.createInsecure(),
    channelOptions
  );
}

// Builds the X.509 identity for the org admin.
function newIdentity(org) {
  const credentials = readSingleFileInDir(org.adminCertDir);
  return { mspId: org.mspId, credentials };
}

// Builds the signer from the admin's private key.
function newSigner(org) {
  const privateKeyPem = readSingleFileInDir(org.adminKeyDir);
  const privateKey = crypto.createPrivateKey(privateKeyPem);
  return signers.newPrivateKeySigner(privateKey);
}

// Opens a Fabric Gateway connection for the given org number (1–8).
// Returns { gateway, client, org }.
// Caller MUST call gateway.close() and client.close() when done.
async function connectGateway(orgNum) {
  const org = config.getOrg(orgNum);
  if (!org) {
    throw new Error(`Unknown organization number: ${orgNum}`);
  }

  const client = newGrpcConnection(org);

  const gateway = connect({
    client,
    identity: newIdentity(org),
    signer: newSigner(org),
    evaluateOptions:() => ({ deadline: Date.now() + 5_000  }),
    endorseOptions:      () => ({ deadline: Date.now() + 15_000 }),
    submitOptions:       () => ({ deadline: Date.now() + 15_000 }),
    commitStatusOptions: () => ({ deadline: Date.now() + 60_000 }),
  });

  return { gateway, client, org };
}

// Convenience wrapper: opens a gateway, runs fn(gateway, org), then closes.
// Use this to avoid resource leaks in route handlers.
//
//   const result = await withGateway(1, async (gw, org) => {
//     const network = gw.getNetwork('networkchannel');
//     ...
//   });
async function withGateway(orgNum, fn) {
  const { gateway, client, org } = await connectGateway(orgNum);
  try {
    return await fn(gateway, org);
  } finally {
    gateway.close();
    client.close();
  }
}

module.exports = { connectGateway, withGateway };
