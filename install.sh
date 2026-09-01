#!/bin/bash
# ══════════════════════════════════════════════════════════════════════
# install.sh — فایل‌های این بسته را در مخزن سرجایشان می‌گذارد.
#
# فقط کپی می‌کند و وابستگی نصب می‌کند. هیچ قراردادی تولید نمی‌شود، هیچ
# چیزی مستقر نمی‌شود، هیچ کانتینری لمس نمی‌شود — آن گام‌ها در RUNBOOK.md
# هستند و باید آگاهانه اجرا شوند.
#
# از هر فایلی که جایگزین می‌شود پشتیبان گرفته می‌شود.
#
# استفاده:
#   ./install.sh                    # به /root/shiraz-permit-network
#   ./install.sh /path/to/repo      # جای دیگر
#   DRY_RUN=1 ./install.sh          # فقط نشان بده چه می‌کند
# ══════════════════════════════════════════════════════════════════════
set -uo pipefail

TARGET="${1:-/root/shiraz-permit-network}"
DRY_RUN="${DRY_RUN:-0}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP="$TARGET/.backup-$STAMP"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $*"; }
warn() { echo -e "  ${YELLOW}!${NC} $*"; }
bad()  { echo -e "  ${RED}✗${NC} $*"; }

echo ""
echo "نصب بسته 6G Fabric"
echo "  مقصد: $TARGET"
[ "$DRY_RUN" = "1" ] && echo -e "  ${YELLOW}حالت DRY_RUN — چیزی نوشته نمی‌شود${NC}"
echo "────────────────────────────────────────────"

# ── بررسی مقصد ──
if [ ! -d "$TARGET" ]; then
  bad "پوشه $TARGET وجود ندارد."
  echo ""
  echo "  اگر هنوز مخزن را کلون نکرده‌اید:"
  echo "    git clone https://github.com/mohammadlohrasbi/shiraz-network.git $TARGET"
  exit 1
fi
for d in scripts server public config; do
  if [ ! -d "$TARGET/$d" ]; then
    bad "$TARGET/$d نیست — این مخزن shiraz-network نیست."
    exit 1
  fi
done
ok "ساختار مخزن تأیید شد"

# ── ابزارهای لازم ──
MISSING=()
for t in node docker go; do
  command -v "$t" >/dev/null 2>&1 || MISSING+=("$t")
done
if [ ${#MISSING[@]} -gt 0 ]; then
  warn "در دسترس نیست: ${MISSING[*]}"
  echo "     نصب: apt-get install -y nodejs docker.io golang"
  echo "     کپی ادامه می‌یابد، ولی گام‌های RUNBOOK بدون اینها کار نمی‌کنند."
else
  ok "node، docker و go در دسترس‌اند"
fi

echo ""
echo "کپی فایل‌ها"
echo "────────────────────────────────────────────"

COPIED=0
SAME=0

copy_one() {
  local src="$1" dst="$2" name
  name="$(basename "$src")"

  if [ -f "$dst" ] && cmp -s "$src" "$dst"; then
    SAME=$((SAME+1))
    return
  fi

  if [ "$DRY_RUN" = "1" ]; then
    if [ -f "$dst" ]; then echo "  → جایگزینی $name"; else echo "  → جدید $name"; fi
    COPIED=$((COPIED+1))
    return
  fi

  # پشتیبان فقط از فایلی که واقعاً عوض می‌شود
  if [ -f "$dst" ]; then
    mkdir -p "$(dirname "$BACKUP/${dst#$TARGET/}")"
    cp "$dst" "$BACKUP/${dst#$TARGET/}"
  fi
  cp "$src" "$dst"
  COPIED=$((COPIED+1))
}

for d in scripts server public config; do
  [ -d "$HERE/$d" ] || continue
  n=0
  # الگوی * فایل‌های نقطه‌دار را نمی‌گیرد، و config/.env دقیقاً یکی از
  # آنهاست — بدون آن docker-compose پارامترهای TLS را نمی‌بیند و شبکه
  # بی‌صدا plaintext بالا می‌آید.
  # find به‌جای گلاب، تا زیرپوشه‌ها هم بیایند.
  #
  # نسخه قبلی «[ -f ] || continue» داشت که پوشه‌ها را رد می‌کرد — یعنی
  # scripts/builders/golang/bin/run هرگز کپی نمی‌شد. آن فایل بیلدر خارجی
  # است و بدون نسخه TLS-آگاهش، chaincode با «container exited with 0»
  # شکست می‌خورد. فایل در بسته بود ولی به سرور نمی‌رسید.
  while IFS= read -r f; do
    rel="${f#$HERE/$d/}"
    mkdir -p "$TARGET/$d/$(dirname "$rel")"
    copy_one "$f" "$TARGET/$d/$rel"
    n=$((n+1))
  done < <(find "$HERE/$d" -type f 2>/dev/null)
  ok "$d/ — $n فایل"
done

# اسناد کنار مخزن، نه داخل مسیرهای اجرایی
if [ "$DRY_RUN" != "1" ]; then
  mkdir -p "$TARGET/docs"
  cp "$HERE"/docs/*.md "$TARGET/docs/" 2>/dev/null
  cp "$HERE/RUNBOOK.md" "$TARGET/" 2>/dev/null
  mkdir -p "$TARGET/reference"
  cp "$HERE"/reference/* "$TARGET/reference/" 2>/dev/null
  chmod +x "$TARGET"/scripts/*.sh "$TARGET/server/patch-index.sh" 2>/dev/null
fi
ok "اسناد و مراجع"

echo ""
echo "وابستگی"
echo "────────────────────────────────────────────"
if [ "$DRY_RUN" = "1" ]; then
  echo "  → npm install js-yaml در server/"
elif node -e "require('$TARGET/server/node_modules/js-yaml')" 2>/dev/null; then
  ok "js-yaml از قبل نصب است"
else
  if (cd "$TARGET/server" && npm install js-yaml >/dev/null 2>&1); then
    ok "js-yaml نصب شد"
  else
    warn "نصب js-yaml ناموفق — روتر /api/bench بدون آن بالا نمی‌آید"
    echo "     دستی: cd $TARGET/server && npm install js-yaml"
  fi
fi

echo ""
echo "بررسی سلامت"
echo "────────────────────────────────────────────"
FAIL=0
if command -v node >/dev/null 2>&1; then
  for f in "$TARGET"/server/*.js "$TARGET"/scripts/*.js "$TARGET"/public/test-app.js; do
    [ -f "$f" ] || continue
    node --check "$f" >/dev/null 2>&1 || { bad "syntax: $(basename "$f")"; FAIL=1; }
  done
  [ "$FAIL" = "0" ] && ok "syntax همه فایل‌های JS سالم"
fi
for f in "$TARGET"/scripts/*.sh "$TARGET/server/patch-index.sh"; do
  [ -f "$f" ] || continue
  bash -n "$f" 2>/dev/null || { bad "bash: $(basename "$f")"; FAIL=1; }
done
[ "$FAIL" = "0" ] && ok "syntax همه اسکریپت‌های bash سالم"

echo ""
echo "────────────────────────────────────────────"
if [ "$DRY_RUN" = "1" ]; then
  echo "$COPIED فایل کپی می‌شد، $SAME فایل از قبل یکسان بود."
  echo "برای اجرای واقعی: ./install.sh $TARGET"
  exit 0
fi

echo "کپی‌شده: $COPIED | بدون تغییر: $SAME"
[ -d "$BACKUP" ] && echo "پشتیبان: $BACKUP"

if [ "$FAIL" != "0" ]; then
  bad "برخی فایل‌ها خطای syntax دارند — پیش از ادامه بررسی کنید."
  exit 1
fi

echo ""
echo -e "${GREEN}نصب فایل‌ها کامل شد.${NC}"
echo ""
echo "هیچ قراردادی تولید نشد و هیچ‌چیز مستقر نشد. گام بعد را از RUNBOOK.md"
echo "بخوانید و مسیر خود را انتخاب کنید:"
echo ""
echo "  شبکه ندارید یا از نو می‌سازید  →  مسیر A (نصب تازه)"
echo "  شبکه بالاست و کانال‌ها مستقرند →  مسیر B (ارتقا)"
echo ""
echo "  cat $TARGET/RUNBOOK.md"
