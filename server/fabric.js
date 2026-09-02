'use strict';

const { withGateway } = require('./connection');

// Channel → Chaincode mapping based on channel_contract_map.sh
const CHANNEL_CHAINCODE_MAP = {
  networkchannel: ['LocationBasedNetworkLoad', 'LocationBasedNetworkHealth', 'ManageNetwork', 'MonitorNetwork'],
  resourcechannel: ['LocationBasedResourceAllocation', 'LocationBasedIoTResource', 'AllocateResource', 'LogResourceAudit', 'MonitorResourceUsage'],
  performancechannel: ['LocationBasedLatency', 'LogPerformance', 'LogNetworkPerformance', 'LogPerformanceAudit'],
  iotchannel: ['LocationBasedIoTConnection', 'LocationBasedIoTBandwidth', 'LocationBasedIoTStatus', 'LocationBasedIoTFault', 'LocationBasedIoTSession', 'ManageIoTDevice', 'MonitorIoT', 'LogIoTActivity'],
  authchannel: ['LocationBasedIoTAuthentication', 'AuthenticateUser', 'AuthenticateIoT', 'VerifyIdentity'],
  connectivitychannel: ['LocationBasedConnection', 'LocationBasedRoaming', 'ConnectUser', 'ConnectIoT', 'LogConnectionAudit'],
  sessionchannel: ['LocationBasedSessionManagement', 'LocationBasedIoTSession', 'ManageSession', 'LogSession', 'LogSessionAudit'],
  policychannel: ['SetPolicy', 'GetPolicy', 'UpdatePolicy', 'LogPolicyAudit', 'LogPolicyChange'],
  auditchannel: ['LogNetworkAudit', 'LogAntennaAudit', 'LogIoTAudit', 'LogUserAudit', 'LogAccessAudit', 'LogSecurityAudit', 'LogComplianceAudit'],
  securitychannel: ['EncryptData', 'DecryptData', 'SecureCommunication', 'LogSecurityEvent'],
  datachannel: ['LocationBasedAssignment', 'LocationBasedBandwidth', 'LocationBasedSignalStrength', 'LocationBasedSignalQuality'],
  analyticschannel: ['LocationBasedQoS', 'LocationBasedCoverage', 'LocationBasedEnergy'],
  monitoringchannel: ['MonitorTraffic', 'MonitorInterference', 'LocationBasedStatus'],
  managementchannel: ['ManageAntenna', 'ManageUser', 'LocationBasedAntennaConfig', 'LocationBasedPowerManagement', 'LocationBasedChannelAllocation'],
  optimizationchannel: ['OptimizeNetwork', 'BalanceLoad', 'LocationBasedDynamicRouting'],
  faultchannel: ['LocationBasedFault', 'LocationBasedIoTFault', 'LogFault'],
  trafficchannel: ['LocationBasedTraffic', 'LogTraffic', 'LocationBasedCongestion'],
  accesschannel: ['RegisterUser', 'RegisterIoT', 'RevokeUser', 'RevokeIoT', 'AssignRole', 'LocationBasedIoTRegistration', 'LocationBasedIoTRevocation', 'LogAccessControl'],
  compliancechannel: ['LogComplianceAudit', 'LocationBasedPriority'],
  integrationchannel: ['LocationBasedInterference', 'LocationBasedSignalStrength', 'LocationBasedUserActivity', 'LogUserActivity', 'LogInterference'],
};

// ── نگاشت «عملیات تست/دمو» هر کانال به قرارداد و تابع واقعی ──
// انتخاب‌ها: تابع نوشتنی بدون وابستگی به رکورد آنتنِ از قبل موجود (blind write).
// buildArgs(id, data) آرگومان‌های تابع را می‌سازد؛ id کلید رکورد است.
const CHANNEL_TEST_FN = {
  networkchannel:      { chaincode: 'LocationBasedNetworkLoad',    fn: 'RecordNetworkLoad',    buildArgs: (id, d = {}) => [id, String(d.load ?? 55), String(d.x ?? 10), String(d.y ?? 20)] },
  resourcechannel:     { chaincode: 'AllocateResource',            fn: 'Allocate',             buildArgs: (id, d = {}) => [id, String(d.resource ?? 'spectrum'), String(d.amount ?? 100)] },
  performancechannel:  { chaincode: 'LogPerformance',              fn: 'LogPerformance',       buildArgs: (id, d = {}) => [id, String(d.metric ?? 'latency'), String(d.value ?? 12)] },
  iotchannel:          { chaincode: 'LocationBasedIoTStatus',      fn: 'UpdateIoTStatus',      buildArgs: (id, d = {}) => [id, String(d.status ?? 'Active'), String(d.x ?? 10), String(d.y ?? 20)] },
  authchannel:         { chaincode: 'AuthenticateUser',            fn: 'Authenticate',         buildArgs: (id, d = {}) => [id, String(d.token ?? 'token-abc')] },
  connectivitychannel: { chaincode: 'ConnectUser',                 fn: 'Connect',              buildArgs: (id, d = {}) => [id, String(d.antennaID ?? 'antenna-1')] },
  sessionchannel:      { chaincode: 'ManageSession',               fn: 'StartSession',         buildArgs: (id, d = {}) => [id, String(d.sessionID ?? `sess-${Date.now()}`)] },
  policychannel:       { chaincode: 'SetPolicy',                   fn: 'Set',                  buildArgs: (id, d = {}) => [id, String(d.policy ?? 'allow-all')] },
  auditchannel:        { chaincode: 'LogNetworkAudit',             fn: 'Log',                  buildArgs: (id, d = {}) => [id, String(d.action ?? 'config-change')] },
  securitychannel:     { chaincode: 'LogSecurityEvent',            fn: 'Log',                  buildArgs: (id, d = {}) => [id, String(d.event ?? 'login-ok')] },
  datachannel:         { chaincode: 'LocationBasedSignalStrength', fn: 'RecordSignalStrength', buildArgs: (id, d = {}) => [id, String(d.signal ?? -70), String(d.x ?? 10), String(d.y ?? 20)] },
  analyticschannel:    { chaincode: 'LocationBasedCoverage',       fn: 'RecordCoverage',       buildArgs: (id, d = {}) => [id, String(d.coverage ?? 85), String(d.x ?? 10), String(d.y ?? 20)] },
  monitoringchannel:   { chaincode: 'MonitorTraffic',              fn: 'RecordTraffic',        buildArgs: (id, d = {}) => [id, String(d.traffic ?? 1200)] },
  managementchannel:   { chaincode: 'ManageAntenna',               fn: 'UpdateAntennaStatus',  buildArgs: (id, d = {}) => [id, String(d.status ?? 'Active')] },
  optimizationchannel: { chaincode: 'OptimizeNetwork',             fn: 'Optimize',             buildArgs: (id, d = {}) => [id, String(d.strategy ?? 'load-balance')] },
  faultchannel:        { chaincode: 'LogFault',                    fn: 'LogFault',             buildArgs: (id, d = {}) => [id, String(d.faultType ?? 'link-down')] },
  trafficchannel:      { chaincode: 'LogTraffic',                  fn: 'LogTraffic',           buildArgs: (id, d = {}) => [id, String(d.traffic ?? 850)] },
  accesschannel:       { chaincode: 'RegisterIoT',                 fn: 'Register',             buildArgs: (id, d = {}) => [id, String(d.status ?? 'Active')] },
  compliancechannel:   { chaincode: 'LocationBasedPriority',       fn: 'AssignPriority',       buildArgs: (id, d = {}) => [id, String(d.priority ?? 'high'), String(d.x ?? 10), String(d.y ?? 20)] },
  integrationchannel:  { chaincode: 'LocationBasedUserActivity',   fn: 'RecordUserActivity',   buildArgs: (id, d = {}) => [id, String(d.activity ?? 'handover'), String(d.x ?? 10), String(d.y ?? 20)] },
};

// Generic query function with automatic resource cleanup
async function queryChaincode(orgNum, channelName, chaincodeName, functionName, args = []) {
  return withGateway(orgNum, async (gateway) => {
    const network = gateway.getNetwork(channelName);
    const contract = network.getContract(chaincodeName);
    const resultBytes = await contract.evaluateTransaction(functionName, ...args);
    const resultString = Buffer.from(resultBytes).toString('utf8');
    return resultString ? JSON.parse(resultString) : null;
  });
}

// invoke از طریق gateway. سیاست endorsement این قراردادها در عمل «یک سازمان»
// است (تأییدشده: invoke بدون --peerAddresses فقط با endorse org1 مقدار VALID می‌گیرد)،
// پس endorsingOrganizations را مشخص نمی‌کنیم تا gateway از peer در دسترس خودش
// (همان سازمان کلاینت) endorse بگیرد. تعیین صریح همه ۸ سازمان بدون anchor peer
// به «failed to find any endorsing peers» منجر می‌شد، چون gossip/discovery بین
// سازمان‌ها برقرار نیست.
async function invokeChaincode(orgNum, channelName, chaincodeName, functionName, args = []) {
  return withGateway(orgNum, async (gateway) => {
    const network = gateway.getNetwork(channelName);
    const contract = network.getContract(chaincodeName);
    const resultBytes = await contract.submitTransaction(functionName, ...args);
    const resultString = Buffer.from(resultBytes).toString('utf8');
    return resultString ? JSON.parse(resultString) : { success: true };
  });
}

// Helper: Get primary chaincode for a channel (first in list)
function getChaincodeForChannel(channelName) {
  const chaincodes = CHANNEL_CHAINCODE_MAP[channelName];
  if (!chaincodes || chaincodes.length === 0) {
    throw new Error(`No chaincode mapping found for channel: ${channelName}`);
  }
  return chaincodes[0];
}

// Helper: Get all chaincodes for a channel
function getAllChaincodesForChannel(channelName) {
  const chaincodes = CHANNEL_CHAINCODE_MAP[channelName];
  if (!chaincodes) {
    throw new Error(`No chaincode mapping found for channel: ${channelName}`);
  }
  return chaincodes;
}

function getTestOp(channelName) {
  const op = CHANNEL_TEST_FN[channelName];
  if (!op) throw new Error(`No test operation defined for channel: ${channelName}`);
  return op;
}

// ── High-level API — نگاشت‌شده به توابع واقعی قراردادهای تولیدی ──
// همه ۸۶ قرارداد این دو تابع خواندن را دارند: QueryAsset(id) و QueryAllAssets()

// Query all assets on a channel (real fn: QueryAllAssets)
async function getAllAssets(orgNum, channelName, chaincodeName = null) {
  const cc = chaincodeName || getTestOp(channelName).chaincode;
  return queryChaincode(orgNum, channelName, cc, 'QueryAllAssets');
}

// Query a single asset by ID (real fn: QueryAsset)
async function getAsset(orgNum, channelName, assetId, chaincodeName = null) {
  const cc = chaincodeName || getTestOp(channelName).chaincode;
  return queryChaincode(orgNum, channelName, cc, 'QueryAsset', [assetId]);
}

// Create a record via the channel's real write function.
// assetData: { ID, ...fields } — فیلدهای اضافی به buildArgs همان کانال پاس می‌شوند.
async function createAsset(orgNum, channelName, assetData = {}, chaincodeName = null) {
  const op = getTestOp(channelName);
  const cc = chaincodeName || op.chaincode;
  const id = assetData.ID || assetData.id || `asset-${Date.now()}`;
  return invokeChaincode(orgNum, channelName, cc, op.fn, op.buildArgs(id, assetData));
}

// Update = re-record با همان کلید (قراردادها blind write هستند؛ نسخه جدید جایگزین می‌شود)
async function updateAsset(orgNum, channelName, assetId, assetData = {}, chaincodeName = null) {
  const op = getTestOp(channelName);
  const cc = chaincodeName || op.chaincode;
  return invokeChaincode(orgNum, channelName, cc, op.fn, op.buildArgs(assetId, assetData));
}

// عملیات‌های زیر در قراردادهای تولیدی 6G وجود ندارند — خطای شفاف به جای خطای گنگ chaincode
async function deleteAsset() {
  throw new Error('Delete is not supported by the 6G contracts (no Delete function in any of the 86 chaincodes)');
}
async function transferAsset() {
  throw new Error('Transfer is not supported by the 6G contracts');
}
async function getAssetHistory() {
  throw new Error('History is not supported by the 6G contracts (no GetHistoryForKey wrapper implemented)');
}

module.exports = {
  CHANNEL_CHAINCODE_MAP,
  CHANNEL_TEST_FN,
  queryChaincode,
  invokeChaincode,
  getChaincodeForChannel,
  getAllChaincodesForChannel,
  getTestOp,
  getAllAssets,
  getAsset,
  createAsset,
  updateAsset,
  deleteAsset,
  transferAsset,
  getAssetHistory,
};
