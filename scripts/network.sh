#!/bin/bash
# ترجیح docker compose v2 (باگ ContainerConfig در v1 با Docker جدید)
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
else
  COMPOSE="docker-compose"
fi
# /root/shiraz-permit-network/scripts/network.sh
# نسخه نهایی — TLS غیرفعال برای سازگاری با Go TLS stack

ROOT_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
CONFIG_DIR="$ROOT_DIR/config"
CRYPTO_DIR="$CONFIG_DIR/crypto-config"
CHANNEL_DIR="$CONFIG_DIR/channel-artifacts"
PROJECT_DIR="$CONFIG_DIR"
SCRIPTS_DIR="$ROOT_DIR/scripts"
CHAINCODE_DIR="$SCRIPTS_DIR/chaincode"
export FABRIC_CFG_PATH="$CONFIG_DIR"

log() { echo "[$(date +'%Y-%m-%d %H:%M:%S')] $*"; }
success() { log "موفق: $*"; }
error() { log "خطا: $*"; exit 1; }
warn() { log "هشدار: $*"; }

CHANNELS=(networkchannel resourcechannel)
declare -A ORG_PORTS=(
  [1]=7051 [2]=8051 [3]=9051 [4]=10051
  [5]=11051 [6]=12051 [7]=13051 [8]=14051
)

# ------------------- پاک‌سازی -------------------
cleanup() {
  log "شروع پاک‌سازی app containers و volumes..."
  cd "$PROJECT_DIR"
  # حذف app containers (orderer + peers) بدون دست زدن به CAها
  $COMPOSE down -v --remove-orphans 2>/dev/null || true
  # حذف صریح volumeهای peer/orderer برای جلوگیری از ناسازگاری ledger قدیمی
  for v in orderer.example.com peer0.org1.example.com peer0.org2.example.com \
           peer0.org3.example.com peer0.org4.example.com peer0.org5.example.com \
           peer0.org6.example.com peer0.org7.example.com peer0.org8.example.com; do
    docker volume rm "config_${v}" 2>/dev/null || true
    docker volume rm "${v}" 2>/dev/null || true
  done
  rm -rf "$CHANNEL_DIR"/* 2>/dev/null || true
  success "پاک‌سازی کامل شد"
}

setup_network_with_fabric_ca_tls_nodeous_active() {
  log "راه‌اندازی کامل شبکه"

  local CRYPTO_DIR="$PROJECT_DIR/crypto-config"
  local CHANNEL_ARTIFACTS="$PROJECT_DIR/channel-artifacts"
  local TEMP_CRYPTO="$PROJECT_DIR/temp-seed-crypto"
  local ROOT_CA_DIR="$CRYPTO_DIR/root-ca"
  local INTERMEDIATE_DIR="$CRYPTO_DIR/intermediate-ca"

  # پاک کردن کامل قبلی
  $COMPOSE -f docker-compose-rca.yml down -v --remove-orphans 2>/dev/null || true
  $COMPOSE -f docker-compose-root-ca.yml down -v --remove-orphans 2>/dev/null || true
  $COMPOSE down -v 2>/dev/null || true
  docker volume prune -f
  rm -rf "$CRYPTO_DIR" "$CHANNEL_ARTIFACTS" "$TEMP_CRYPTO"
  mkdir -p "$CRYPTO_DIR" "$CHANNEL_ARTIFACTS" "$TEMP_CRYPTO" "$ROOT_CA_DIR" "$INTERMEDIATE_DIR/tls"

  # =====================================================
  # ایجاد شبکه Docker
  # =====================================================
  if ! docker network ls | grep -q "shiraz-network"; then
    log "ایجاد شبکه shiraz-network"
    docker network create shiraz-network
    success "شبکه shiraz-network ساخته شد"
  else
    log "شبکه shiraz-network از قبل وجود دارد"
  fi

  # =====================================================
  # ساخت config برای Root CA — قبل از start
  # =====================================================
  log "ساخت config برای Root CA"
  cat > "$ROOT_CA_DIR/fabric-ca-server-config.yaml" << 'EOF'
port: 7052
debug: true

tls:
  enabled: true

registry:
  maxenrollments: -1
  identities:
    - name: admin
      pass: adminpw
      type: admin
      affiliation: ""
      attrs:
        hf.Registrar.Roles: "*"
        hf.Registrar.DelegateRoles: "*"
        hf.Revoker: true
        hf.IntermediateCA: true
        hf.GenCRL: true
        hf.Registrar.Attributes: "*"
        hf.AffiliationMgr: true

affiliations:
  "":
    - "."
EOF
  success "config Root CA ساخته شد"

  # =====================================================
  # راه‌اندازی Root CA
  # =====================================================
  log "راه‌اندازی Root CA"
  $COMPOSE -f "$PROJECT_DIR/docker-compose-root-ca.yml" down -v --remove-orphans || true
  $COMPOSE -f "$PROJECT_DIR/docker-compose-root-ca.yml" up -d
  sleep 35

  # =====================================================
  # دریافت گواهی TLS برای rca-main از Root CA
  # =====================================================
  log "دریافت گواهی TLS سرور برای rca-main"
  docker run --rm \
    --network shiraz-network \
    -v "$CRYPTO_DIR":/crypto-config \
    hyperledger/fabric-ca-tools:latest \
    /bin/bash -c '
      set -e
      export FABRIC_CA_CLIENT_HOME=/tmp/tls-enroll
      fabric-ca-client enroll \
        -u https://admin:adminpw@root-ca:7052 \
        --tls.certfiles /crypto-config/root-ca/ca-cert.pem \
        --enrollment.profile tls \
        --csr.hosts "rca-main,localhost,127.0.0.1,rca-main.example.com" \
        -M /crypto-config/intermediate-ca/tls
    ' || error "دریافت گواهی TLS ناموفق بود"

  cp "$INTERMEDIATE_DIR/tls/signcerts/cert.pem"  "$INTERMEDIATE_DIR/tls/server.crt"
  cp "$INTERMEDIATE_DIR/tls/keystore/"*_sk        "$INTERMEDIATE_DIR/tls/server.key"
  cp "$CRYPTO_DIR/root-ca/ca-cert.pem"            "$INTERMEDIATE_DIR/tls/ca.crt"
  success "فایل‌های TLS rca-main آماده شدند"

  # =====================================================
  # ساخت config برای rca-main با تمام identity‌ها
  # =====================================================
  log "ساخت config برای rca-main"
  cat > "$INTERMEDIATE_DIR/fabric-ca-server-config.yaml" << 'EOF'
port: 7054
debug: true

tls:
  enabled: true
  certfile: tls/server.crt
  keyfile: tls/server.key

signing:
  default:
    usage:
      - digital signature
    expiry: 8760h
  profiles:
    tls:
      usage:
        - signing
        - key encipherment
        - server auth
        - client auth
      expiry: 8760h
    ca:
      usage:
        - signing
        - digital signature
        - key encipherment
        - cert sign
        - crl sign
      expiry: 8760h
      caconstraint:
        isca: true

affiliations:
  "":
    - "."

registry:
  maxenrollments: -1
  identities:
    - name: admin
      pass: adminpw
      type: admin
      affiliation: ""
      attrs:
        hf.Registrar.Roles: "client,peer,orderer,admin,user"
        hf.Registrar.DelegateRoles: "client,peer,orderer,admin,user"
        hf.Revoker: true
        hf.IntermediateCA: true
        hf.GenCRL: true
        hf.Registrar.Attributes: "*"
        hf.AffiliationMgr: true

    - name: Admin@example.com
      pass: adminpw
      type: admin
      affiliation: ""
      attrs:
        hf.Registrar.Roles: "client,peer,orderer,admin,user"
        hf.Registrar.DelegateRoles: "client,peer,orderer,admin,user"
        hf.Revoker: true

    - name: orderer.example.com
      pass: ordererpw
      type: orderer
      affiliation: ""
      attrs:
        hf.Revoker: true
EOF

  # اوردررهای اضافی خوشه Raft.
  #
  # هویت‌ها در همین فایل پیکربندی CA تعریف می‌شوند و بعد از راه‌اندازی
  # قابل افزودن نیستند. تا پیش از این فقط orderer.example.com اینجا بود،
  # پس enroll نودهای ۲ و ۳ با «Failed to get user: sql: no rows in result
  # set» رد می‌شد و network.sh به گواهی خودامضا می‌افتاد — که خوشه Raft را
  # می‌خواباند بی‌آنکه خطایی در آن مرحله دیده شود.
  for _n in $(seq 2 "${ORDERER_NODES:-1}"); do
    cat >> "$INTERMEDIATE_DIR/fabric-ca-server-config.yaml" << EOF

    - name: orderer${_n}.example.com
      pass: ordererpw
      type: orderer
      affiliation: ""
      attrs:
        hf.Revoker: true
EOF
  done

  for i in {1..8}; do
    cat >> "$INTERMEDIATE_DIR/fabric-ca-server-config.yaml" << EOF

    - name: Admin@org${i}.example.com
      pass: adminpw
      type: admin
      affiliation: ""
      attrs:
        hf.Registrar.Roles: "client,peer,orderer,admin,user"
        hf.Registrar.DelegateRoles: "client,peer,orderer,admin,user"
        hf.Revoker: true

    - name: peer0.org${i}.example.com
      pass: peerpw
      type: peer
      affiliation: ""
      attrs:
        hf.Revoker: true
EOF
  done
  success "config rca-main با تمام identity‌ها ساخته شد"

  # =====================================================
  # ساخت docker-compose-rca.yml و راه‌اندازی rca-main
  # =====================================================
  log "ساخت docker-compose-rca.yml"
  cat > "$PROJECT_DIR/docker-compose-rca.yml" << 'EOF'
version: '3.8'
networks:
  shiraz-network:
    external: true
services:
  rca-main:
    image: hyperledger/fabric-ca:1.5
    container_name: rca-main
    hostname: rca-main
    ports:
      - "7054:7054"
    environment:
      - FABRIC_CA_SERVER_HOME=/etc/hyperledger/fabric-ca-server
      - FABRIC_CA_SERVER_PORT=7054
      - FABRIC_CA_SERVER_CA_NAME=rca-main
      - FABRIC_CA_SERVER_TLS_ENABLED=true
      - FABRIC_CA_SERVER_TLS_CERTFILE=/etc/hyperledger/fabric-ca-server/tls/server.crt
      - FABRIC_CA_SERVER_TLS_KEYFILE=/etc/hyperledger/fabric-ca-server/tls/server.key
      - FABRIC_CA_SERVER_CSR_CN=rca-main.example.com
      - FABRIC_CA_SERVER_CSR_HOSTS=rca-main,localhost,127.0.0.1
    volumes:
      - ./crypto-config/intermediate-ca:/etc/hyperledger/fabric-ca-server
    command: sh -c 'fabric-ca-server start -b admin:adminpw --registry.maxenrollments -1'
    networks:
      - shiraz-network
    restart: unless-stopped
EOF

  log "راه‌اندازی rca-main"
  $COMPOSE -f "$PROJECT_DIR/docker-compose-rca.yml" up -d
  sleep 60
  docker logs rca-main 2>&1 | grep -i "listening\|error" | tail -3

  # =====================================================
  # تولید هویت Orderer (MSP)
  # =====================================================
  log "تولید هویت Orderer از rca-main"
  docker run --rm \
    --network shiraz-network \
    -v "$CRYPTO_DIR":/crypto-config \
    hyperledger/fabric-ca-tools:latest \
    /bin/bash -c '
      set -e
      TLS_CERT="/crypto-config/intermediate-ca/tls/ca.crt"

      fabric-ca-client enroll \
        -u https://Admin@example.com:adminpw@rca-main:7054 \
        --tls.certfiles "$TLS_CERT" \
        --csr.cn Admin@example.com \
        --csr.names C=IR,O=6G-Project,OU=admin,ST=Tehran \
        -M /crypto-config/ordererOrganizations/example.com/users/Admin@example.com/msp

      fabric-ca-client enroll \
        -u https://orderer.example.com:ordererpw@rca-main:7054 \
        --tls.certfiles "$TLS_CERT" \
        --csr.cn orderer.example.com \
        --csr.names C=IR,O=6G-Project,OU=orderer,ST=Tehran \
        --csr.hosts "orderer.example.com,localhost,127.0.0.1" \
        -M /crypto-config/ordererOrganizations/example.com/orderers/orderer.example.com/msp

      echo "✅ هویت Orderer تولید شد"
    ' || error "تولید هویت Orderer ناموفق بود"

  # =====================================================
  # تولید هویت همه Orgها (MSP)
  # =====================================================
  log "تولید هویت تمام سازمان‌ها از rca-main"
  for i in {1..8}; do
    log "تولید هویت org${i}"
    docker run --rm \
      --network shiraz-network \
      -v "$CRYPTO_DIR":/crypto-config \
      hyperledger/fabric-ca-tools:latest \
      /bin/bash -c "
        set -e
        TLS_CERT=\"/crypto-config/intermediate-ca/tls/ca.crt\"

        fabric-ca-client enroll \
          -u https://Admin@org${i}.example.com:adminpw@rca-main:7054 \
          --tls.certfiles \"\$TLS_CERT\" \
          --csr.cn Admin@org${i}.example.com \
          --csr.names C=IR,O=6G-Project,OU=admin,ST=Tehran \
          -M /crypto-config/peerOrganizations/org${i}.example.com/users/Admin@org${i}.example.com/msp

        fabric-ca-client enroll \
          -u https://peer0.org${i}.example.com:peerpw@rca-main:7054 \
          --tls.certfiles \"\$TLS_CERT\" \
          --csr.cn peer0.org${i}.example.com \
          --csr.names C=IR,O=6G-Project,OU=peer,ST=Tehran \
          --csr.hosts \"peer0.org${i}.example.com,localhost,127.0.0.1\" \
          -M /crypto-config/peerOrganizations/org${i}.example.com/peers/peer0.org${i}.example.com/msp

        echo \"✅ org${i} تولید شد\"
      " || error "تولید هویت org${i} ناموفق بود"
  done
  success "تمام هویت‌های Orgها تولید شدند"

  # =====================================================
  # ساخت MSP کامل با cacerts، admincerts و config.yaml
  # =====================================================
  log "ساخت MSP کامل برای همه سازمان‌ها"

  CA_CERT_NAME="ca-cert.pem"
  RCA_CERT="$INTERMEDIATE_DIR/ca-cert.pem"
  ROOT_CERT="$CRYPTO_DIR/root-ca/ca-cert.pem"

  # زنجیره اعتماد TLS: گواهی CA میانی که واقعاً نودها را امضا می‌کند، به
  # همراه ریشه. ترتیب مهم است — امضاکننده مستقیم اول.
  _tls_chain() {
    local mid="$CRYPTO_DIR/intermediate-ca/ca-cert.pem"
    [ -f "$mid" ] && cat "$mid"
    [ -f "$ROOT_CERT" ] && cat "$ROOT_CERT"
  }

  if [ ! -f "$RCA_CERT" ] && [ -f "$INTERMEDIATE_DIR/ca-chain.pem" ]; then
    awk '/-----BEGIN CERTIFICATE-----/{p=1; count++} p && count==1{print} /-----END CERTIFICATE-----/ && count==1{p=0}' \
      "$INTERMEDIATE_DIR/ca-chain.pem" > "$RCA_CERT"
  fi
  [ ! -f "$RCA_CERT" ] && cp "$ROOT_CERT" "$RCA_CERT"

  # ----- تابع کمکی برای ساخت config.yaml -----
  write_nodeou_config() {
    local target="$1"
    cat > "$target" << EOF
NodeOUs:
  Enable: true
  ClientOUIdentifier:
    Certificate: cacerts/${CA_CERT_NAME}
    OrganizationalUnitIdentifier: client
  PeerOUIdentifier:
    Certificate: cacerts/${CA_CERT_NAME}
    OrganizationalUnitIdentifier: peer
  AdminOUIdentifier:
    Certificate: cacerts/${CA_CERT_NAME}
    OrganizationalUnitIdentifier: admin
  OrdererOUIdentifier:
    Certificate: cacerts/${CA_CERT_NAME}
    OrganizationalUnitIdentifier: orderer
EOF
  }

  # ===================== Orderer MSP =====================
  log "تنظیم MSP Orderer"
  local OORG="$CRYPTO_DIR/ordererOrganizations/example.com"
  mkdir -p "$OORG/orderers/orderer.example.com/msp" "$OORG/msp" "$OORG/users/Admin@example.com/msp"

  write_nodeou_config "$OORG/orderers/orderer.example.com/msp/config.yaml"
  cp "$OORG/orderers/orderer.example.com/msp/config.yaml" "$OORG/msp/config.yaml"
  cp "$OORG/orderers/orderer.example.com/msp/config.yaml" "$OORG/users/Admin@example.com/msp/config.yaml"

  mkdir -p "$OORG/msp/cacerts" "$OORG/orderers/orderer.example.com/msp/cacerts" "$OORG/users/Admin@example.com/msp/cacerts"
  cp "$RCA_CERT" "$OORG/msp/cacerts/${CA_CERT_NAME}"
  cp "$RCA_CERT" "$OORG/orderers/orderer.example.com/msp/cacerts/${CA_CERT_NAME}"
  cp "$RCA_CERT" "$OORG/users/Admin@example.com/msp/cacerts/${CA_CERT_NAME}"

  # ── tlscacerts فقط وقتی TLS روی سیم فعال است ──
  # در شبکه بدون TLS، وجود tlscacerts در MSP باعث می‌شود Fabric Gateway
  # نتیجه بگیرد که باید با TLS/mutual-TLS به peer ها و orderer وصل شود و با
  # خطای «both Key and Certificate are required when using mutual TLS» شکست بخورد.
  # برای فعال‌سازی TLS: NETWORK_TLS=true ./network.sh
  #
  # محتوای tlscacerts باید **امضاکننده واقعی گواهی TLS نودها** باشد، نه
  # گواهی ریشه. گواهی‌ها را CA میانی (rca-main) صادر می‌کند، پس اگر فقط
  # ریشه اینجا بنشیند، اوردرر هنگام ساخت کانال گواهی consenter را
  # نمی‌شناسد:
  #
  #   consenter ... has invalid certificate: x509: certificate signed by
  #   unknown authority
  #
  # زنجیره کامل (میانی + ریشه) نوشته می‌شود تا هر دو حالت پوشش داده شود.
  if [ "${NETWORK_TLS:-false}" = "true" ]; then
    mkdir -p "$OORG/msp/tlscacerts"
    _tls_chain > "$OORG/msp/tlscacerts/ca-cert.pem"
  fi

  mkdir -p "$OORG/msp/admincerts" "$OORG/orderers/orderer.example.com/msp/admincerts"
  cp "$OORG/users/Admin@example.com/msp/signcerts/cert.pem" "$OORG/msp/admincerts/"
  cp "$OORG/users/Admin@example.com/msp/signcerts/cert.pem" "$OORG/orderers/orderer.example.com/msp/admincerts/"
  success "MSP Orderer ساخته شد"

  # ===================== Peer Orgها MSP =====================
  for i in {1..8}; do
    local PORG="$CRYPTO_DIR/peerOrganizations/org${i}.example.com"
    log "تنظیم MSP برای org${i}"

    mkdir -p "$PORG/peers/peer0.org${i}.example.com/msp" "$PORG/msp" "$PORG/users/Admin@org${i}.example.com/msp"

    write_nodeou_config "$PORG/peers/peer0.org${i}.example.com/msp/config.yaml"
    cp "$PORG/peers/peer0.org${i}.example.com/msp/config.yaml" "$PORG/msp/config.yaml"
    cp "$PORG/peers/peer0.org${i}.example.com/msp/config.yaml" "$PORG/users/Admin@org${i}.example.com/msp/config.yaml"

    mkdir -p "$PORG/msp/cacerts" "$PORG/peers/peer0.org${i}.example.com/msp/cacerts" "$PORG/users/Admin@org${i}.example.com/msp/cacerts"
    cp "$RCA_CERT" "$PORG/msp/cacerts/${CA_CERT_NAME}"
    cp "$RCA_CERT" "$PORG/peers/peer0.org${i}.example.com/msp/cacerts/${CA_CERT_NAME}"
    cp "$RCA_CERT" "$PORG/users/Admin@org${i}.example.com/msp/cacerts/${CA_CERT_NAME}"

    # tlscacerts فقط وقتی TLS روی سیم فعال است (توضیح در بخش MSP اوردرر)
    if [ "${NETWORK_TLS:-false}" = "true" ]; then
      mkdir -p "$PORG/msp/tlscacerts"
      _tls_chain > "$PORG/msp/tlscacerts/ca-cert.pem"
    fi

    mkdir -p "$PORG/msp/admincerts" "$PORG/peers/peer0.org${i}.example.com/msp/admincerts"
    cp "$PORG/users/Admin@org${i}.example.com/msp/signcerts/cert.pem" "$PORG/msp/admincerts/"
    cp "$PORG/users/Admin@org${i}.example.com/msp/signcerts/cert.pem" "$PORG/peers/peer0.org${i}.example.com/msp/admincerts/"
    success "MSP org${i} ساخته شد"
  done

  # ═══════════════════════════════════════════════════════════════════
  # گواهی‌های TLS
  # ═══════════════════════════════════════════════════════════════════
  # هر نود گواهی TLS خودش را از همان CA میانی می‌گیرد که هویت MSP را
  # صادر کرده، پس زنجیره اعتماد یکی می‌ماند و نودها گواهی یکدیگر را
  # می‌پذیرند.
  #
  # این کار همیشه انجام می‌شود، حتی وقتی NETWORK_TLS=false است. وجود
  # گواهی بی‌ضرر است، ولی نبودش یعنی برای روشن کردن TLS باید کل شبکه از
  # نو ساخته شود. با این کار فعال‌سازی TLS فقط یک متغیر در .env و یک
  # restart است.
  #
  # گواهی کلاینت هم صادر می‌شود چون Raft برای ارتباط بین نودها هر دو را
  # می‌خواهد و بدون آن اصلاً بالا نمی‌آید.
  _enroll_tls() {
    local cn="$1" secret="$2" out="$3"
    mkdir -p "$out"
    docker run --rm --network shiraz-network \
      -v "$CRYPTO_DIR":/crypto-config \
      hyperledger/fabric-ca-tools:latest \
      /bin/bash -c "
        set -e
        fabric-ca-client enroll \
          -u https://${cn}:${secret}@rca-main:7054 \
          --tls.certfiles /crypto-config/intermediate-ca/tls/ca.crt \
          --enrollment.profile tls \
          --csr.cn ${cn} \
          --csr.hosts '${cn},localhost,127.0.0.1' \
          -M /crypto-config/.tlstmp/${cn} >/dev/null 2>&1
      " >/dev/null 2>&1 || return 1

    local tmp="$CRYPTO_DIR/.tlstmp/${cn}"
    [ -f "$tmp/signcerts/cert.pem" ] || return 1
    cp "$tmp/signcerts/cert.pem" "$out/server.crt"
    cp "$tmp"/keystore/*_sk       "$out/server.key"
    cp "$tmp"/tlscacerts/*.pem    "$out/ca.crt"
    cp "$out/server.crt" "$out/client.crt"
    cp "$out/server.key" "$out/client.key"
    chmod 600 "$out/server.key" "$out/client.key"
    rm -rf "$tmp"
  }

  # اگر CA در دسترس نبود، گواهی از ریشه امضا می‌شود. برای شبکه آزمایشی
  # کافی است و همان ca.crt ریشه اعتماد همه می‌ماند.
  _selfsign_tls() {
    local cn="$1" out="$2"
    local root="$CRYPTO_DIR/root-ca/ca-cert.pem"
    local rootkey
    rootkey="$(ls "$CRYPTO_DIR"/root-ca/*_sk "$CRYPTO_DIR"/root-ca/ca-key.pem 2>/dev/null | head -1)"
    mkdir -p "$out"
    openssl ecparam -name prime256v1 -genkey -noout -out "$out/server.key" 2>/dev/null
    if [ -f "$root" ] && [ -n "$rootkey" ] && [ -f "$rootkey" ]; then
      openssl req -new -key "$out/server.key" -out "$out/server.csr" \
        -subj "/CN=${cn}/O=6G-Project/C=IR" 2>/dev/null
      openssl x509 -req -in "$out/server.csr" -CA "$root" -CAkey "$rootkey" \
        -CAcreateserial -out "$out/server.crt" -days 3650 \
        -extfile <(printf "subjectAltName=DNS:%s,DNS:localhost,IP:127.0.0.1" "$cn") 2>/dev/null
      rm -f "$out/server.csr"
      cp "$root" "$out/ca.crt"
    else
      openssl req -new -x509 -key "$out/server.key" -out "$out/server.crt" \
        -days 3650 -subj "/CN=${cn}/O=6G-Project/C=IR" \
        -addext "subjectAltName=DNS:${cn},DNS:localhost,IP:127.0.0.1" 2>/dev/null
      cp "$out/server.crt" "$out/ca.crt"
    fi
    cp "$out/server.crt" "$out/client.crt"
    cp "$out/server.key" "$out/client.key"
    chmod 600 "$out/server.key" "$out/client.key"
  }

  # صدور گواهی TLS یک نود.
  #
  # اگر CA در دسترس نباشد، fallback خودامضا می‌سازد. برای یک peer تنها
  # بی‌ضرر است، ولی برای خوشه Raft کشنده: نودها با TLS pinning همدیگر را
  # می‌شناسند، و وقتی هر کدام ریشه خودش باشد هیچ‌کدام دیگری را تأیید
  # نمی‌کند. نتیجه «tls: bad certificate» و خوشه‌ای که هرگز رهبر انتخاب
  # نمی‌کند — بی‌آنکه هیچ خطایی در این مرحله دیده شود.
  #
  # پس در حالت Raft، افتادن به fallback یک شکست است نه یک جایگزین.
  _issue_tls() {
    local cn="$1" secret="$2" out="$3"
    # گواهی موجود فقط وقتی پذیرفته می‌شود که از CA آمده باشد. خودامضای
    # مانده از اجرای قبلی باید دور ریخته شود، وگرنه اجرای دوباره با CA
    # سالم هم چیزی را درست نمی‌کند.
    if [ -f "$out/server.crt" ]; then
      local iss
      iss="$(openssl x509 -in "$out/server.crt" -noout -issuer 2>/dev/null)"
      if echo "$iss" | grep -q "rca-main"; then
        return 0
      fi
      warn "  گواهی $cn خودامضا بود — دور ریخته شد"
      rm -f "$out"/server.crt "$out"/server.key "$out"/ca.crt \
            "$out"/client.crt "$out"/client.key
    fi

    if _enroll_tls "$cn" "$secret" "$out"; then
      return 0
    fi

    if [ "${ORDERER_NODES:-1}" -gt 1 ] && echo "$cn" | grep -q orderer; then
      error "صدور گواهی $cn از CA ناموفق بود.

  برای خوشه Raft گواهی خودامضا کار نمی‌کند — نودها یکدیگر را با pinning
  می‌شناسند و ریشه‌های جدا همدیگر را تأیید نمی‌کنند.

  بررسی کنید CA بالا باشد:
    docker ps --format '{{.Names}}' | grep -E 'rca-main|root-ca'
    cd $CONFIG_DIR && docker compose -f docker-compose-root-ca.yml up -d"
    fi

    _selfsign_tls "$cn" "$out"
  }

  log "تولید گواهی‌های TLS"
  OORG_TLS="$CRYPTO_DIR/ordererOrganizations/example.com"

  # اوردررها به تعداد ORDERER_NODES ساخته می‌شوند، تا خوشه Raft بدون گام
  # جداگانه آماده باشد. اگر شبکه solo بماند، نودهای اضافی فقط روی دیسک
  # هستند و کانتینری برایشان بالا نمی‌آید — profile در compose این را
  # کنترل می‌کند.
  for _n in $(seq 1 "${ORDERER_NODES:-1}"); do
    if [ "$_n" = "1" ]; then _cn="orderer.example.com"; else _cn="orderer${_n}.example.com"; fi
    _dir="$OORG_TLS/orderers/$_cn"
    if [ "$_n" != "1" ] && [ ! -d "$_dir/msp" ]; then
      mkdir -p "$_dir/msp"
      cp -r "$OORG_TLS/orderers/orderer.example.com/msp/." "$_dir/msp/"
    fi
    _issue_tls "$_cn" "ordererpw" "$_dir/tls"
  done
  success "گواهی TLS ${ORDERER_NODES:-1} اوردرر"

  for i in {1..8}; do
    _issue_tls "peer0.org${i}.example.com" "peerpw" \
      "$CRYPTO_DIR/peerOrganizations/org${i}.example.com/peers/peer0.org${i}.example.com/tls"
  done
  success "گواهی TLS ۸ peer"
  rm -rf "$CRYPTO_DIR/.tlstmp"


  # =====================================================
  # ساخت channel artifacts
  # =====================================================
  mkdir -p "$CHANNEL_ARTIFACTS"

  log "ساخت genesis.block"
  configtxgen -profile OrdererGenesis \
    -outputBlock "$CHANNEL_ARTIFACTS/genesis.block" \
    -channelID system-channel 2>&1 || error "خطا در ساخت genesis.block"
  success "genesis.block ساخته شد"

  for ch in "${CHANNELS[@]}"; do
    log "ساخت ${ch}.tx"
    configtxgen -profile ApplicationChannel \
      -outputCreateChannelTx "$CHANNEL_ARTIFACTS/${ch}.tx" \
      -channelID "$ch" 2>&1 || error "خطا در ساخت ${ch}.tx"

    for i in {1..8}; do
      configtxgen -profile ApplicationChannel \
        -outputAnchorPeersUpdate "$CHANNEL_ARTIFACTS/${ch}_org${i}MSP_anchors.tx" \
        -channelID "$ch" \
        -asOrg "org${i}MSP" 2>&1
    done
    success "${ch} artifacts ساخته شدند"
  done

  success "شبکه با موفقیت آماده شد!"
}

# ------------------- راه‌اندازی شبکه -------------------
start_network() {
  log "راه‌اندازی شبکه (orderer + peers)..."
  cd "$PROJECT_DIR"
  $COMPOSE up -d
  sleep 60
  success "شبکه بالا آمد"
  docker ps --format "table {{.Names}}\t{{.Status}}" | grep -E "orderer|peer0"
}

# ------------------- ایجاد و join کانال‌ها -------------------
create_and_join_channels() {
  log "ایجاد کانال‌ها و تنظیم Anchor Peer..."
  local CHANNEL_ARTIFACTS="$CONFIG_DIR/channel-artifacts"

  for ch in "${CHANNELS[@]}"; do
    log "ایجاد کانال $ch ..."

    docker cp "$CHANNEL_ARTIFACTS/${ch}.tx" peer0.org1.example.com:/tmp/${ch}.tx
    docker exec \
      -e CORE_PEER_LOCALMSPID=org1MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
      -e CORE_PEER_TLS_ENABLED=false \
      peer0.org1.example.com \
      peer channel create \
        -o orderer.example.com:7050 \
        -c ${ch} \
        -f /tmp/${ch}.tx \
        --outputBlock /tmp/${ch}.block \
        --timeout 30s 2>&1 || error "ایجاد کانال ${ch} ناموفق بود"

    docker cp peer0.org1.example.com:/tmp/${ch}.block "$CHANNEL_ARTIFACTS/"
    success "کانال ${ch} ساخته شد"

    # join همه peerها
    for i in {1..8}; do
      local PEER="peer0.org${i}.example.com"
      local PORT="${ORG_PORTS[$i]}"

      docker cp "$CHANNEL_ARTIFACTS/${ch}.block" $PEER:/tmp/${ch}.block
      docker exec \
        -e CORE_PEER_LOCALMSPID=org${i}MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
        -e CORE_PEER_TLS_ENABLED=false \
        $PEER \
        peer channel join -b /tmp/${ch}.block 2>&1 \
        && success "$PEER به $ch join شد" || log "هشدار: $PEER join نشد"
    done

    # تنظیم Anchor Peer
    log "تنظیم Anchor Peer برای $ch"
    for i in {1..8}; do
      local PEER="peer0.org${i}.example.com"
      local PORT="${ORG_PORTS[$i]}"
      local ANCHOR_TX="${ch}_org${i}MSP_anchors.tx"

      [ ! -f "$CHANNEL_ARTIFACTS/$ANCHOR_TX" ] && continue

      docker cp "$CHANNEL_ARTIFACTS/$ANCHOR_TX" $PEER:/tmp/$ANCHOR_TX
      docker exec \
        -e CORE_PEER_LOCALMSPID=org${i}MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
        -e CORE_PEER_TLS_ENABLED=false \
        $PEER \
        peer channel update \
          -o orderer.example.com:7050 \
          -c ${ch} \
          -f /tmp/${ANCHOR_TX} 2>&1 \
        && success "Anchor Peer $PEER در $ch تنظیم شد" \
        || log "هشدار: Anchor Peer $PEER در $ch تنظیم نشد"
    done

    success "کانال $ch کامل شد"
  done

  success "تمام کانال‌ها ساخته و join شدند"
}

# ------------------- آماده‌سازی chaincode -------------------
generate_chaincode_modules() {
  if [ ! -d "$CHAINCODE_DIR" ] || [ -z "$(ls -A "$CHAINCODE_DIR" 2>/dev/null)" ]; then
    log "پوشه chaincode خالی یا موجود نیست — رد شد"
    return 0
  fi

  log "آماده‌سازی go.mod برای chaincodeها..."
  local count=0
  while IFS= read -r d; do
    name=$(basename "$d")
    [ ! -f "$d/chaincode.go" ] && continue
    (
      cd "$d"
      if [ ! -f go.mod ] || [ ! -d vendor ]; then
        cat > go.mod << EOF
module $name
go 1.18
require github.com/hyperledger/fabric-contract-api-go v1.2.2
EOF
        go mod tidy
        go mod vendor
      fi
      success "Chaincode $name آماده شد"
    ) || log "خطا در $name"
    ((count++))
  done < <(find "$CHAINCODE_DIR" -mindepth 1 -maxdepth 1 -type d | sort)
  success "تمام $count chaincode آماده شدند!"
}

setup_external_builders() {
  log "راه‌اندازی external builder برای chaincode..."

  local builder_dir="$SCRIPTS_DIR/builders/golang/bin"
  mkdir -p "$builder_dir"

  # ---------- detect ----------
  cat > "$builder_dir/detect" << 'EOF'
#!/bin/bash
set -e
METADIR=$2
if grep -qE '"type"\s*:\s*"prebuilt"' "$METADIR/metadata.json" 2>/dev/null; then
  exit 0
fi
exit 1
EOF

  # ---------- build ----------
  cat > "$builder_dir/build" << 'EOF'
#!/bin/bash
set -e
SOURCE=$1
OUTPUT=$3
mkdir -p "$OUTPUT/bin"
cp "$SOURCE/bin/chaincode" "$OUTPUT/bin/chaincode"
chmod +x "$OUTPUT/bin/chaincode"
EOF

  # ---------- release ----------
  cat > "$builder_dir/release" << 'EOF'
#!/bin/bash
exit 0
EOF

  # ---------- run ----------
  # peer این اسکریپت را برای اجرای باینری از پیش‌ساخته صدا می‌زند.
  # chaincode.json شامل chaincode_id و peer_address است؛ بدون jq با sed پارس می‌کنیم.
  cat > "$builder_dir/run" << 'EOF'
#!/bin/bash
# run — اجرای chaincode ساخته‌شده توسط builder خارجی.
#
# دو نکته که این اسکریپت را شکل می‌دهند:
#
# ۱) اینجا داخل کانتینر peer اجرا می‌شود، نه روی هاست. تصویر fabric-peer
#    نه python3 دارد نه jq — استفاده از آنها «exit status 127» می‌دهد که
#    peer آن را «builder 'prebuilt' run failed» گزارش می‌کند. پس فقط sed.
#
# ۲) نسخه اول «export CORE_PEER_TLS_ENABLED=false» را ثابت داشت. با TLS
#    روشن، سرویس chaincode روی peer (پورت ۷۰۵۲) فقط اتصال TLS می‌پذیرد:
#      peer: tls: first record does not look like a TLS handshake
#            server=ChaincodeServer
#      سپس: chaincode registration failed: container exited with 0
#    chaincode رد می‌شود، ثبت‌نام نمی‌کند و تمیز خارج می‌شود — پس هیچ خطای
#    صریحی از خودش دیده نمی‌شود.
#
# فابریک مواد TLS را در همان chaincode.json می‌گذارد.
set -e

BUILD_DIR="$1"
METADIR="$2"
CC_JSON="$METADIR/chaincode.json"

# یک فیلد رشته‌ای از JSON. \n های داخل مقدار به خط واقعی تبدیل می‌شوند —
# گواهی‌های PEM این‌طور کدگذاری شده‌اند و بدون این تبدیل نامعتبرند.
_field() {
    sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$CC_JSON" \
        | sed 's/\\n/\n/g'
}

export CORE_CHAINCODE_ID_NAME="$(_field chaincode_id)"
PEER_ADDRESS="$(_field peer_address)"

CLIENT_CERT="$(_field client_cert)"
CLIENT_KEY="$(_field client_key)"
ROOT_CERT="$(_field root_cert)"

if [ -n "$CLIENT_CERT" ] && [ -n "$CLIENT_KEY" ]; then
    printf '%s\n' "$CLIENT_CERT" > "$METADIR/client.crt"
    printf '%s\n' "$CLIENT_KEY"  > "$METADIR/client.key"
    chmod 600 "$METADIR/client.key"

    export CORE_PEER_TLS_ENABLED=true
    # shim دو صورت را می‌شناسد و تفاوتشان مهم است:
    #   *_PATH → فایل حاوی PEM کدشده با base64
    #   *_FILE → فایل حاوی PEM خام
    export CORE_TLS_CLIENT_CERT_FILE="$METADIR/client.crt"
    export CORE_TLS_CLIENT_KEY_FILE="$METADIR/client.key"

    if [ -n "$ROOT_CERT" ]; then
        printf '%s\n' "$ROOT_CERT" > "$METADIR/root.crt"
        export CORE_PEER_TLS_ROOTCERT_FILE="$METADIR/root.crt"
    fi
else
    export CORE_PEER_TLS_ENABLED=false
fi

# نکته: shim آدرس peer را از فلگ -peer.address می‌خواند، نه از env
exec "$BUILD_DIR/bin/chaincode" -peer.address="$PEER_ADDRESS"
EOF

  chmod +x "$builder_dir"/{detect,build,release,run}
  success "External builder scripts ساخته شدند در $builder_dir"

  # ---------- آماده‌سازی core.yaml ----------
  local core_yaml="$CONFIG_DIR/core.yaml"

  # اگر Docker قبلاً به‌جای فایل، دایرکتوری ساخته باشد → حذفش کن
  if [ -d "$core_yaml" ]; then
    log "core.yaml به‌صورت دایرکتوری ساخته شده بود؛ حذف می‌شود..."
    rm -rf "$core_yaml"
  fi

  # اگر فایل موجود نیست، از image پیر استخراج کن
  if [ ! -f "$core_yaml" ]; then
    log "استخراج core.yaml از image پیر..."
    mkdir -p "$CONFIG_DIR"
    docker run --rm hyperledger/fabric-peer:2.5 \
      cat /etc/hyperledger/fabric/core.yaml > "$core_yaml"
  fi

  # ---------- افزودن builder «prebuilt» ----------
  log "اضافه کردن external builder «prebuilt» به core.yaml..."
  python3 - "$core_yaml" << 'PYEOF'
import sys, yaml

path = sys.argv[1]
with open(path) as f:
    cfg = yaml.safe_load(f) or {}

chaincode = cfg.setdefault("chaincode", {})
builders = chaincode.get("externalBuilders")
if not isinstance(builders, list):
    builders = []
    chaincode["externalBuilders"] = builders

names = {b.get("name") for b in builders if isinstance(b, dict)}
if "prebuilt" not in names:
    builders.append({"name": "prebuilt", "path": "/builders/golang"})

with open(path, "w") as f:
    yaml.safe_dump(cfg, f, default_flow_style=False, sort_keys=False)

print("prebuilt added" if "prebuilt" not in names else "prebuilt already present")
PYEOF

  success "core.yaml آماده شد ($core_yaml)"
}

# ------------------- نصب و deploy chaincode -------------------
package_and_install_chaincode() {
  if [ ! -d "$CHAINCODE_DIR" ] || [ -z "$(ls -A "$CHAINCODE_DIR" 2>/dev/null)" ]; then
    log "هیچ chaincode پیدا نشد"
    return 0
  fi

  log "دانلود تصاویر مورد نیاز chaincode..."
  docker pull hyperledger/fabric-tools:2.5 >/dev/null 2>&1 || true
  docker pull hyperledger/fabric-ccenv:2.5 >/dev/null 2>&1 || true
  docker pull hyperledger/fabric-baseos:2.5 >/dev/null 2>&1 || true

  for dir in "$CHAINCODE_DIR"/*/; do
    [ ! -d "$dir" ] && continue
    name=$(basename "$dir")
    log "=== پردازش Chaincode: $name ==="

    local tar="/tmp/${name}.tar.gz"
    rm -f "$tar"

    log "بسته‌بندی $name ..."
    docker run --rm \
      -v "$dir":/chaincode/input:ro \
      -v /tmp:/hosttmp \
      hyperledger/fabric-tools:2.5 \
      peer lifecycle chaincode package /hosttmp/${name}.tar.gz \
        --path /chaincode/input \
        --lang golang \
        --label ${name}_1.0 2>&1

    [ ! -f "$tar" ] && { log "خطا: tar ساخته نشد"; continue; }
    success "بسته‌بندی $name موفق بود"

    # نصب روی همه org‌ها
    local PACKAGE_ID=""
    for i in {1..8}; do
      local PEER="peer0.org${i}.example.com"
      local PORT="${ORG_PORTS[$i]}"

      log "نصب $name روی $PEER ..."
      docker cp "$tar" $PEER:/tmp/${name}.tar.gz

      local OUT
      OUT=$(docker exec \
        -e CORE_PEER_LOCALMSPID=org${i}MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
        -e CORE_PEER_TLS_ENABLED=false \
        $PEER \
        peer lifecycle chaincode install /tmp/${name}.tar.gz 2>&1)
      echo "$OUT"

      if echo "$OUT" | grep -qE "Installed remotely|already successfully installed"; then
        success "نصب روی $PEER موفق بود"
        if [ $i -eq 1 ]; then
          PACKAGE_ID=$(echo "$OUT" | grep -o "${name}_1.0:[0-9a-f]*" | head -n1)
          [ -z "$PACKAGE_ID" ] && PACKAGE_ID=$(docker exec \
            -e CORE_PEER_LOCALMSPID=org1MSP \
            -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
            -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
            -e CORE_PEER_TLS_ENABLED=false \
            $PEER peer lifecycle chaincode queryinstalled 2>&1 | grep -o "${name}_1.0:[0-9a-f]*" | head -n1)
          echo "$PACKAGE_ID" > "/tmp/${name}_package_id.txt"
          log "Package ID: $PACKAGE_ID"
        fi
      else
        log "هشدار: نصب روی $PEER ناموفق بود"
      fi
    done

    [ -z "$PACKAGE_ID" ] && { log "خطا: Package ID پیدا نشد"; continue; }
    success "نصب $name روی همه org‌ها تمام شد — $PACKAGE_ID"

    # approve + commit برای هر کانال
    for ch in "${CHANNELS[@]}"; do
      log "approve $name برای $ch ..."
      for i in {1..8}; do
        local PEER="peer0.org${i}.example.com"
        local PORT="${ORG_PORTS[$i]}"
        docker exec \
          -e CORE_PEER_LOCALMSPID=org${i}MSP \
          -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
          -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
          -e CORE_PEER_TLS_ENABLED=false \
          $PEER \
          peer lifecycle chaincode approveformyorg \
            -o orderer.example.com:7050 \
            --channelID $ch \
            --name $name \
            --version 1.0 \
            --package-id "$PACKAGE_ID" \
            --sequence 1 2>&1 \
          && success "approve org${i} برای $ch موفق بود" \
          || log "هشدار: approve org${i} برای $ch ناموفق بود"
      done

      log "بررسی readiness برای $ch ..."
      docker exec \
        -e CORE_PEER_LOCALMSPID=org1MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
        -e CORE_PEER_TLS_ENABLED=false \
        peer0.org1.example.com \
        peer lifecycle chaincode checkcommitreadiness \
          --channelID $ch --name $name --version 1.0 --sequence 1 \
          --output json 2>&1

      log "commit $name در $ch ..."
      local PEER_ARGS=""
      for i in {1..8}; do
        PEER_ARGS="$PEER_ARGS --peerAddresses peer0.org${i}.example.com:${ORG_PORTS[$i]}"
      done

      docker exec \
        -e CORE_PEER_LOCALMSPID=org1MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
        -e CORE_PEER_TLS_ENABLED=false \
        peer0.org1.example.com \
        peer lifecycle chaincode commit \
          -o orderer.example.com:7050 \
          --channelID $ch --name $name --version 1.0 --sequence 1 \
          $PEER_ARGS 2>&1 \
        && success "commit $name در $ch موفق بود" \
        || log "هشدار: commit $name در $ch ناموفق بود"
    done

    rm -f "$tar"
    success "=== Chaincode $name کاملاً deploy شد ==="
  done

  success "تمام chaincode‌ها با موفقیت deploy شدند!"
}

# ------------------- اجرا -------------------
main() {
  cleanup
  setup_network_with_fabric_ca_tls_nodeous_active
  generate_chaincode_modules
  setup_external_builders
  start_network
  success "شبکه آماده است."
  log "حالا برای deploy کانال‌ها از deploy-staged.sh استفاده کن:"
  log "  ./deploy-staged.sh artifacts             # ساخت artifact همه ۲۰ کانال"
  log "  ./deploy-staged.sh channel datachannel   # deploy اولین کانال"
  log "  ./deploy-staged.sh list                  # وضعیت کانال‌ها"
}

main
