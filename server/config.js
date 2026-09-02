'use strict';

const path = require('path');

// Central configuration for the 6G Fabric dashboard server.
// Values are grounded in network.sh / deploy scripts:
//   - MSP IDs: org1MSP ... org8MSP
//   - Peer ports: 7051, 8051, 9051, 10051, 11051, 12051, 13051, 14051
//   - Orderer: orderer.example.com:7050
//   - Peer-side TLS: disabled (CORE_PEER_TLS_ENABLED=false)
//   - Network: shiraz-network

const CRYPTO_BASE =
  process.env.CRYPTO_BASE ||
  path.resolve(__dirname, '..', 'config', 'crypto-config');

// Build the peerOrganizations MSP path for a given org number.
function orgMspPath(orgNum) {
  const domain = `org${orgNum}.example.com`;
  return path.join(
    CRYPTO_BASE,
    'peerOrganizations',
    domain,
    'users',
    `Admin@${domain}`,
    'msp'
  );
}

// Eight organizations, each with its own peer0 endpoint.
const organizations = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => {
  const domain = `org${n}.example.com`;
  const peerPort = 6000 + (n * 1000) + 51; // 7051, 8051, ..., 14051
  
  return {
    orgNum: n,
    name: `Org${n}`,
    mspId: `org${n}MSP`,
    domain,
    peerEndpoint: `peer0.${domain}:${peerPort}`,
    peerHostAlias: `peer0.${domain}`,
    tlsRootCert: path.join(
      CRYPTO_BASE,
      'peerOrganizations',
      domain,
      'peers',
      `peer0.${domain}`,
      'tls',
      'ca.crt'
    ),
    adminCertDir: path.join(orgMspPath(n), 'signcerts'),
    adminKeyDir: path.join(orgMspPath(n), 'keystore'),
    mspPath: orgMspPath(n),
  };
});

module.exports = {
  port: process.env.PORT || 3000,

  // Peer-side TLS is disabled per lifecycle commands in network.sh.
  tlsEnabled: process.env.CORE_PEER_TLS_ENABLED === 'true',

  orderer: {
    address: process.env.ORDERER_ADDRESS || 'orderer.example.com',
    port: parseInt(process.env.ORDERER_PORT) || 7050,
    get endpoint() {
      return `${this.address}:${this.port}`;
    },
    tlsCaCert: path.join(
      CRYPTO_BASE,
      'ordererOrganizations',
      'example.com',
      'orderers',
      'orderer.example.com',
      'msp',
      'tlscacerts',
      'tlsca.example.com-cert.pem'
    ),
  },

  dockerNetwork: 'shiraz-network',
  cryptoBase: CRYPTO_BASE,
  organizations,

  // Helper lookups
  getOrg(orgNum) {
    return organizations.find((o) => o.orgNum === Number(orgNum));
  },
  getOrgByMsp(mspId) {
    return organizations.find((o) => o.mspId === mspId);
  },
  
  // Get all org MSP IDs
  getAllMspIds() {
    return organizations.map((o) => o.mspId);
  },
};
