#!/bin/bash
# ══════════════════════════════════════════════════════════════════════
# set-tls.sh — TLS را روشن یا خاموش می‌کند.
#
# جانشین enable-tls.sh است و کار بسیار کمتری می‌کند، چون حالا خود فایل‌ها
# پارامتریک‌اند:
#
#   docker-compose.yml   TLS_ENABLED=${NETWORK_TLS} می‌خواند و پوشه
#                        گواهی هر نود را همیشه mount می‌کند
#   network.sh           گواهی همه نودها را همیشه می‌سازد
#
# پس تنها چیزی که می‌ماند: یک خط در .env، فلگ دستورهای CLI، و متغیر
# سرویس داشبورد.
#
#   ./set-tls.sh on
#   ./set-tls.sh off
# ══════════════════════════════════════════════════════════════════════
set -uo pipefail

ROOT_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
CONFIG="$ROOT_DIR/config"
SCRIPTS="$ROOT_DIR/scripts"
MODE="${1:-on}"
C_TLS_PEER="/etc/hyperledger/fabric/tls"
# مسیر گواهی ریشه orderer **از دید کانتینر peer**.
#
# دستورهای peer داخل کانتینر peer اجرا می‌شوند، نه orderer. مسیر
# /var/hyperledger/orderer/tls فقط داخل خود orderer وجود دارد، پس
# --cafile با آن مسیر شکست می‌خورد:
#
#     unable to load orderer.tls.rootcert.file: no such file or directory
#
# docker-compose همان پوشه را با نام دیگری در peer ها هم mount می‌کند.
ORD_CA="/etc/hyperledger/fabric/orderer-tls/ca.crt"

GREEN='\033[0;32m'; RED='\033[0;31m'; NC='\033[0m'
ok()  { echo -e "  ${GREEN}✓${NC} $*"; }
die() { echo -e "  ${RED}✗${NC} $*"; exit 1; }

case "$MODE" in on|off) ;; *) die "حالت باید on یا off باشد" ;; esac
# .env اگر نبود ساخته می‌شود. network.sh پوشه config را بازسازی می‌کند و
# آن را با خود می‌برد، پس مردن اینجا یعنی راه‌اندازی از صفر همیشه در گام
# سوم متوقف شود — در حالی که تنها کار لازم نوشتن یک خط است.
[ -d "$CONFIG" ] || die "$CONFIG نیست — اول network.sh را اجرا کنید"
if [ ! -f "$CONFIG/.env" ]; then
    printf '# پیکربندی شبکه — docker-compose از اینجا می‌خواند\nNETWORK_TLS=false\n' \
        > "$CONFIG/.env"
    ok ".env ساخته شد"
fi

echo ""
echo "$([ "$MODE" = on ] && echo روشن || echo خاموش) کردن TLS"
echo "────────────────────────────────────────────"

# ── ۱) .env ──
VAL=$([ "$MODE" = on ] && echo true || echo false)
if grep -q "^NETWORK_TLS=" "$CONFIG/.env"; then
    sed -i "s/^NETWORK_TLS=.*/NETWORK_TLS=$VAL/" "$CONFIG/.env"
else
    echo "NETWORK_TLS=$VAL" >> "$CONFIG/.env"
fi
ok ".env → NETWORK_TLS=$VAL"

# ── ۲) فلگ دستورهای CLI ──
# دو دسته با دو نیاز متفاوت: آنهایی که با orderer حرف می‌زنند گواهی
# اوردرر می‌خواهند، آنهایی که فقط با peer کار دارند ریشه اعتماد peer.
for f in deploy_functions.sh deploy-staged.sh seed-network.sh upgrade-spatial.sh; do
    p="$SCRIPTS/$f"
    [ -f "$p" ] || continue
    sed -i "s| --tls --cafile [^ ]*||g" "$p"
    sed -i "/^ *-e CORE_PEER_TLS_ROOTCERT_FILE=[^ ]* \\\\$/d" "$p"
    sed -i "s|-e CORE_PEER_TLS_ENABLED=true|-e CORE_PEER_TLS_ENABLED=false|g" "$p"
    if [ "$MODE" = "on" ]; then
        sed -i "s|\(peer channel create\)|\1 --tls --cafile $ORD_CA|g;
                s|\(peer channel update\)|\1 --tls --cafile $ORD_CA|g;
                s|\(chaincode approveformyorg\)|\1 --tls --cafile $ORD_CA|g;
                s|\(chaincode commit\)|\1 --tls --cafile $ORD_CA|g;
                s|\(peer chaincode invoke\)|\1 --tls --cafile $ORD_CA|g" "$p"
        sed -i "s|-e CORE_PEER_TLS_ENABLED=false|-e CORE_PEER_TLS_ENABLED=true|g" "$p"
        sed -i "s|^\( *\)\(-e CORE_PEER_ADDRESS=\)|\1-e CORE_PEER_TLS_ROOTCERT_FILE=$C_TLS_PEER/ca.crt \\\\\n\1\2|g" "$p"
    fi
    ok "$f"
done

# ── ۲ب) گواهی به‌ازای هر peer ──
#
# با TLS روشن، هر --peerAddresses باید یک --tlsRootCertFiles همراه داشته
# باشد وگرنه:
#
#     number of peer addresses (8) does not match
#     the number of TLS root cert files (1)
#
# اسکریپت‌ها PEER_ARGS را در حلقه می‌سازند، پس همان‌جا جفتش را اضافه
# می‌کنیم. هر peer گواهی ریشه خودش را دارد، ولی همه از یک CA آمده‌اند و
# فابریک فقط ریشه را تأیید می‌کند — پس گواهی peer محلی برای همه کار
# می‌کند و نیازی به mount هشت پوشه نیست.
PEER_LINE='PEER_ARGS="$PEER_ARGS --peerAddresses peer0.org${i}.example.com:${ORG_PORTS[$i]}"'
PEER_LINE_TLS='PEER_ARGS="$PEER_ARGS --peerAddresses peer0.org${i}.example.com:${ORG_PORTS[$i]} --tlsRootCertFiles '"$C_TLS_PEER"'/ca.crt"'

for f in seed-network.sh upgrade-spatial.sh network.sh deploy_functions.sh deploy-staged.sh; do
    p="$SCRIPTS/$f"
    [ -f "$p" ] || continue
    # اول هر جفت قبلی را بردار تا اجرای دوباره تکرارش نکند
    sed -i "s| --tlsRootCertFiles [^\"]*||g" "$p"
    if [ "$MODE" = "on" ]; then
        python3 - "$p" "$C_TLS_PEER" <<'PYEOF'
import sys, re
path, tls = sys.argv[1], sys.argv[2]
s = open(path).read()
# هر خطی که --peerAddresses را به PEER_ARGS اضافه می‌کند
pat = re.compile(r'(--peerAddresses peer0\.org\$\{i\}\.example\.com:\$\{ORG_PORTS\[\$i\]\})')
s2 = pat.sub(r'\1 --tlsRootCertFiles ' + tls + '/ca.crt', s)
if s2 != s:
    open(path, 'w').write(s2)
PYEOF
    fi
done
ok "گواهی به‌ازای هر peer در دستورهای چندنقطه‌ای"

# ── ۲د) احراز هویت کلاینت در دستورهای رو به اوردرر ──
#
# سرویس خوشه Raft روی پورت عمومی اوردرر است، پس فابریک احراز هویت کلاینت
# را روی آن پورت لازم می‌کند — نه فقط برای نودهای خوشه، بلکه برای هر
# اتصالی. دستورهای peer باید گواهی خودشان را بفرستند:
#
#     tls: client didn't provide a certificate
#
# متغیرهای محیطی CORE_PEER_TLS_CLIENTCERT_FILE در docker-compose برای
# کانتینر peer کار می‌کنند، ولی peer CLI در دستورهای رو به اوردرر فلگ
# صریح می‌خواهد.
for f in deploy_functions.sh deploy-staged.sh seed-network.sh upgrade-spatial.sh; do
    p="$SCRIPTS/$f"
    [ -f "$p" ] || continue
    sed -i "s| --clientauth --certfile [^ ]* --keyfile [^ ]*||g" "$p"
    if [ "$MODE" = "on" ]; then
        sed -i "s|\(--tls --cafile [^ ]*\)|\1 --clientauth --certfile $C_TLS_PEER/server.crt --keyfile $C_TLS_PEER/server.key|g" "$p"
    fi
done
ok "احراز هویت کلاینت در دستورهای رو به اوردرر"

# ── ۲ج) مهلت انتظار رویداد ──
#
# با Raft هر تراکنش پیش از کامیت باید در اکثریت نودها تکرار شود، و با TLS
# هر اتصال یک دست‌تکانی رمزنگاری اضافه دارد. مهلت پیش‌فرض ۳۰ ثانیه برای
# approve هشت سازمان کافی نیست و به این شکل ظاهر می‌شود:
#
#     DeadlineExceeded ... stream terminated by RST_STREAM
#
# افزودن --waitForEventTimeout مشکل را حل می‌کند بی‌آنکه رفتار دیگری
# عوض شود.
for f in deploy_functions.sh deploy-staged.sh upgrade-spatial.sh; do
    p="$SCRIPTS/$f"
    [ -f "$p" ] || continue
    sed -i "s| --waitForEventTimeout [0-9]*[a-z]*||g" "$p"
    if [ "$MODE" = "on" ]; then
        sed -i "s|--waitForEvent\b|--waitForEvent --waitForEventTimeout 300s|g" "$p"
    fi
done
ok "مهلت انتظار رویداد"

# ── ۳) سرویس داشبورد ──
# config.js مقدار tlsEnabled را از این متغیر می‌خواند، و bench-runner و
# gen-caliper-network هر دو از config.js تبعیت می‌کنند.
UNIT="/etc/systemd/system/dashboard.service.d/tls.conf"
if [ "$MODE" = "on" ]; then
    mkdir -p "$(dirname "$UNIT")"
    printf '[Service]\nEnvironment=CORE_PEER_TLS_ENABLED=true\n' > "$UNIT"
else
    rm -f "$UNIT"
fi
systemctl daemon-reload 2>/dev/null
ok "سرویس داشبورد"

echo ""
echo -e "${GREEN}TLS $([ "$MODE" = on ] && echo روشن || echo خاموش) شد.${NC}"
cat <<NEXT

اعمال:
  cd $CONFIG && docker compose down && docker compose up -d
  systemctl restart dashboard
  cd $SCRIPTS && node gen-caliper-network.js && ./fix-tape-policy.sh

بررسی:
  docker exec peer0.org1.example.com env | grep TLS_ENABLED
NEXT
