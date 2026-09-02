#!/bin/bash
# ══════════════════════════════════════════════════════════════════════
# patch-tls-detect.sh — server/config.js را وادار می‌کند وضعیت TLS را از
# config/.env بخواند، نه فقط از متغیر محیطی.
#
# مسئله
# ─────
# config.js این‌طور تصمیم می‌گیرد:
#
#     tlsEnabled: process.env.CORE_PEER_TLS_ENABLED === 'true'
#
# در سرویس داشبورد آن متغیر ست است (drop-in که set-tls.sh می‌سازد). ولی
# وقتی اسکریپتی را دستی از خط فرمان اجرا کنید — مثل gen-caliper-network.js
# — ست نیست، پس config.tlsEnabled برابر false می‌شود و:
#
#   • پروفایل‌های Caliper با grpc:// و بدون tlsCACerts ساخته می‌شوند
#   • کانفیگ‌های Tape با tls_ca_cert خالی ساخته می‌شوند
#
# شبکه TLS دارد، پس هر تراکنش رد می‌شود — بدون هیچ خطای گواهی، فقط
# «۰ موفق از ۵۰۰». تشخیصش سخت است چون هیچ‌چیز نمی‌گوید علت TLS است.
#
# config/.env منبع حقیقتی است که docker-compose هم از آن می‌خواند، پس
# همان‌جا را می‌پرسیم و متغیر محیطی فقط بازنویسی‌کننده می‌ماند.
#
# استفاده:
#   ./patch-tls-detect.sh
#   DRY_RUN=1 ./patch-tls-detect.sh
# ══════════════════════════════════════════════════════════════════════
set -uo pipefail

ROOT_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
CONFIG_JS="$ROOT_DIR/server/config.js"
DRY_RUN="${DRY_RUN:-0}"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $*"; }
warn() { echo -e "  ${YELLOW}!${NC} $*"; }
bad()  { echo -e "  ${RED}✗${NC} $*"; }

echo ""
echo "تشخیص TLS از config/.env"
[ "$DRY_RUN" = "1" ] && warn "DRY_RUN — چیزی نوشته نمی‌شود"
echo "────────────────────────────────────────────"

[ -f "$CONFIG_JS" ] || { bad "$CONFIG_JS نیست"; exit 1; }

# هر شکلی از اصلاح که .env را می‌خواند پذیرفته است — نه فقط شکل خودِ این
# اسکریپت. بدون این بررسی، اجرای دوباره روی فایلی که با روش دیگری اصلاح
# شده آن را خنثی می‌کرد.
if grep -qE "tlsFromEnvFile|NETWORK_TLS" "$CONFIG_JS"; then
    ok "از قبل اعمال شده (config.js وضعیت TLS را از .env می‌خواند)"
    exit 0
fi

if ! grep -q "CORE_PEER_TLS_ENABLED" "$CONFIG_JS"; then
    bad "الگوی tlsEnabled در config.js پیدا نشد — دستی بررسی کنید"
    exit 1
fi

if [ "$DRY_RUN" = "1" ]; then
    echo "  → افزودن tlsFromEnvFile و تغییر منبع tlsEnabled"
    exit 0
fi

cp "$CONFIG_JS" "$CONFIG_JS.bak-$(date +%Y%m%d-%H%M%S)"

node - "$CONFIG_JS" <<'NODEEOF'
const fs = require('fs');
const path = process.argv[2];
let s = fs.readFileSync(path, 'utf8');

const helper = `
/* وضعیت TLS از config/.env خوانده می‌شود، نه فقط از متغیر محیطی.
   دلیلش در scripts/patch-tls-detect.sh توضیح داده شده: اسکریپت‌هایی که
   دستی اجرا می‌شوند آن متغیر را ندارند و بی‌صدا پیکربندی بدون TLS
   می‌سازند. متغیر محیطی همچنان بازنویسی می‌کند. */
function tlsFromEnvFile() {
  try {
    const p = require('path').join(__dirname, '..', 'config', '.env');
    const m = require('fs').readFileSync(p, 'utf8')
      .match(/^NETWORK_TLS\\s*=\\s*(\\S+)/m);
    return m ? m[1].trim() === 'true' : null;
  } catch (_) {
    return null;
  }
}
`;

// helper پس از آخرین require سطح بالا
const lastRequire = s.lastIndexOf("require(");
const lineEnd = s.indexOf('\n', lastRequire);
s = s.slice(0, lineEnd + 1) + helper + s.slice(lineEnd + 1);

// منبع tlsEnabled
s = s.replace(
  /tlsEnabled:\s*process\.env\.CORE_PEER_TLS_ENABLED\s*===\s*'true'/,
  "tlsEnabled: process.env.CORE_PEER_TLS_ENABLED !== undefined\n"
  + "    ? process.env.CORE_PEER_TLS_ENABLED === 'true'\n"
  + "    : (tlsFromEnvFile() ?? false)"
);

fs.writeFileSync(path, s);
NODEEOF

if node --check "$CONFIG_JS" 2>/dev/null; then
    # تأیید کارکردی: مقدار واقعی باید با .env بخواند، نه فقط syntax سالم
    WANT="$(grep -oE '^NETWORK_TLS[[:space:]]*=[[:space:]]*\S+' \
        "$ROOT_DIR/config/.env" 2>/dev/null | tr -d ' ' | cut -d= -f2)"
    GOT="$(cd "$ROOT_DIR/server" && env -u CORE_PEER_TLS_ENABLED \
        node -e "console.log(require('./config').tlsEnabled)" 2>/dev/null)"
    if [ -n "$WANT" ] && [ "$GOT" != "$WANT" ]; then
        bad "config.js اصلاح شد ولی مقدار نمی‌خواند: .env=$WANT ولی tlsEnabled=$GOT"
        echo "     از پشتیبان بازگردانید: $(ls -t "$CONFIG_JS".bak-* 2>/dev/null | head -1)"
        exit 1
    fi
    ok "config.js اصلاح شد (tlsEnabled=$GOT)"
else
    bad "config.js پس از ویرایش معتبر نیست — از پشتیبان بازگردانید:"
    ls -t "$CONFIG_JS".bak-* 2>/dev/null | head -1
    exit 1
fi

echo ""
echo "────────────────────────────────────────────"
cat <<'NEXT'
حالا این‌ها را دوباره بسازید تا مسیر گواهی درست در آنها بنشیند:

  node gen-caliper-network.js
  ./fix-tape-policy.sh
  systemctl restart dashboard

بررسی:

  grep -c grpcs ../test-tools/caliper-workspace/networks/connection-profile-org1.json
  grep tls_ca_cert ../test-tools/tape-configs/config-datachannel.yaml

اولی باید عددی بیش از صفر بدهد و دومی مسیر یک فایل موجود.
NEXT
