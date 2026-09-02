#!/bin/bash

set -e

# ========================================
# Configuration Variables
# ========================================
DASHBOARD_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
SERVER_DIR="${DASHBOARD_DIR}/server"
TEST_DIR="${DASHBOARD_DIR}/test-tools"
CALIPER_WORKSPACE="${TEST_DIR}/caliper-workspace"
TAPE_CONFIG_DIR="${TEST_DIR}/tape-configs"

# Tape binary (real Hyperledger tape, NOT the npm one)
TAPE_BIN="$HOME/go/bin/tape"

# Network configuration
ORGS=("org1" "org2" "org3" "org4" "org5" "org6" "org7" "org8")
MSP_IDS=("org1MSP" "org2MSP" "org3MSP" "org4MSP" "org5MSP" "org6MSP" "org7MSP" "org8MSP")
PEER_PORTS=(7051 8051 9051 10051 11051 12051 13051 14051)
ORDERER_PORT=7050

# Channels from channel_contract_map.sh
CHANNELS=(
    "networkchannel" "resourcechannel" "performancechannel" "iotchannel"
    "authchannel" "connectivitychannel" "sessionchannel" "policychannel"
    "auditchannel" "securitychannel" "datachannel" "analyticschannel"
    "monitoringchannel" "managementchannel" "optimizationchannel"
    "faultchannel" "trafficchannel" "accesschannel" "compliancechannel"
    "integrationchannel"
)

# Main contracts per channel
declare -A CHANNEL_CONTRACTS=(
    ["networkchannel"]="LocationBasedNetworkLoad"
    ["resourcechannel"]="AllocateResource"
    ["performancechannel"]="LogPerformance"
    ["iotchannel"]="LocationBasedIoTStatus"
    ["authchannel"]="AuthenticateUser"
    ["connectivitychannel"]="ConnectUser"
    ["sessionchannel"]="ManageSession"
    ["policychannel"]="SetPolicy"
    ["auditchannel"]="LogNetworkAudit"
    ["securitychannel"]="LogSecurityEvent"
    ["datachannel"]="LocationBasedSignalStrength"
    ["analyticschannel"]="LocationBasedCoverage"
    ["monitoringchannel"]="MonitorTraffic"
    ["managementchannel"]="ManageAntenna"
    ["optimizationchannel"]="OptimizeNetwork"
    ["faultchannel"]="LogFault"
    ["trafficchannel"]="LogTraffic"
    ["accesschannel"]="RegisterIoT"
    ["compliancechannel"]="LocationBasedPriority"
    ["integrationchannel"]="LocationBasedUserActivity"
)

# Real test function + args per channel (verified against generated chaincode)
declare -A CHANNEL_TEST_ARGS=(
    ["networkchannel"]="RecordNetworkLoad|net-1|55|10|20"
    ["resourcechannel"]="Allocate|res-1|spectrum|100"
    ["performancechannel"]="LogPerformance|perf-1|latency|12"
    ["iotchannel"]="UpdateIoTStatus|iot-1|Active|10|20"
    ["authchannel"]="Authenticate|user-1|token-abc"
    ["connectivitychannel"]="Connect|user-1|antenna-1"
    ["sessionchannel"]="StartSession|user-1|sess-1"
    ["policychannel"]="Set|pol-1|allow-all"
    ["auditchannel"]="Log|net-1|config-change"
    ["securitychannel"]="Log|node-1|login-ok"
    ["datachannel"]="RecordSignalStrength|sig-1|-70|10|20"
    ["analyticschannel"]="RecordCoverage|cov-1|85|10|20"
    ["monitoringchannel"]="RecordTraffic|net-1|1200"
    ["managementchannel"]="UpdateAntennaStatus|antenna-1|Active"
    ["optimizationchannel"]="Optimize|net-1|load-balance"
    ["faultchannel"]="LogFault|node-1|link-down"
    ["trafficchannel"]="LogTraffic|node-1|850"
    ["accesschannel"]="Register|dev-1|Active"
    ["compliancechannel"]="AssignPriority|ent-1|high|10|20"
    ["integrationchannel"]="RecordUserActivity|user-1|handover|10|20"
)

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Installing Performance Testing Tools${NC}"
echo -e "${GREEN}for 8-Organization Fabric Network${NC}"
echo -e "${GREEN}========================================${NC}"

# ========================================
# Crypto Material Detection Helpers
# ========================================

detect_crypto_base() {
    local candidates=(
        "${DASHBOARD_DIR}/config/crypto-config"
        "${DASHBOARD_DIR}/organizations"
        "${DASHBOARD_DIR}/crypto-config"
        "${SERVER_DIR}/config/crypto-config"
    )
    for c in "${candidates[@]}"; do
        if [ -d "$c" ]; then
            echo "$c"
            return 0
        fi
    done
    echo ""
}

find_user_dir() {
    local org="$1"
    local base="$2"
    local users_dir="${base}/peerOrganizations/${org}.example.com/users"

    if [ -d "${users_dir}/Admin@${org}.example.com" ]; then
        echo "${users_dir}/Admin@${org}.example.com"
    elif [ -d "${users_dir}/User1@${org}.example.com" ]; then
        echo "${users_dir}/User1@${org}.example.com"
    else
        ls -d "${users_dir}"/*/ 2>/dev/null | head -1 | sed 's:/*$::'
    fi
}

find_private_key() {
    local user_dir="$1"
    local keystore="${user_dir}/msp/keystore"
    local key=""
    key=$(ls "${keystore}"/*_sk 2>/dev/null | head -1)
    [ -z "$key" ] && key=$(ls "${keystore}"/priv_sk 2>/dev/null | head -1)
    [ -z "$key" ] && key=$(ls "${keystore}"/* 2>/dev/null | head -1)
    echo "$key"
}

find_sign_cert() {
    local user_dir="$1"
    local signcerts="${user_dir}/msp/signcerts"
    ls "${signcerts}"/*.pem 2>/dev/null | head -1
}

# ========================================
# Prerequisites Check
# ========================================
echo -e "\n${YELLOW}Checking prerequisites...${NC}"

if ! command -v node &> /dev/null; then
    echo -e "${RED}Node.js not found. Please install Node.js 18+${NC}"
    exit 1
fi

NODE_VERSION=$(node -v | cut -d'v' -f2 | cut -d'.' -f1)
if [ "$NODE_VERSION" -lt 18 ]; then
    echo -e "${RED}Node.js version must be 18 or higher (current: $(node -v))${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Node.js $(node -v) detected${NC}"

if ! command -v npm &> /dev/null; then
    echo -e "${RED}npm not found${NC}"
    exit 1
fi
echo -e "${GREEN}✓ npm $(npm -v) detected${NC}"

CRYPTO_BASE=$(detect_crypto_base)
if [ -z "$CRYPTO_BASE" ]; then
    echo -e "${YELLOW}⚠ crypto-config directory not found automatically.${NC}"
    echo -e "${YELLOW}  Configs will use a default path; verify them before running tests.${NC}"
    CRYPTO_BASE="${DASHBOARD_DIR}/config/crypto-config"
else
    echo -e "${GREEN}✓ Crypto material base: ${CRYPTO_BASE}${NC}"
fi

# tape روی هاست اجرا می‌شود؛ نام peerها باید از هاست resolve شوند (پورت‌ها publish هستند).
# این بلوک قبلاً داخل شاخهٔ «crypto یافت شد» تو‌رفته بود و در حالت نیافتن هرگز اجرا نمی‌شد.
if ! grep -q "peer0.org1.example.com" /etc/hosts; then
    {
        echo "127.0.0.1 orderer.example.com"
        for i in 1 2 3 4 5 6 7 8; do
            echo "127.0.0.1 peer0.org${i}.example.com"
        done
    } >> /etc/hosts
    echo -e "${GREEN}✓ Fabric hostnames added to /etc/hosts${NC}"
else
    echo -e "${GREEN}✓ Fabric hostnames already in /etc/hosts${NC}"
fi

# ========================================
# Create Directory Structure
# ========================================
echo -e "\n${YELLOW}Creating test directory structure...${NC}"
mkdir -p "${TEST_DIR}"
mkdir -p "${CALIPER_WORKSPACE}"/{benchmarks,networks,workload}
# سازگاری با مسیرهای مورد انتظار server/index.js (test-tools/caliper/{networks,benchmarks,workloads})
ln -sfn "${CALIPER_WORKSPACE}" "${TEST_DIR}/caliper"
ln -sfn workload "${CALIPER_WORKSPACE}/workloads"
mkdir -p "${TAPE_CONFIG_DIR}"
mkdir -p "${SERVER_DIR}/test"/{unit,integration}
echo -e "${GREEN}✓ Directory structure created${NC}"

# ========================================
# Install Hyperledger Caliper
# ========================================
echo -e "\n${YELLOW}Installing Hyperledger Caliper...${NC}"

cd "${SERVER_DIR}"

# npm نمی‌تواند روی نصب سراسری نیمه‌کاره بنویسد: پوشه قدیمی را rename می‌کند و
# اگر خالی نباشد با ENOTEMPTY می‌ایستد. پاک‌سازی پیش از نصب همین را حل می‌کند.
# نکته مهم: خروجی npm پیش از این بررسی نمی‌شد، پس شکست نصب بی‌صدا رد می‌شد و
# همه گام‌های بعدی روی نصب ناقص اجرا می‌شدند.
CALIPER_GLOBAL="$(npm root -g 2>/dev/null)/@hyperledger"
if [ -d "${CALIPER_GLOBAL}" ]; then
    # بازمانده‌های تلاش ناموفق قبلی
    rm -rf "${CALIPER_GLOBAL}"/.caliper-cli-* 2>/dev/null
fi

install_caliper() {
    npm install -g --unsafe-perm @hyperledger/caliper-cli@0.6.0 2>&1
}

CALIPER_LOG="$(install_caliper)"
CALIPER_RC=$?

if [ $CALIPER_RC -ne 0 ] && echo "$CALIPER_LOG" | grep -q "ENOTEMPTY"; then
    echo -e "${YELLOW}نصب قبلی ناقص مانده — پاک‌سازی و تلاش دوباره...${NC}"
    rm -rf "${CALIPER_GLOBAL}/caliper-cli" "${CALIPER_GLOBAL}"/.caliper-cli-* 2>/dev/null
    CALIPER_LOG="$(install_caliper)"
    CALIPER_RC=$?
fi

if [ $CALIPER_RC -ne 0 ]; then
    echo "$CALIPER_LOG" | tail -15
    echo -e "${RED}✗ نصب Caliper ناموفق بود.${NC}"
    echo -e "  بدون آن، بنچمارک Caliper کار نمی‌کند (Tape مستقل است)."
    echo -e "  پاک‌سازی دستی و تلاش دوباره:"
    echo -e "    ${GREEN}rm -rf ${CALIPER_GLOBAL}/caliper-cli ${CALIPER_GLOBAL}/.caliper-cli-*${NC}"
    echo -e "    ${GREEN}npm install -g --unsafe-perm @hyperledger/caliper-cli@0.6.0${NC}"
    exit 1
fi

# نصب موفق را تأیید کن، نه فقط کد خروجی npm را
if ! command -v caliper >/dev/null 2>&1 \
   && [ ! -f "$(npm root -g)/@hyperledger/caliper-cli/caliper.js" ]; then
    echo -e "${RED}✗ npm موفق گزارش داد ولی باینری caliper پیدا نشد.${NC}"
    echo -e "  بررسی: ${GREEN}npm list -g --depth=0 | grep caliper${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Caliper CLI نصب شد${NC}"

echo -e "${YELLOW}Detecting Fabric version...${NC}"
if docker ps | grep -q "peer0.org1.example.com"; then
    FABRIC_VERSION=$(docker exec peer0.org1.example.com peer version 2>/dev/null | grep "Version:" | awk '{print $2}' | cut -d'.' -f1,2)
    echo -e "${GREEN}✓ Detected Fabric version: ${FABRIC_VERSION}${NC}"

    case "${FABRIC_VERSION}" in
        2.5|2.4)
            BIND_VERSION="2.5"
            ;;
        2.3|2.2)
            BIND_VERSION="2.2"
            ;;
        *)
            BIND_VERSION="2.5"
            echo -e "${YELLOW}Unknown version, using default: 2.5${NC}"
            ;;
    esac
else
    echo -e "${YELLOW}Peer container not running, using default bind version 2.5${NC}"
    BIND_VERSION="2.5"
fi

echo -e "${YELLOW}Binding Caliper to Fabric ${BIND_VERSION}...${NC}"
if ! caliper bind --caliper-bind-sut fabric:${BIND_VERSION} --caliper-bind-cwd "${SERVER_DIR}"; then
    echo -e "${RED}✗ caliper bind ناموفق — SDK فابریک نصب نشد.${NC}"
    echo -e "  بدون آن Caliper به شبکه وصل نمی‌شود."
    exit 1
fi
echo -e "${GREEN}✓ Caliper installed and bound to Fabric ${BIND_VERSION}${NC}"

# CLI سراسری است ولی bind فقط در server/node_modules نصب می‌کند؛
# برای اینکه require سراسری SDK ها را ببیند، همان نسخه‌ها را global هم نصب می‌کنیم
npm install -g @hyperledger/fabric-gateway@1.5.0 @grpc/grpc-js@1.10.3 @hyperledger/caliper-core@0.6.0
echo -e "${GREEN}✓ Fabric SDK packages installed globally for the CLI${NC}"

# ========================================
# Generate Caliper Connection Profiles
# ========================================
echo -e "\n${YELLOW}Generating Caliper connection profiles for 8 organizations...${NC}"

for i in "${!ORGS[@]}"; do
    ORG="${ORGS[$i]}"
    MSP="${MSP_IDS[$i]}"
    PORT="${PEER_PORTS[$i]}"

    CONNECTION_PROFILE="${CALIPER_WORKSPACE}/networks/connection-profile-${ORG}.json"

    cat > "${CONNECTION_PROFILE}" << EOF
{
  "name": "shiraz-network-${ORG}",
  "version": "1.0.0",
  "client": {
    "organization": "${MSP}",
    "connection": {
      "timeout": {
        "peer": {
          "endorser": "300"
        },
        "orderer": "300"
      }
    }
  },
  "channels": {
$(
    first=true
    for channel in "${CHANNELS[@]}"; do
        if [ "$first" = true ]; then
            first=false
        else
            echo ","
        fi
        cat << CHANNEL_EOF
    "${channel}": {
      "orderers": [
        "orderer.example.com"
      ],
      "peers": {
        "peer0.${ORG}.example.com": {
          "endorsingPeer": true,
          "chaincodeQuery": true,
          "ledgerQuery": true,
          "eventSource": true
        }
      }
    }
CHANNEL_EOF
    done
)
  },
  "organizations": {
    "${MSP}": {
      "mspid": "${MSP}",
      "peers": [
        "peer0.${ORG}.example.com"
      ],
      "certificateAuthorities": [
        "ca.${ORG}.example.com"
      ]
    }
  },
  "orderers": {
    "orderer.example.com": {
      "url": "grpc://orderer.example.com:${ORDERER_PORT}"
    }
  },
  "peers": {
    "peer0.${ORG}.example.com": {
      "url": "grpc://peer0.${ORG}.example.com:${PORT}"
    }
  },
  "certificateAuthorities": {
    "ca.${ORG}.example.com": {
      "url": "http://ca.${ORG}.example.com:7054",
      "caName": "ca-${ORG}"
    }
  }
}
EOF

    echo -e "${GREEN}✓ Created connection profile: connection-profile-${ORG}.json${NC}"
done

# ========================================
# Generate Caliper Network Config
# ========================================
echo -e "\n${YELLOW}Generating Caliper network configuration...${NC}"

NETWORK_CONFIG="${CALIPER_WORKSPACE}/networks/network-config.yaml"

cat > "${NETWORK_CONFIG}" << 'EOF'
name: 6G-Network-Multi-Org
version: "2.0.0"
caliper:
  blockchain: fabric

channels:
EOF

for channel in "${CHANNELS[@]}"; do
    CONTRACT="${CHANNEL_CONTRACTS[$channel]}"
    cat >> "${NETWORK_CONFIG}" << EOF
  - channelName: ${channel}
    create: false
    contracts:
      - id: ${CONTRACT}
        version: v1
        language: golang
EOF
done

cat >> "${NETWORK_CONFIG}" << 'EOF'

organizations:
EOF

for i in "${!ORGS[@]}"; do
    ORG="${ORGS[$i]}"
    MSP="${MSP_IDS[$i]}"

    USER_DIR=$(find_user_dir "${ORG}" "${CRYPTO_BASE}")
    if [ -n "$USER_DIR" ]; then
        USER_NAME=$(basename "$USER_DIR")
        PRIV_KEY=$(find_private_key "$USER_DIR")
        SIGN_CERT=$(find_sign_cert "$USER_DIR")
    fi

    if [ -z "$USER_DIR" ] || [ -z "$PRIV_KEY" ] || [ -z "$SIGN_CERT" ]; then
        echo -e "${YELLOW}⚠ Crypto material for ${ORG} not fully detected, using conventional paths${NC}"
        USER_NAME="Admin@${ORG}.example.com"
        PRIV_KEY="${CRYPTO_BASE}/peerOrganizations/${ORG}.example.com/users/${USER_NAME}/msp/keystore/priv_sk"
        SIGN_CERT="${CRYPTO_BASE}/peerOrganizations/${ORG}.example.com/users/${USER_NAME}/msp/signcerts/${USER_NAME}-cert.pem"
    else
        echo -e "${GREEN}✓ ${ORG}: user=${USER_NAME}, key detected${NC}"
    fi

    cat >> "${NETWORK_CONFIG}" << EOF
  - mspid: ${MSP}
    identities:
      certificates:
        - name: '${USER_NAME}'
          clientPrivateKey:
            path: '${PRIV_KEY}'
          clientSignedCert:
            path: '${SIGN_CERT}'
    connectionProfile:
      path: './connection-profile-${ORG}.json'
      discover: false
EOF
done

echo -e "${GREEN}✓ Caliper network configuration created${NC}"

# index.js برای هر سازمان networks/orgN.yaml می‌خواهد — همه به network-config.yaml اشاره می‌کنند
for i in 1 2 3 4 5 6 7 8; do
    ln -sfn network-config.yaml "${CALIPER_WORKSPACE}/networks/org${i}.yaml"
done
echo -e "${GREEN}✓ org1..org8 network symlinks created${NC}"

# ── workload و benchmark کالیپر ──
# قبلاً اینجا ۵ فایل با نام سناریو ساخته می‌شد که به caliper/workloads/<SCN>.js
# اشاره داشتند ولی آن فایل‌ها هرگز تولید نمی‌شدند؛ ضمناً server/index.js فایل را
# با نام *تابع واقعی chaincode* می‌جوید نه نام سناریو، پس هیچ‌کدام از ۲۰ کانال از
# رابط وب کار نمی‌کرد. حالا gen-caliper-assets.js یک workload و یک benchmark
# به‌ازای هر تابع نوشتنی شبکه می‌سازد، به‌علاوهٔ دو workload عمومی که روتر
# /api/bench هنگام اجرا برایشان benchmark می‌نویسد.
echo -e "\n${YELLOW}Generating Caliper workloads and benchmarks...${NC}"
node "${DASHBOARD_DIR}/scripts/gen-caliper-assets.js" --workspace "${CALIPER_WORKSPACE}"

# کانفیگ شبکه کالیپر هم از روی همان کاتالوگ ساخته می‌شود. نسخهٔ بش قبلی سه
# ایراد داشت: مسیر نسبی پروفایل اتصال (که کالیپر نسبت به ریشهٔ workspace حل
# می‌کند نه پوشهٔ فایل)، زبان javascript برای chaincode های Go، و اعلام تنها
# یک قرارداد به‌ازای هر کانال — که ۷۰ هدف از ۹۰ هدف بنچمارک را دست‌نیافتنی
# می‌کرد چون کالیپر قراردادی را که به آن معرفی نشده صدا نمی‌زند.
echo -e "\n${YELLOW}Generating Caliper network configuration...${NC}"
node "${DASHBOARD_DIR}/scripts/gen-caliper-network.js" --workspace "${CALIPER_WORKSPACE}"

# ========================================
# Generate Sample Caliper Workload
# ========================================
echo -e "\n${YELLOW}Generating sample Caliper workload modules...${NC}"

cat > "${CALIPER_WORKSPACE}/workload/datachannel-workload.js" << 'EOF'
'use strict';
const { WorkloadModuleBase } = require('@hyperledger/caliper-core');
// RecordSignalStrength(entityID, signal, x, y) — تابع واقعی، blind write، کلید یکتا
class SignalWriteWorkload extends WorkloadModuleBase {
    async initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext) {
        await super.initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext);
        this.keyPrefix = `sig-${workerIndex}-`;
        this.txIndex = 0;
    }
    async submitTransaction() {
        this.txIndex++;
        const request = {
            contractId: this.roundArguments.contractId || 'LocationBasedSignalStrength',
            contractFunction: 'RecordSignalStrength',
            invokerMspId: this.roundArguments.mspId || 'org1MSP',
            contractArguments: [
                `${this.keyPrefix}${this.txIndex}`,
                String(-60 - Math.floor(Math.random() * 40)),
                String(Math.floor(Math.random() * 100)),
                String(Math.floor(Math.random() * 100))
            ],
            readOnly: false
        };
        await this.sutAdapter.sendRequests(request);
    }
}
module.exports.createWorkloadModule = () => new SignalWriteWorkload();
EOF

cat > "${CALIPER_WORKSPACE}/workload/datachannel-query-workload.js" << 'EOF'
'use strict';
const { WorkloadModuleBase } = require('@hyperledger/caliper-core');
// QueryAsset روی کلیدهای نوشته‌شده در دور قبل — بدون خطای not-found
class SignalQueryWorkload extends WorkloadModuleBase {
    async initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext) {
        await super.initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext);
        this.workerIndex = workerIndex;
        this.writesPerWorker = Number(roundArguments.writesPerWorker) || 250;
    }
    async submitTransaction() {
        const idx = 1 + Math.floor(Math.random() * this.writesPerWorker);
        const request = {
            contractId: this.roundArguments.contractId || 'LocationBasedSignalStrength',
            contractFunction: 'QueryAsset',
            invokerMspId: this.roundArguments.mspId || 'org1MSP',
            contractArguments: [`sig-${this.workerIndex}-${idx}`],
            readOnly: true
        };
        await this.sutAdapter.sendRequests(request);
    }
}
module.exports.createWorkloadModule = () => new SignalQueryWorkload();
EOF

cat > "${CALIPER_WORKSPACE}/benchmarks/datachannel-benchmark.yaml" << 'EOF'
test:
  name: Datachannel-Performance-Test
  description: Write/read benchmark with real contract functions (8-org network)
  workers:
    number: 2
  rounds:
    - label: write-signal-strength
      txNumber: 500
      rateControl: { type: fixed-rate, opts: { tps: 20 } }
      workload:
        module: workload/datachannel-workload.js
        arguments: { contractId: LocationBasedSignalStrength, mspId: org1MSP }
    - label: query-signal-strength
      txNumber: 500
      rateControl: { type: fixed-rate, opts: { tps: 50 } }
      workload:
        module: workload/datachannel-query-workload.js
        arguments: { contractId: LocationBasedSignalStrength, mspId: org1MSP, writesPerWorker: 250 }
monitors:
  resource:
    - module: docker
      options:
        interval: 5
        containers: [peer0.org1.example.com, peer0.org2.example.com, orderer.example.com]
EOF

echo -e "${GREEN}✓ Sample Caliper workload created${NC}"

# ========================================
# Install Tape
# ========================================
echo -e "\n${YELLOW}Installing Tape...${NC}"

if ! command -v go &> /dev/null; then
    echo -e "${YELLOW}Go not found. Installing Go 1.21.0...${NC}"
    wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz
    rm go1.21.0.linux-amd64.tar.gz

    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
    echo -e "${GREEN}✓ Go 1.21.0 installed${NC}"
else
    echo -e "${GREEN}✓ Go $(go version | awk '{print $3}') detected${NC}"
fi

echo -e "${YELLOW}Installing Tape from hyperledger-twgc...${NC}"
go install github.com/hyperledger-twgc/tape/cmd/tape@latest

GO_BIN="$(go env GOPATH)/bin"
export PATH=$PATH:${GO_BIN}
if [ -f "${GO_BIN}/tape" ]; then
    TAPE_BIN="${GO_BIN}/tape"
fi

if [ -f "${TAPE_BIN}" ]; then
    echo -e "${GREEN}✓ Tape installed at ${TAPE_BIN}${NC}"
else
    echo -e "${YELLOW}⚠ Tape binary not found at ${TAPE_BIN}. Check '\$(go env GOPATH)/bin'.${NC}"
fi

if command -v tape &> /dev/null; then
    RESOLVED_TAPE=$(command -v tape)
    if [ "${RESOLVED_TAPE}" != "${TAPE_BIN}" ]; then
        echo -e "${YELLOW}⚠ A different 'tape' is first in PATH: ${RESOLVED_TAPE}${NC}"
        echo -e "${YELLOW}  Helper scripts use the absolute path ${TAPE_BIN} to avoid this.${NC}"
    fi
fi

# ========================================
# Generate Tape Configuration
# ========================================
# ── سیاست endorsement برای tape ──
# قرارداد با سیاست OR('org1MSP.member',...,'org8MSP.member') کامیت می‌شود،
# یعنی یک امضا کافی است. اگر tape با MAJORITY (۵ از ۸) بسنجد، همیشه چهار
# امضای اضافه جمع می‌کند که شبکه هرگز نخواسته — تأخیر مصنوعی بالا می‌رود و
# مقایسهٔ Tape با Caliper (که یک امضا می‌گیرد) بی‌اعتبار می‌شود.
# پس پیش‌فرض endorsement-any است؛ majority فقط برای مقایسهٔ عمدی هزینهٔ سیاست.
mkdir -p "${TAPE_CONFIG_DIR}"
cat > "${TAPE_CONFIG_DIR}/endorsement-any.rego" << 'REGO_EOF'
package tape

default allow = false

# مطابق سیاست مستقر: OR('org1MSP.member', ... ,'org8MSP.member')
allow {
    count(input) >= 1
}
REGO_EOF

cat > "${TAPE_CONFIG_DIR}/endorsement-majority.rego" << 'REGO_EOF'
package tape

default allow = false

# سیاست سخت‌گیرانهٔ فرضی: MAJORITY از ۸ سازمان.
# این سیاست روی شبکه مستقر نیست — فقط برای سنجش هزینهٔ یک سیاست سخت‌گیرانه‌تر.
allow {
    count(input) >= 5
}
REGO_EOF

# سازگاری با کانفیگ‌های قدیمی که هنوز به majority.rego اشاره می‌کنند
cp "${TAPE_CONFIG_DIR}/endorsement-any.rego" "${TAPE_CONFIG_DIR}/majority.rego"
TAPE_POLICY_FILE="${TAPE_CONFIG_DIR}/endorsement-any.rego"

echo -e "\n${YELLOW}Generating Tape configuration for 8 organizations...${NC}"

ORG1_USER_DIR=$(find_user_dir "org1" "${CRYPTO_BASE}")
if [ -n "$ORG1_USER_DIR" ]; then
    ORG1_PRIV_KEY=$(find_private_key "$ORG1_USER_DIR")
    ORG1_SIGN_CERT=$(find_sign_cert "$ORG1_USER_DIR")
fi
if [ -z "$ORG1_USER_DIR" ] || [ -z "$ORG1_PRIV_KEY" ] || [ -z "$ORG1_SIGN_CERT" ]; then
    echo -e "${YELLOW}⚠ org1 crypto material not fully detected, using conventional paths${NC}"
    ORG1_PRIV_KEY="${CRYPTO_BASE}/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp/keystore/priv_sk"
    ORG1_SIGN_CERT="${CRYPTO_BASE}/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp/signcerts/Admin@org1.example.com-cert.pem"
fi

TAPE_CONFIG="${TAPE_CONFIG_DIR}/config.yaml"

cat > "${TAPE_CONFIG}" << 'EOF'
# Tape Configuration for 8-Organization Fabric Network
# TLS is DISABLED (grpc://)

# ── گواهی TLS برای Tape ──
# تا وقتی شبکه plaintext بود، tls_ca_cert خالی درست بود. با TLS روشن، Tape
# بدون آن اصلاً وصل نمی‌شود. مسیرها از روی دیسک خوانده می‌شوند چون
# fabric-ca و cryptogen ساختار متفاوتی می‌سازند.
_CRYPTO="${CRYPTO_BASE:-$ROOT_DIR/config/crypto-config}"
_tape_tls() {   # $1 = org (خالی یعنی اوردرر)
    [ "$(grep -oE '^NETWORK_TLS[[:space:]]*=[[:space:]]*\S+' \
        "$ROOT_DIR/config/.env" 2>/dev/null | tr -d ' ' | cut -d= -f2)" = "true" ] || return 0
    if [ -n "$1" ]; then
        find "$_CRYPTO/peerOrganizations/$1.example.com" -path "*msp/tlscacerts/*.pem" 2>/dev/null | head -1
    else
        find "$_CRYPTO/ordererOrganizations" -path "*msp/tlscacerts/*.pem" 2>/dev/null | head -1
    fi
}
_ORD_TLS="$(_tape_tls)"

endorsers:
EOF

for i in "${!ORGS[@]}"; do
    ORG="${ORGS[$i]}"
    PORT="${PEER_PORTS[$i]}"
    cat >> "${TAPE_CONFIG}" << EOF
  - addr: peer0.${ORG}.example.com:${PORT}
    tls_ca_cert: "$(_tape_tls "$ORG")"
    org: ${ORG}
EOF
done

cat >> "${TAPE_CONFIG}" << EOF

committers:
  - addr: peer0.org1.example.com:7051
    tls_ca_cert: "$(_tape_tls org1)"
    org: org1
commitThreshold: 1

orderer:
  addr: orderer.example.com:${ORDERER_PORT}
  tls_ca_cert: "${_ORD_TLS}"
  org: org1

policyFile: ${TAPE_POLICY_FILE}
channel: datachannel
chaincode: LocationBasedSignalStrength
args:
  - RecordSignalStrength
  - tape-dev
  - "-70"
  - "10"
  - "20"
mspid: org1MSP
private_key: ${ORG1_PRIV_KEY}
sign_cert: ${ORG1_SIGN_CERT}
num_of_conn: 8
client_per_conn: 10
EOF

echo -e "${GREEN}✓ Tape configuration created${NC}"

for channel in "${CHANNELS[@]}"; do
    CONTRACT="${CHANNEL_CONTRACTS[$channel]}"
    CHANNEL_TAPE_CONFIG="${TAPE_CONFIG_DIR}/config-${channel}.yaml"

    IFS='|' read -ra _TA <<< "${CHANNEL_TEST_ARGS[$channel]}"
    ARGS_YAML=""
    for _a in "${_TA[@]}"; do ARGS_YAML+="  - \"${_a}\""$'\n'; done

    cat > "${CHANNEL_TAPE_CONFIG}" << CHANNEL_EOF
endorsers:
CHANNEL_EOF

    for i in "${!ORGS[@]}"; do
        ORG="${ORGS[$i]}"
        PORT="${PEER_PORTS[$i]}"
        cat >> "${CHANNEL_TAPE_CONFIG}" << EOF
  - addr: peer0.${ORG}.example.com:${PORT}
    tls_ca_cert: "$(_tape_tls "$ORG")"
    org: ${ORG}
EOF
    done

    cat >> "${CHANNEL_TAPE_CONFIG}" << EOF

committers:
  - addr: peer0.org1.example.com:7051
    tls_ca_cert: "$(_tape_tls org1)"
    org: org1
commitThreshold: 1

orderer:
  addr: orderer.example.com:${ORDERER_PORT}
  tls_ca_cert: "${_ORD_TLS}"
  org: org1

policyFile: ${TAPE_POLICY_FILE}
channel: ${channel}
chaincode: ${CONTRACT}
args:
${ARGS_YAML}mspid: org1MSP
private_key: ${ORG1_PRIV_KEY}
sign_cert: ${ORG1_SIGN_CERT}
num_of_conn: 8
client_per_conn: 10
EOF

    echo -e "${GREEN}✓ Created Tape config for ${channel}${NC}"
done

# ========================================
# Install Node.js Test Dependencies
# ========================================
echo -e "\n${YELLOW}Installing Node.js test dependencies...${NC}"

cd "${SERVER_DIR}"
npm install --save-dev mocha@10 chai@4 chai-http@4 supertest@6 nyc@15 eslint@8 rimraf@5

if [ -f package.json ]; then
    echo -e "${YELLOW}Updating package.json with test scripts...${NC}"

    node << 'NODE_SCRIPT'
const fs = require('fs');
const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));

pkg.scripts = pkg.scripts || {};
pkg.scripts.test = 'mocha server/test/**/*.test.js --timeout 30000';
pkg.scripts['test:unit'] = 'mocha server/test/unit/**/*.test.js';
pkg.scripts['test:integration'] = 'mocha server/test/integration/**/*.test.js --timeout 30000';
pkg.scripts['test:coverage'] = 'nyc npm test';
pkg.scripts['test:watch'] = 'mocha server/test/**/*.test.js --watch';
pkg.scripts.lint = 'eslint server/**/*.js';

fs.writeFileSync('package.json', JSON.stringify(pkg, null, 2));
console.log('✓ package.json updated');
NODE_SCRIPT

fi

cat > "${SERVER_DIR}/test/unit/sample.test.js" << 'EOF'
const { expect } = require('chai');

describe('Sample Test Suite', () => {
    it('should pass basic assertion', () => {
        expect(true).to.be.true;
    });

    it('should perform arithmetic correctly', () => {
        expect(2 + 2).to.equal(4);
    });
});
EOF

echo -e "${GREEN}✓ Test dependencies installed${NC}"

# ========================================
# Create Helper Scripts
# ========================================
echo -e "\n${YELLOW}Creating helper scripts...${NC}"

cat > "${TEST_DIR}/run-caliper.sh" << 'EOF'
#!/bin/bash
cd ${DASHBOARD_DIR}/test-tools/caliper-workspace
npx caliper launch manager \
    --caliper-workspace ./ \
    --caliper-networkconfig networks/network-config.yaml \
    --caliper-benchconfig benchmarks/datachannel-benchmark.yaml \
    --caliper-flow-only-test \
    --caliper-fabric-gateway-enabled
EOF

chmod +x "${TEST_DIR}/run-caliper.sh"

cat > "${TEST_DIR}/run-tape.sh" << EOF
#!/bin/bash

TAPE_BIN="${TAPE_BIN}"

CHANNEL=\${1:-iotchannel}
CONFIG_FILE="/root/shiraz-permit-network/test-tools/tape-configs/config-\${CHANNEL}.yaml"

if [ ! -f "\$TAPE_BIN" ]; then
    echo "Error: tape binary not found at \$TAPE_BIN"
    echo "Run: go install github.com/hyperledger-twgc/tape/cmd/tape@latest"
    exit 1
fi

if [ ! -f "\$CONFIG_FILE" ]; then
    echo "Config file not found: \$CONFIG_FILE"
    echo "Available configs:"
    ls -1 /root/shiraz-permit-network/test-tools/tape-configs/
    exit 1
fi

echo "Running Tape for channel: \$CHANNEL"
"\$TAPE_BIN" -c "\$CONFIG_FILE" -n 1000
EOF

chmod +x "${TEST_DIR}/run-tape.sh"

cat > "${TEST_DIR}/run-multi-org-test.sh" << EOF
#!/bin/bash

TAPE_BIN="${TAPE_BIN}"

if [ ! -f "\$TAPE_BIN" ]; then
    echo "Error: tape binary not found at \$TAPE_BIN"
    exit 1
fi

CHANNELS=("iotchannel" "authchannel" "performancechannel" "securitychannel")

echo "Starting multi-organization load test..."

for CHANNEL in "\${CHANNELS[@]}"; do
    echo "Testing channel: \$CHANNEL"
    "\$TAPE_BIN" -c "/root/shiraz-permit-network/test-tools/tape-configs/config-\${CHANNEL}.yaml" -n 500 &
done

wait
echo "All tests completed"
EOF

chmod +x "${TEST_DIR}/run-multi-org-test.sh"

echo -e "${GREEN}✓ Helper scripts created${NC}"

# ========================================
# Summary
# ========================================
set +e

echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}Installation Summary${NC}"
echo -e "${GREEN}========================================${NC}"

echo -e "\n${YELLOW}Installed Tools:${NC}"
caliper --version 2>/dev/null && echo -e "${GREEN}✓ Caliper installed${NC}" || echo -e "${RED}✗ Caliper not found${NC}"
if [ -f "${TAPE_BIN}" ]; then
    "${TAPE_BIN}" version 2>/dev/null
    echo -e "${GREEN}✓ Tape installed at ${TAPE_BIN}${NC}"
else
    echo -e "${YELLOW}⚠ Tape not found at ${TAPE_BIN}${NC}"
fi

echo -e "\n${YELLOW}Generated Configurations:${NC}"
echo -e "  ${GREEN}✓${NC} 8 Caliper connection profiles (org1-org8)"
echo -e "  ${GREEN}✓${NC} 1 Caliper network config (20 channels)"
echo -e "  ${GREEN}✓${NC} 20 Tape configs (1 per channel)"
echo -e "  ${GREEN}✓${NC} Sample workload and benchmark"

echo -e "\n${YELLOW}Directory Structure:${NC}"
echo -e "  Test tools:        ${TEST_DIR}"
echo -e "  Caliper workspace: ${CALIPER_WORKSPACE}"
echo -e "  Tape configs:      ${TAPE_CONFIG_DIR}"
echo -e "  Crypto base:       ${CRYPTO_BASE}"

echo -e "\n${YELLOW}Quick Start Commands:${NC}"
echo -e "  Run Caliper:       ${TEST_DIR}/run-caliper.sh"
echo -e "  Run Tape:          ${TEST_DIR}/run-tape.sh [channel]"
echo -e "  Multi-org test:    ${TEST_DIR}/run-multi-org-test.sh"
echo -e "  Run unit tests:    cd ${SERVER_DIR} && npm run test:unit"

echo -e "\n${YELLOW}Next Steps:${NC}"
echo -e "  1. ${RED}Run 'source ~/.bashrc'${NC} to load Go/Tape PATH"
echo -e "  2. Verify crypto-config paths in generated configs"
echo -e "  3. Adjust workload parameters in benchmarks/"
echo -e "  4. Update chaincode function names if needed"
echo -e "  5. Run: ${TEST_DIR}/run-caliper.sh"

echo -e "\n${GREEN}Installation complete!${NC}"
echo -e "${YELLOW}Reminder: helper scripts use absolute path ${TAPE_BIN}${NC}"
