#!/bin/bash
# ---------------------------------------------------------------------------
# build-chaincode.sh — آماده‌سازی و کامپایل همه قراردادها
#
# هر قرارداد یک ماژول Go مستقل است، چون external builder از نوع prebuilt
# هر دایرکتوری را جدا می‌سازد. پس shared.go باید در همه کپی شود.
#
# ⚠️ درسی که از پروژه 6G آمده و اینجا از روز اول اعمال شده:
# این اسکریپت اگر کامپایل شکست بخورد exit 1 می‌دهد. در پروژه قبلی پنج بار
# پیاپی خطای کامپایل تا خود شبکه رفت و deploy هر بار «موفق» اعلام کرد در
# حالی که ۰ قرارداد commit شده بود، و علت واقعی زیر چند دقیقه لاگ دفن بود.
#
# استفاده:
#   ./build-chaincode.sh            # همه
#   ./build-chaincode.sh Permit Fee # فقط چند تا
#   SKIP_COMPILE=1 ./build-chaincode.sh   # فقط sync و بررسی ساختاری
# ---------------------------------------------------------------------------
set -e

ROOT_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
SRC_DIR="$ROOT_DIR/chaincode"
DEST_DIR="$ROOT_DIR/scripts/chaincode"
SHARED="$SRC_DIR/_shared/shared.go"

log()     { echo "[$(date +'%H:%M:%S')] $*"; }
success() { log "موفق: $*"; }
error()   { log "خطا: $*"; exit 1; }

[ -f "$SHARED" ] || error "فایل مشترک یافت نشد: $SHARED"

if [ $# -gt 0 ]; then
  CONTRACTS=("$@")
else
  CONTRACTS=()
  for d in "$SRC_DIR"/*/; do
    name=$(basename "$d")
    [ "$name" = "_shared" ] && continue
    CONTRACTS+=("$name")
  done
fi

log "آماده‌سازی ${#CONTRACTS[@]} قرارداد..."
mkdir -p "$DEST_DIR"

for cc in "${CONTRACTS[@]}"; do
  src="$SRC_DIR/$cc"
  dst="$DEST_DIR/$cc"
  [ -f "$src/chaincode.go" ] || error "فایل $src/chaincode.go وجود ندارد"

  mkdir -p "$dst"
  cp "$src/chaincode.go" "$dst/chaincode.go"
  cp "$SHARED" "$dst/shared.go"
  # فایل مجموعه داده خصوصی اگر بود
  [ -f "$src/collections.json" ] && cp "$src/collections.json" "$dst/collections.json"

  if [ ! -f "$dst/go.mod" ]; then
    (cd "$dst" && go mod init "$cc" >/dev/null 2>&1) || true
  fi
  (
    cd "$dst"
    go mod edit -require=github.com/hyperledger/fabric-contract-api-go@v1.2.1
    go mod tidy   >/dev/null 2>&1 || true
    go mod vendor >/dev/null 2>&1 || true
  )
  echo "  ✓ $cc"
done

# --------- بررسی ساختاری بدون کامپایلر ---------
# check-go.js ده کلاس خطا را می‌گیرد (تعادل پرانتز، کمکی تعریف‌نشده، تطابق
# import، فیلد ساختار، تابع تعریف‌نشده و ...). کامپایلر نیست ولی روی محیطی
# که Go ندارد تنها لایه دفاعی است.
if command -v node >/dev/null 2>&1 && [ -f "$ROOT_DIR/scripts/check-go.js" ]; then
  log "بررسی ساختاری..."
  node "$ROOT_DIR/scripts/check-go.js" "$DEST_DIR" || error "بررسی ساختاری شکست خورد"
fi

# --------- کامپایل واقعی ---------
if [ "${SKIP_COMPILE:-0}" = "1" ]; then
  success "sync و بررسی انجام شد (کامپایل رد شد)"
  exit 0
fi
command -v go >/dev/null 2>&1 || error "Go نصب نیست؛ برای رد کردن کامپایل SKIP_COMPILE=1 بگذارید"

FAILED=()
for cc in "${CONTRACTS[@]}"; do
  log "کامپایل $cc ..."
  if ! (cd "$DEST_DIR/$cc" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -mod=vendor -ldflags="-s -w" -o /tmp/_cc_check_$cc . 2>&1); then
    FAILED+=("$cc")
  else
    rm -f "/tmp/_cc_check_$cc"
    echo "  ✓ $cc"
  fi
done

if [ ${#FAILED[@]} -gt 0 ]; then
  error "کامپایل این قراردادها شکست خورد: ${FAILED[*]}"
fi
success "هر ${#CONTRACTS[@]} قرارداد کامپایل شدند و آماده deploy هستند"
