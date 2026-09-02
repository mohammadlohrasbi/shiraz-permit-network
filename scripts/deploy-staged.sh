#!/bin/bash
# deploy-staged.sh — deploy پله‌پله ۸۶ قرارداد روی ۲۰ کانال با ۸ سازمان
# طراحی‌شده برای سرور کم‌حافظه (۳.۷GB) با پاک‌سازی خودکار dev-container
#
# استفاده:
#   ./deploy-staged.sh artifacts        → ساخت artifact همه ۲۰ کانال (یک‌بار)
#   ./deploy-staged.sh channel <name>   → deploy کامل یک کانال خاص
#   ./deploy-staged.sh test <name>      → تست یک کانال + پاک‌سازی dev بعدش
#   ./deploy-staged.sh list             → نمایش وضعیت همه کانال‌ها
#   ./deploy-staged.sh all              → deploy همه (با احتیاط! پاک‌سازی بین هر کانال)

ROOT_DIR="/root/shiraz-permit-network"
CONFIG_DIR="$ROOT_DIR/config"
SCRIPTS_DIR="$ROOT_DIR/scripts"
CHAINCODE_DIR="$SCRIPTS_DIR/chaincode"
export FABRIC_CFG_PATH="$CONFIG_DIR"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
success() { log "موفق: $*"; }
error() { log "خطا: $*"; exit 1; }

declare -A ORG_PORTS=(
  [1]=7051 [2]=8051 [3]=9051 [4]=10051
  [5]=11051 [6]=12051 [7]=13051 [8]=14051
)

# بارگذاری نگاشت کانال↔قرارداد
source "$SCRIPTS_DIR/channel_contract_map.sh" || error "فایل channel_contract_map.sh یافت نشد"

# بارگذاری توابع deploy
source "$SCRIPTS_DIR/deploy_functions.sh" || error "فایل deploy_functions.sh یافت نشد"

# ---------- دستورات ----------
case "$1" in
  artifacts)
    generate_all_channel_artifacts
    ;;

  channel)
    [ -z "$2" ] && error "نام کانال را بده: ./deploy-staged.sh channel datachannel"
    deploy_one_channel "$2"
    ;;

  test)
    [ -z "$2" ] && error "نام کانال را بده"
    ch="$2"
    log "تست کانال $ch — query یک قرارداد..."
    first_cc=$(echo ${CHANNEL_CONTRACTS[$ch]} | awk '{print $1}')
    docker exec \
      -e CORE_PEER_LOCALMSPID=org1MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
      -e CORE_PEER_TLS_ENABLED=false \
      peer0.org1.example.com \
      peer lifecycle chaincode querycommitted -C $ch 2>&1
    ;;

  cleanup-dev)
    # پاک‌سازی همه dev-container‌های یک کانال برای آزادسازی حافظه
    [ -z "$2" ] && error "نام کانال را بده"
    for cc in ${CHANNEL_CONTRACTS[$2]}; do
      cleanup_dev_containers "$cc"
    done
    success "dev-container‌های کانال $2 پاک شدند"
    free -h
    ;;

  list)
    log "وضعیت کانال‌ها:"
    for ch in "${CHANNELS[@]}"; do
      n=$(echo ${CHANNEL_CONTRACTS[$ch]} | wc -w)
      committed=$(docker exec \
        -e CORE_PEER_LOCALMSPID=org1MSP \
        -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
        -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
        -e CORE_PEER_TLS_ENABLED=false \
        peer0.org1.example.com \
        peer lifecycle chaincode querycommitted -C $ch 2>/dev/null | grep -c "Name:")
      printf "  %-22s %d/%d قرارداد commit شده\n" "$ch" "${committed:-0}" "$n"
    done
    ;;

  all)
    log "⚠️  deploy همه ۲۰ کانال با پاک‌سازی بین هر کانال"
    log "این ممکن است طولانی باشد. شروع در ۵ ثانیه..."
    sleep 5
    generate_all_channel_artifacts
    for ch in "${CHANNELS[@]}"; do
      deploy_one_channel "$ch"
      # پاک‌سازی dev-container‌های این کانال قبل از کانال بعد
      for cc in ${CHANNEL_CONTRACTS[$ch]}; do
        cleanup_dev_containers "$cc"
      done
      log "→ آماده کانال بعدی"
    done
    success "🎉 همه کانال‌ها deploy شدند!"
    ;;

  *)
    echo "استفاده:"
    echo "  ./deploy-staged.sh artifacts          # ساخت artifact همه کانال‌ها (اول این)"
    echo "  ./deploy-staged.sh channel <name>     # deploy یک کانال"
    echo "  ./deploy-staged.sh cleanup-dev <name> # آزادسازی حافظه یک کانال"
    echo "  ./deploy-staged.sh list               # وضعیت همه کانال‌ها"
    echo "  ./deploy-staged.sh all                # deploy همه (با پاک‌سازی خودکار)"
    echo ""
    echo "کانال‌های موجود:"
    for ch in "${CHANNELS[@]}"; do echo "  - $ch"; done
    ;;
esac
