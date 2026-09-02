#!/bin/bash
# ══════════════════════════════════════════════════════════════════════
# fix-paths.sh — مسیرهای کدشده در فایل‌های مخزن را با محل واقعی پروژه
# هم‌راستا می‌کند.
#
# مسئله
# ─────
# چند اسکریپت مخزن مسیر پروژه را به‌صورت ثابت در خود دارند:
#
#     source /root/shiraz-permit-network/scripts/channel_contract_map.sh
#
# اگر پروژه جای دیگری باشد — مثلاً /root/shiraz-permit-network — این خط به فایلی
# اشاره می‌کند که آنجا نیست، و اسکریپت با «No such file or directory»
# می‌ایستد. بدتر اینکه اگر نسخه قدیمی پروژه هنوز در مسیر اصلی باشد، فایل
# **پیدا می‌شود** و اسکریپت با پیکربندی اشتباه ادامه می‌دهد.
#
# راه‌حل: مسیر از محل خود اسکریپت مشتق شود، نه از یک ثابت.
#
# استفاده:
#   ./fix-paths.sh                 # مسیر را از محل این اسکریپت می‌گیرد
#   ./fix-paths.sh /path/to/repo
#   DRY_RUN=1 ./fix-paths.sh
# ══════════════════════════════════════════════════════════════════════
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${1:-$(cd "$HERE/.." && pwd)}"
DRY_RUN="${DRY_RUN:-0}"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $*"; }
warn() { echo -e "  ${YELLOW}!${NC} $*"; }
bad()  { echo -e "  ${RED}✗${NC} $*"; }

echo ""
echo "هم‌راستاسازی مسیرها با $ROOT"
[ "$DRY_RUN" = "1" ] && warn "DRY_RUN — چیزی نوشته نمی‌شود"
echo "────────────────────────────────────────────"

[ -d "$ROOT/scripts" ] || { bad "$ROOT/scripts نیست"; exit 1; }

STAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP="$ROOT/.path-backup-$STAMP"
FOUND=0
FIXED=0

# هر مسیری که به یک پروژه 6g اشاره می‌کند ولی این پروژه نیست
PATTERN='/root/shiraz-permit-network[a-zA-Z0-9_-]*'

for f in "$ROOT"/scripts/*.sh "$ROOT"/server/*.sh; do
    [ -f "$f" ] || continue
    # مسیرهایی که با ROOT فعلی فرق دارند
    hits=$(grep -oE "$PATTERN" "$f" 2>/dev/null | grep -v "^$ROOT\$" | sort -u)
    [ -z "$hits" ] && continue

    FOUND=$((FOUND+1))
    name="$(basename "$f")"
    echo ""
    echo "  $name"
    while IFS= read -r h; do
        [ -z "$h" ] && continue
        n=$(grep -c "$h" "$f")
        echo "    $h  ($n مورد)"
    done <<< "$hits"

    if [ "$DRY_RUN" = "1" ]; then
        continue
    fi

    mkdir -p "$BACKUP/$(dirname "${f#$ROOT/}")"
    cp "$f" "$BACKUP/${f#$ROOT/}"

    # مسیر ثابت با ROOT واقعی جایگزین می‌شود. جایگزینی ساده کافی است و
    # امن‌تر از تلاش برای پارامتریک کردن اسکریپتی است که ننوشته‌ایم.
    sed -i "s|$PATTERN|$ROOT|g" "$f"
    FIXED=$((FIXED+1))
    ok "اصلاح شد"
done

echo ""
echo "────────────────────────────────────────────"
if [ "$FOUND" = "0" ]; then
    ok "هیچ مسیر ناهم‌راستایی نبود"
    exit 0
fi
if [ "$DRY_RUN" = "1" ]; then
    echo "$FOUND فایل مسیر ناهم‌راستا دارند. برای اصلاح بدون DRY_RUN بزنید."
    exit 0
fi
echo "اصلاح‌شده: $FIXED فایل"
[ -d "$BACKUP" ] && echo "پشتیبان: $BACKUP"
