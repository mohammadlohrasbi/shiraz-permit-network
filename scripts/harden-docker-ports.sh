#!/bin/bash
# harden-docker-ports.sh — مقید کردن پورت‌های publish شده فابریک به loopback
#
# چرا: Docker قوانین iptables خودش را می‌نویسد و از UFW عبور می‌کند؛ با فایروال
# فعال هم پورت‌های peer/orderer/CA (7050-14051) روی IP عمومی باز می‌مانند —
# آن هم در شبکه‌ای که TLS ندارد. چون همه اجزا (server، tape، caliper، CLI)
# روی همین هاست هستند، دسترسی از 127.0.0.1 برای همه‌چیز کافی است.
#
# چه می‌کند: در docker-compose.yml و docker-compose-root-ca.yml هر نگاشت
#   "PORT:PORT"  →  "127.0.0.1:PORT:PORT"
# با backup و اعتبارسنجی؛ اجرای چندباره بی‌ضرر است.
set -e

CONFIG_DIR="/root/shiraz-permit-network/config"
CHANGED=0

for f in docker-compose.yml docker-compose-root-ca.yml; do
    F="${CONFIG_DIR}/${f}"
    [ ! -f "$F" ] && { echo "⚠ یافت نشد: $F (رد شد)"; continue; }

    if grep -qE '"[0-9]{4,5}:[0-9]{4,5}"' "$F"; then
        cp "$F" "${F}.bak_$(date +%Y%m%d_%H%M%S)"
        sed -i -E 's/"([0-9]{4,5}):([0-9]{4,5})"/"127.0.0.1:\1:\2"/g' "$F"
        echo "✓ ${f}: پورت‌ها به 127.0.0.1 مقید شدند"
        CHANGED=1
    else
        echo "✓ ${f}: از قبل مقید است یا نگاشت پورتی ندارد"
    fi
done

# اعتبارسنجی compose
cd "$CONFIG_DIR"
if docker compose version >/dev/null 2>&1; then
    docker compose -f docker-compose.yml config -q && echo "✓ compose معتبر است"
elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose -f docker-compose.yml config -q && echo "✓ compose معتبر است"
else
    docker compose -f docker-compose.yml config -q && echo "✓ compose معتبر است"
fi

if [ "$CHANGED" -eq 1 ]; then
    echo ""
    echo "برای اعمال بدون از دست دادن لجر (volume ها حفظ می‌شوند):"
    echo "  cd ${CONFIG_DIR}"
    echo "  docker compose up -d      # (v2؛ نسخه v1 در recreate باگ ContainerConfig دارد)"
    echo ""
    echo "بعد از اعمال، از بیرونِ سرور پورت 7051 نباید در دسترس باشد ولی"
    echo "همه ابزارهای روی هاست (server/tape/caliper/CLI) مثل قبل کار می‌کنند."
fi
