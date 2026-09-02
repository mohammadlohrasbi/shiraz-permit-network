# =====================================================================
# توابع deploy کانال‌به‌کانال — شبکه پروانه‌های ساختمانی شهرداری شیراز
# ۱۰ قرارداد روی ۲ کانال، ۸ سازمان، با پاک‌سازی خودکار dev-container
# برای صرفه‌جویی حافظه روی ماشین ۳٫۷ گیگابایتی.
# =====================================================================

# ── سیاست endorsement قراردادها (صریح و قابل resolve برای service discovery) ──
# امضای هر یک از ۸ سازمان کافی است — مطابق تصمیم «سیاست ساده و سریع».
# برای اجماع اکثریت، این را به OutOf(5,'org1MSP.member',...,'org8MSP.member') تغییر دهید.
CC_POLICY="${CC_POLICY:-OR('org1MSP.member','org2MSP.member','org3MSP.member','org4MSP.member','org5MSP.member','org6MSP.member','org7MSP.member','org8MSP.member')}"

# نگاشت کانال↔قرارداد را از فایل جداگانه source می‌کنیم
# source "$SCRIPTS_DIR/channel_contract_map.sh"

# --------- ساخت artifact همه کانال‌ها ---------
generate_all_channel_artifacts() {
  log "ساخت artifact برای ${#CHANNELS[@]} کانال..."
  local CHANNEL_ARTIFACTS="$CONFIG_DIR/channel-artifacts"
  mkdir -p "$CHANNEL_ARTIFACTS"

  for ch in "${CHANNELS[@]}"; do
    # پروفایل هر کانال از channel_contract_map.sh می‌آید تا اگر بعداً عضویت
    # auditchannel به شورای شهر و رسانه گسترش یافت، فقط یک سطر آنجا عوض شود.
    local profile="${CHANNEL_PROFILE[$ch]:-ApplicationChannel}"
    log "ساخت ${ch}.tx (پروفایل $profile) ..."
    configtxgen -profile "$profile" \
      -outputCreateChannelTx "$CHANNEL_ARTIFACTS/${ch}.tx" \
      -channelID "$ch" 2>&1 | grep -iE "error|panic" && error "خطا در ${ch}.tx" || true

    # ⚠️ گرفتن «error» از خروجی کافی نیست: configtxgen می‌تواند با پروفایل
    # اشتباه بی‌صدا خارج شود و فایلی نسازد. آن‌وقت شکست واقعی چند مرحله
    # بعد، سر join کانال، با پیامی بی‌ربط ظاهر می‌شود. پس وجود فایل
    # مستقیم بررسی می‌شود.
    [ -s "$CHANNEL_ARTIFACTS/${ch}.tx" ] || error "فایل ${ch}.tx ساخته نشد — پروفایل «$profile» را در configtx.yaml بررسی کنید"

    for i in {1..8}; do
      configtxgen -profile "$profile" \
        -outputAnchorPeersUpdate "$CHANNEL_ARTIFACTS/${ch}_org${i}MSP_anchors.tx" \
        -channelID "$ch" \
        -asOrg "org${i}MSP" 2>&1 | grep -iE "error|panic" && true || true
    done
  done
  success "همه artifact‌های کانال ساخته شدند"
}

# --------- ساخت و join یک کانال خاص ---------
create_and_join_one_channel() {
  local ch="$1"
  local CHANNEL_ARTIFACTS="$CONFIG_DIR/channel-artifacts"

  log "=== ساخت کانال $ch ==="
  if [ -f "$CHANNEL_ARTIFACTS/${ch}.block" ]; then
    log "  کانال $ch از قبل ساخته شده — فقط join انجام می‌شود"
  else
    docker cp "$CHANNEL_ARTIFACTS/${ch}.tx" peer0.org1.example.com:/tmp/${ch}.tx
    docker exec \
      -e CORE_PEER_LOCALMSPID=org1MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
      -e CORE_PEER_TLS_ENABLED=false \
      peer0.org1.example.com \
      peer channel create \
        -o orderer.example.com:7050 \
        -c ${ch} -f /tmp/${ch}.tx \
        --outputBlock /tmp/${ch}.block --timeout 30s 2>&1 \
      || { log "هشدار: ساخت $ch ناموفق (شاید از قبل هست)"; }
    docker cp peer0.org1.example.com:/tmp/${ch}.block "$CHANNEL_ARTIFACTS/" 2>/dev/null || true
  fi

  # کپی موازی block به همه peerها
  for i in {1..8}; do
    docker cp "$CHANNEL_ARTIFACTS/${ch}.block" "peer0.org${i}.example.com:/tmp/${ch}.block" 
  done
  wait

  # join موازی
  for i in {1..8}; do
    local PEER="peer0.org${i}.example.com"
    local PORT="${ORG_PORTS[$i]}"
    docker exec \
      -e CORE_PEER_LOCALMSPID=org${i}MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
      -e CORE_PEER_TLS_ENABLED=false \
      $PEER peer channel join -b /tmp/${ch}.block 
  done
  wait
  success "کانال $ch ساخته و همه peer‌ها join شدند"

  # ── اعمال anchor peer هر سازمان ──
  # بدون این گام، gossip بین‌سازمانی برقرار نمی‌شود و service discovery
  # (که Fabric Gateway به آن متکی است) فقط peer محلی را می‌بیند.
  log "  اعمال anchor peer ها..."
  for i in {1..8}; do
    local PEER="peer0.org${i}.example.com"
    local PORT="${ORG_PORTS[$i]}"
    local ATX="$CHANNEL_ARTIFACTS/${ch}_org${i}MSP_anchors.tx"
    if [ ! -f "$ATX" ]; then
      log "    org${i}: فایل anchor یافت نشد — رد شد"
      continue
    fi
    docker cp "$ATX" "${PEER}:/tmp/${ch}_anchors_${i}.tx" >/dev/null 2>&1
    if docker exec \
        -e CORE_PEER_LOCALMSPID=org${i}MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
        -e CORE_PEER_TLS_ENABLED=false \
        $PEER peer channel update -f /tmp/${ch}_anchors_${i}.tx \
        -c ${ch} -o orderer.example.com:7050 >/dev/null 2>&1; then
      log "    org${i}: ✓ anchor peer ست شد"
    else
      log "    org${i}: هشدار — anchor peer اعمال نشد (شاید از قبل ست است)"
    fi
    sleep 1
  done
  success "anchor peer های کانال $ch اعمال شدند"
}


# --------- نصب یک قرارداد روی همه ۸ peer ---------
install_one_chaincode() {
  local name="$1"
  local dir="$CHAINCODE_DIR/$name"
  local tar="/tmp/${name}.tar.gz"

  [ ! -d "$dir" ] && { log "قرارداد $name یافت نشد" >&2; return 1; }

  log "📦 vendor کردن dependencies برای $name ..."
  (cd "$dir" && go mod tidy >/dev/null 2>&1 && go mod vendor >/dev/null 2>&1)

  rm -f "$tar"
  docker run --rm \
    -v "$dir":/chaincode/input:ro -v /tmp:/hosttmp \
    hyperledger/fabric-tools:2.5 \
    peer lifecycle chaincode package /hosttmp/${name}.tar.gz \
      --path /chaincode/input --lang golang --label ${name}_1.0 

  [ ! -f "$tar" ] && { log "بسته‌بندی $name ناموفق" >&2; return 1; }

  log "  بسته‌بندی $name انجام شد، نصب موازی روی ۸ peer..." >&2

  # کپی tar به همه peerها (موازی)
  for i in {1..8}; do
    docker cp "$tar" "peer0.org${i}.example.com:/tmp/${name}.tar.gz" 
  done
  wait

  # نصب موازی روی همه ۸ peer
  for i in {1..8}; do
    local PEER="peer0.org${i}.example.com"
    local PORT="${ORG_PORTS[$i]}"
    docker exec \
      -e CORE_PEER_LOCALMSPID=org${i}MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
      -e CORE_PEER_TLS_ENABLED=false \
      $PEER peer lifecycle chaincode install /tmp/${name}.tar.gz 
  done
  wait
  sleep 3
  echo "    نصب روی ۸ peer تمام شد ✅" >&2

  # استخراج Package ID از org1
  local PACKAGE_ID
  PACKAGE_ID=$(docker exec \
    -e CORE_PEER_LOCALMSPID=org1MSP \
    -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
    -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
    -e CORE_PEER_TLS_ENABLED=false \
    peer0.org1.example.com peer lifecycle chaincode queryinstalled 2>&1 \
    | grep -o "${name}_1.0:[0-9a-f]*" | head -n1)

  rm -f "$tar"
  echo "$PACKAGE_ID"
}


# --------- approve + commit یک قرارداد روی یک کانال ---------
approve_commit_one() {
  local name="$1" ch="$2" pkgid="$3"

  # --------- مجموعه داده خصوصی ---------
  # اگر قرارداد فایل collections.json دارد، باید هم در approve و هم در commit
  # اعلام شود و هر دو باید دقیقاً یکی باشند؛ اختلاف بینشان یعنی commit با خطای
  # مبهم «approvals mismatch» رد می‌شود.
  #
  # ⚠️ نکته‌ای که ساده به نظر می‌رسد و نیست: peer فایل را از *داخل کانتینر*
  # می‌خواند، نه از هاست. پس باید اول کپی شود داخل هر کانتینری که قرار است
  # این دستور را اجرا کند.
  local COLL_SRC="$CHAINCODE_DIR/$name/collections.json"
  local COLL_ARG=""
  if [ -f "$COLL_SRC" ]; then
    local COLL_IN="/tmp/${name}_collections.json"
    for i in {1..8}; do
      docker cp "$COLL_SRC" "peer0.org${i}.example.com:$COLL_IN" >/dev/null 2>&1 \
        || { log "هشدار: کپی collections.json به peer0.org${i} ناموفق"; }
    done
    COLL_ARG="--collections-config $COLL_IN"
    log "  مجموعه داده خصوصی برای $name اعلام شد"
  fi

  # این حلقه عمداً ترتیبی است. با ۳٫۷ گیگابایت RAM، هشت approve همزمان
  # پیک حافظه می‌سازد و OOM killer معمولاً peer را می‌برد نه دستور را —
  # یعنی خطایی می‌بینید که به approve ربطی ندارد.
  log "  approve ترتیبی $name روی ۸ سازمان..."
  local APPROVE_FAILED=0
  for i in {1..8}; do
    local PEER="peer0.org${i}.example.com"
    local PORT="${ORG_PORTS[$i]}"
    if ! docker exec \
      -e CORE_PEER_LOCALMSPID=org${i}MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
      -e CORE_PEER_TLS_ENABLED=false \
      $PEER peer lifecycle chaincode approveformyorg \
        -o orderer.example.com:7050 \
        --channelID $ch --name $name --version 1.0 \
        --package-id "$pkgid" --sequence 1 \
        --signature-policy "$CC_POLICY" $COLL_ARG; then
      log "  ✗ approve سازمان org${i} برای $name ناموفق"
      APPROVE_FAILED=1
    fi
  done

  # اگر approve ناقص باشد، commit یا رد می‌شود یا بدتر: با تعداد امضای کافی
  # قبول می‌شود و یک سازمان بی‌خبر می‌ماند. جلوی هر دو گرفته می‌شود.
  if [ "$APPROVE_FAILED" = "1" ]; then
    log "✗ $name روی $ch: approve همه سازمان‌ها کامل نشد، commit انجام نمی‌شود"
    return 1
  fi
  log "  approve ۸ سازمان تمام شد"

  local PEER_ARGS=""
  for i in {1..8}; do
    PEER_ARGS="$PEER_ARGS --peerAddresses peer0.org${i}.example.com:${ORG_PORTS[$i]}"
  done
  if docker exec \
    -e CORE_PEER_LOCALMSPID=org1MSP \
    -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
    -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
    -e CORE_PEER_TLS_ENABLED=false \
    peer0.org1.example.com peer lifecycle chaincode commit \
      -o orderer.example.com:7050 \
      --channelID $ch --name $name --version 1.0 --sequence 1 \
      --signature-policy "$CC_POLICY" $COLL_ARG \
      $PEER_ARGS; then
    success "✅ $name روی $ch commit شد"
    return 0
  fi
  log "✗ commit $name/$ch ناموفق"
  return 1
}


# --------- پاک‌سازی dev-container‌های یک قرارداد (آزادسازی حافظه) ---------
cleanup_dev_containers() {
  local name="$1"
  local ids
  ids=$(docker ps -aq --filter "name=dev-peer.*-${name}_1.0" 2>/dev/null)
  if [ -n "$ids" ]; then
    docker rm -f $ids >/dev/null 2>&1 || true
    log "🧹 dev-container‌های $name پاک شدند"
  fi
}

# --------- نصب یک قرارداد که tar آن از قبل ساخته شده ---------
manual_package_one() {
  local name="$1"
  local src_dir="$CHAINCODE_DIR/$name"
  local out_tar="/tmp/${name}.tar.gz"
  local bin_out="/tmp/${name}_chaincode_bin"

  [ ! -d "$src_dir" ] && { echo "ERROR: directory not found: $src_dir"; return 1; }
  [ ! -f "$src_dir/go.mod" ] && { echo "ERROR: go.mod not found in $src_dir"; return 1; }

  echo "  [build] Compiling $name for linux/amd64..."
  local build_args=()
  [ -d "$src_dir/vendor" ] && build_args+=("-mod=vendor")

  if ! (cd "$src_dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build "${build_args[@]}" -ldflags="-s -w" -o "$bin_out" . 2>&1); then
    echo "ERROR: go build failed for $name"
    return 1
  fi

  local work
  work=$(mktemp -d)

  #ساختار داخلی code.tar.gz
  mkdir -p "$work/code/bin"
  cp "$bin_out" "$work/code/bin/chaincode"
  chmod +x "$work/code/bin/chaincode"

  # metadata با type=prebuilt برای external builder
  printf '{"path":"","type":"prebuilt","label":"%s_1.0"}' "$name" \
    > "$work/metadata.json"

  tar -czf "$work/code.tar.gz" -C "$work/code" bin
  rm -f "$out_tar"
  tar -czf "$out_tar" -C "$work" metadata.json code.tar.gz

  rm -rf "$work" "$bin_out"
  echo "  [build] Package ready: $out_tar"
  [ -f "$out_tar" ]
}


install_prepackaged() {
  local name="$1"
  local tar="/tmp/${name}.tar.gz"
  [ ! -f "$tar" ] && { echo ""; return 1; }

  # کپی موازی ✅
  for i in {1..8}; do
    docker cp "$tar" "peer0.org${i}.example.com:/tmp/${name}.tar.gz"
  done
  wait

  # نصب موازی ✅
  for i in {1..8}; do
    local PEER="peer0.org${i}.example.com"
    local PORT="${ORG_PORTS[$i]}"
    docker exec \
      -e CORE_PEER_LOCALMSPID=org${i}MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=${PEER}:${PORT} \
      -e CORE_PEER_TLS_ENABLED=false \
      $PEER peer lifecycle chaincode install /tmp/${name}.tar.gz 
  done
  wait
  sleep 3

  docker exec \
    -e CORE_PEER_LOCALMSPID=org1MSP \
    -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
    -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
    -e CORE_PEER_TLS_ENABLED=false \
    peer0.org1.example.com peer lifecycle chaincode queryinstalled 2>&1 \
    | grep -o "${name}_1.0:[0-9a-f]*" | head -n1
}


batch_package_channel() {
  local ch="$1"
  local contracts="${CHANNEL_CONTRACTS[$ch]}"
  [ -z "$contracts" ] && return 0

  # ⚠️ همزمانی محدود است و این عدد دلبخواه نیست. هر `go build` روی این کد
  # حدود ۴۰۰ تا ۶۰۰ مگابایت می‌گیرد. permitchannel نه قرارداد دارد؛ نه
  # کامپایل همزمان روی ماشین ۳٫۷ گیگابایتی یعنی OOM. و بدترین بخش ماجرا این
  # است که OOM killer معمولاً سراغ فرایند کامپایل نمی‌رود، سراغ یک peer
  # می‌رود — پس خطایی که می‌بینید «کامپایل شکست خورد» نیست، «peer پاسخ
  # نمی‌دهد» است و ساعت‌ها دنبال دلیل اشتباه می‌گردید.
  local JOBS="${PACKAGE_JOBS:-2}"
  local running=0
  for cc in $contracts; do
    manual_package_one "$cc" &
    running=$((running + 1))
    if [ "$running" -ge "$JOBS" ]; then
      wait -n 2>/dev/null || wait
      running=$((running - 1))
    fi
  done
  wait
}

# --------- deploy کامل یک کانال (همه قراردادهایش) ---------
deploy_one_channel() {
  local ch="$1"
  local contracts="${CHANNEL_CONTRACTS[$ch]}"

  [ -z "$contracts" ] && { log "کانال $ch قراردادی ندارد"; return 0; }

  log "════════════════════════════════════════"
  log "شروع deploy کانال: $ch"
  log "قراردادها: $contracts"
  log "════════════════════════════════════════"

  create_and_join_one_channel "$ch"

  # مرحله ۱: package دسته‌ای همه قراردادها در یک container (سریع!)
  log "  package دسته‌ای ${ch} (یک container برای همه)..."
  batch_package_channel "$ch"

  # مرحله ۲: نصب + approve + commit هر قرارداد (با tar آماده)
  local total=0 ok=0
  local failed_list=""
  for cc in $contracts; do
    total=$((total + 1))
    log "── قرارداد $cc روی $ch ──"
    local pkgid
    pkgid=$(install_prepackaged "$cc")
    if [ -z "$pkgid" ]; then
      log "  ✗ نصب $cc ناموفق"
      failed_list="$failed_list $cc(نصب)"
      continue
    fi
    log "  نصب شد، Package ID: ${pkgid:0:40}..."
    if approve_commit_one "$cc" "$ch" "$pkgid"; then
      ok=$((ok + 1))
    else
      failed_list="$failed_list $cc(commit)"
    fi
    rm -f "/tmp/${cc}.tar.gz"
  done

  # نمایش مصرف حافظه فعلی
  local mem_free
  mem_free=$(free -m | awk '/^Mem:/{print $7}')
  log "💾 حافظه در دسترس بعد از کانال $ch: ${mem_free}MB"

  # ⚠️ این بخش عمداً با نسخه 6G فرق دارد. آنجا این تابع در هر حالتی
  # «کانال کامل deploy شد» می‌گفت — حتی وقتی صفر قرارداد commit شده بود.
  # علت واقعی زیر چند دقیقه لاگ دفن می‌شد و بارها همان اشتباه تکرار شد.
  # اینجا شمارش واقعی گزارش می‌شود و شکست، شکست است.
  if [ "$ok" -eq "$total" ]; then
    success "کانال $ch: هر $total قرارداد commit شد"
    return 0
  fi
  log "✗ کانال $ch: فقط $ok از $total قرارداد commit شد"
  log "✗ ناموفق:$failed_list"
  return 1
}
