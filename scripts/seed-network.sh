#!/bin/bash
# ═══════════════════════════════════════════════════════════════════════════
# seed-network.sh — بارگذاری داده پایه شبکه پروانه ساختمانی شیراز
#
# شبکه بدون این داده کار نمی‌کند: بدون منطقه، پهنه‌ای ثبت نمی‌شود؛ بدون پهنه،
# پلاکی معنا ندارد؛ بدون دفترچه تعرفه، موتور عوارض تقسیم بر صفر می‌کند.
#
# ⚠️ خیلی مهم — درباره اعداد این فایل:
# قیمت‌های منطقه‌ای و ضرایب تعرفه اینجا «نمونه ساختاری» هستند، نه رونوشت
# دفترچه رسمی. شکل داده و روابط بین اعداد درست است، ولی خودِ ارقام باید پیش
# از هر استفاده واقعی از این دو منبع جایگزین شوند:
#   • دفترچه ارزش معاملاتی املاک شهر شیراز (ستون‌های P و BP)
#   • مصوبه شورای اسلامی شهر شیراز درباره «آیین‌نامه نحوه محاسبه عوارض صدور
#     پروانه ساختمانی، عوارض تراکم، عوارض تجاری و ساختمان‌های غیرمسکونی»
# جایگزینی فقط یعنی ویرایش همین فایل و اجرای دوباره‌اش — قرارداد دست نمی‌خورد،
# چون تعرفه داده است نه کد. این عمدی است: هر سال تعرفه عوض می‌شود و نباید
# هر سال شبکه بازسازی شود.
#
# استفاده:
#   ./seed-network.sh                 # همه: مناطق، پهنه‌ها، تعرفه، سقف‌ها
#   ./seed-network.sh regions         # فقط مناطق
#   ./seed-network.sh zones           # فقط پهنه‌ها
#   ./seed-network.sh tariff          # فقط دفترچه تعرفه
#   ./seed-network.sh quotas          # فقط سقف انتشار توکن
#   YEAR=1406 ./seed-network.sh tariff
#   VERIFY_ONLY=1 ./seed-network.sh   # فقط گزارش وضعیت، بدون نوشتن
# ═══════════════════════════════════════════════════════════════════════════
set -e

ROOT_DIR="${ROOT_DIR:-/root/shiraz-permit-network}"
CHANNEL="${CHANNEL:-permitchannel}"
YEAR="${YEAR:-1405}"
VERIFY_ONLY="${VERIFY_ONLY:-0}"
WHAT="${1:-all}"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
log()     { echo -e "[$(date +'%H:%M:%S')] $*"; }
success() { log "${GREEN}موفق${NC}: $*"; }
warn()    { log "${YELLOW}هشدار${NC}: $*"; }
error()   { log "${RED}خطا${NC}: $*"; exit 1; }

# ---------------------------------------------------------------------------
# تشخیص TLS از همان منبعی که docker-compose می‌خواند.
# درسی که در پروژه 6G گران تمام شد: ابزارها CORE_PEER_TLS_ENABLED را از محیط
# سرویس می‌خواندند و وقتی از خط فرمان اجرا می‌شدند ست نبود، پس بی‌صدا پیکربندی
# بدون TLS می‌ساختند و نتیجه‌اش «۰ موفق بدون هیچ خطای گواهی» بود.
# ---------------------------------------------------------------------------
ENV_FILE="$ROOT_DIR/config/.env"
NETWORK_TLS="false"
if [ -f "$ENV_FILE" ]; then
  NETWORK_TLS=$(grep -E '^NETWORK_TLS=' "$ENV_FILE" | tail -1 | cut -d= -f2 | tr -d '"' | tr -d "'" || echo "false")
fi
NETWORK_TLS="${NETWORK_TLS:-false}"

ORDERER_CA="/etc/hyperledger/fabric/orderer-tls/ca.crt"
PEER_TLS_DIR="/etc/hyperledger/fabric/tls"
TLS_ARGS=""
TLS_ENV="-e CORE_PEER_TLS_ENABLED=false"
if [ "$NETWORK_TLS" = "true" ]; then
  TLS_ARGS="--tls --cafile $ORDERER_CA --clientauth --certfile $PEER_TLS_DIR/server.crt --keyfile $PEER_TLS_DIR/server.key"
  TLS_ENV="-e CORE_PEER_TLS_ENABLED=true -e CORE_PEER_TLS_ROOTCERT_FILE=$PEER_TLS_DIR/ca.crt -e CORE_PEER_TLS_CLIENTCERT_FILE=$PEER_TLS_DIR/server.crt -e CORE_PEER_TLS_CLIENTKEY_FILE=$PEER_TLS_DIR/server.key"
fi
log "حالت TLS: $NETWORK_TLS"

# ---------------------------------------------------------------------------
# invoke — همیشه از org1 (نقش REGULATOR). همه توابع این اسکریپت
# requireRole(RoleRegulator) دارند، پس با سازمان دیگری رد می‌شوند.
# ---------------------------------------------------------------------------
invoke() {
  local cc="$1"; shift
  local args_json="$1"; shift
  local label="${1:-$cc}"

  if [ "$VERIFY_ONLY" = "1" ]; then
    echo "  [DRY] $cc ← $(echo "$args_json" | cut -c1-110)..."
    return 0
  fi

  local out
  if out=$(docker exec \
      -e CORE_PEER_LOCALMSPID=org1MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
      $TLS_ENV \
      peer0.org1.example.com peer chaincode invoke \
        -o orderer.example.com:7050 \
        -C "$CHANNEL" -n "$cc" \
        $TLS_ARGS \
        -c "$args_json" 2>&1); then
    if echo "$out" | grep -q "status:200"; then
      echo -e "  ${GREEN}✓${NC} $label"
      return 0
    fi
  fi
  echo -e "  ${RED}✗${NC} $label"
  echo "$out" | grep -oP 'message:"[^"]*"' | head -1 | sed 's/^/      /'
  FAILURES=$((FAILURES + 1))
  return 0   # ادامه می‌دهیم تا کل تصویر خطاها یکجا دیده شود
}

FAILURES=0

# ═══════════════════════════════════════════════════════════════════════════
# ۱) مناطق یازده‌گانه شهرداری شیراز
#
# قالب هر ردیف:  کد|عنوان|مساحت به هکتار|قیمت منطقه‌ای هر کاربری (ریال/م²)
#
# قیمت منطقه‌ای (P) پایه تقریباً همه فرمول‌های عوارض است. اختلاف قیمت بین
# منطقه ۱ (مرکز تاریخی) و منطقه ۸ (حریم) عمداً زیاد است تا اثر پهنه‌بندی در
# محاسبات دیده شود.
# ═══════════════════════════════════════════════════════════════════════════
REGIONS_DATA=(
"1|منطقه ۱ — مرکز تاریخی، بازار وکیل و شاهچراغ|1850|RES:52000000,COM:240000000,OFF:118000000,MIX:145000000,TUR:160000000,EDU:40000000,HLT:60000000,REL:30000000,SPT:38000000,IND:45000000"
"2|منطقه ۲ — سعدی، دروازه اصفهان و شمال شرق|2400|RES:38000000,COM:150000000,OFF:82000000,MIX:98000000,TUR:95000000,EDU:32000000,HLT:48000000,REL:24000000,SPT:30000000,IND:40000000"
"3|منطقه ۳ — قصردشت، باغات و محور ارم|2950|RES:61000000,COM:215000000,OFF:126000000,MIX:150000000,TUR:175000000,EDU:44000000,HLT:66000000,REL:32000000,SPT:42000000,IND:38000000"
"4|منطقه ۴ — شرق، سلطان‌آباد و بزرگراه امام خمینی|3100|RES:33000000,COM:118000000,OFF:68000000,MIX:82000000,TUR:74000000,EDU:28000000,HLT:42000000,REL:20000000,SPT:26000000,IND:52000000"
"5|منطقه ۵ — معالی‌آباد، قدوسی و شمال شهر|2600|RES:78000000,COM:280000000,OFF:158000000,MIX:190000000,TUR:205000000,EDU:52000000,HLT:80000000,REL:36000000,SPT:50000000,IND:44000000"
"6|منطقه ۶ — غرب، شهرک گلستان و محور صدرا|3400|RES:57000000,COM:190000000,OFF:112000000,MIX:135000000,TUR:130000000,EDU:40000000,HLT:60000000,REL:28000000,SPT:38000000,IND:48000000"
"7|منطقه ۷ — جنوب غرب، شهرک صنعتی و بزرگراه|3800|RES:29000000,COM:98000000,OFF:56000000,MIX:70000000,TUR:60000000,EDU:24000000,HLT:36000000,REL:18000000,SPT:22000000,IND:62000000"
"8|منطقه ۸ — حریم شمالی و اراضی توسعه|5200|RES:22000000,COM:72000000,OFF:44000000,MIX:54000000,TUR:66000000,EDU:20000000,HLT:30000000,REL:15000000,SPT:20000000,IND:40000000"
"9|منطقه ۹ — جنوب، دروازه کازرون و شهرک والفجر|2700|RES:26000000,COM:88000000,OFF:50000000,MIX:62000000,TUR:54000000,EDU:22000000,HLT:33000000,REL:16000000,SPT:20000000,IND:46000000"
"10|منطقه ۱۰ — شمال غرب، محدوده طرح ساماندهی|4100|RES:35000000,COM:120000000,OFF:70000000,MIX:86000000,TUR:80000000,EDU:28000000,HLT:44000000,REL:21000000,SPT:28000000,IND:44000000"
"11|منطقه ۱۱ — جنوب شرق و محور فرودگاه|3600|RES:24000000,COM:82000000,OFF:48000000,MIX:58000000,TUR:56000000,EDU:20000000,HLT:31000000,REL:15000000,SPT:19000000,IND:50000000"
)

seed_regions() {
  log "── مناطق یازده‌گانه ──"
  for row in "${REGIONS_DATA[@]}"; do
    IFS='|' read -r code title hect prices <<< "$row"
    # تبدیل «RES:123,COM:456» به JSON
    local pj="{"
    IFS=',' read -ra pairs <<< "$prices"
    local first=1
    for p in "${pairs[@]}"; do
      IFS=':' read -r k v <<< "$p"
      [ $first -eq 0 ] && pj="$pj,"
      pj="$pj\"$k\":$v"
      first=0
    done
    pj="$pj}"
    invoke Regulation \
      "{\"function\":\"SetRegion\",\"Args\":[\"$code\",\"$title\",\"$hect\",\"$(echo "$pj" | sed 's/"/\\"/g')\"]}" \
      "منطقه $code"
  done
}

# ═══════════════════════════════════════════════════════════════════════════
# ۲) پهنه‌های طرح تفصیلی
#
# قالب: کد|منطقه|عنوان|کاربری‌های مجاز|تراکم(در هزار)|سطح اشغال(در هزار)|
#        حداکثر طبقات|حداقل تفکیک(م²)|عقب‌نشینی(سانتی‌متر)|
#        میراثی|گسلی|فرستنده TDR|گیرنده TDR|سقف دریافت TDR(در هزار)
#
# سه نکته که در انتخاب این اعداد تعیین‌کننده بود:
#
# • «تراکم در هزار» یعنی ۲۴۰۰ ⇒ تراکم ۲۴۰٪. اعداد صحیح نگه داشته شده‌اند تا
#   هیچ‌جا float وارد محاسبه نشود.
#
# • پهنه‌های باغی (GRD*) فرستنده TDR اند و گیرنده نیستند: مالک باغ حق توسعه
#   دارد ولی در باغ نمی‌سازد. این همان اهرمی است که باغات قصردشت را به‌جای
#   «زمین بلااستفاده که صاحبش را به تخلف می‌کشاند» به دارایی قابل معامله
#   تبدیل می‌کند.
#
# • پهنه‌های میراثی محدوده ثبت جهانی نه می‌فرستند نه می‌گیرند و سقف طبقاتشان
#   کم است — حریم منظری شاهچراغ و بازار وکیل با فروش تراکم قابل معاوضه نیست.
# ═══════════════════════════════════════════════════════════════════════════
ZONES_DATA=(
# --- منطقه ۱: مرکز تاریخی ---
"H101|1|بافت تاریخی — حریم بازار وکیل و شاهچراغ|RES,TUR,REL|1200|500|2|150|0|true|false|false|false|0"
"H102|1|بافت تاریخی درجه دو — پیرامون حرم|RES,COM,TUR,MIX|1800|600|3|120|100|true|false|true|false|0"
"R110|1|مسکونی متوسط مرکز|RES,MIX|2400|600|4|150|200|false|false|false|true|300"
"S110|1|تجاری محور اصلی مرکز|COM,OFF,MIX|3200|700|5|200|300|false|false|false|true|400"
# --- منطقه ۲ ---
"R210|2|مسکونی عام سعدی|RES|2400|600|4|180|200|false|false|false|true|300"
"R220|2|مسکونی کم‌تراکم شیب شمالی|RES|1800|500|3|250|200|false|true|false|false|0"
"S210|2|تجاری محور دروازه اصفهان|COM,OFF,MIX|3000|700|5|200|300|false|false|false|true|400"
# --- منطقه ۳: قصردشت و باغات ---
"GRD301|3|باغات قصردشت — حوزه حفاظت|GRD,AGR|0|50|1|1000|0|false|false|true|false|0"
"GRD302|3|باغات حاشیه محور ارم|GRD,AGR,TUR|300|100|1|800|0|false|false|true|false|0"
"R310|3|مسکونی ویژه ارم|RES,MIX|2800|600|5|200|300|false|false|false|true|350"
"S310|3|تجاری محور قصردشت|COM,OFF,MIX,TUR|3400|700|6|250|400|false|false|false|true|400"
# --- منطقه ۴ ---
"R410|4|مسکونی عام شرق|RES|2400|600|4|180|200|false|false|false|true|300"
"M410|4|مختلط محور امام خمینی|MIX,COM,OFF,RES|3000|650|5|200|400|false|false|false|true|400"
"I410|4|کارگاهی و انبارداری|IND|1200|500|2|500|500|false|false|false|false|0"
# --- منطقه ۵: پرقیمت‌ترین ---
"R510|5|مسکونی معالی‌آباد|RES,MIX|3000|600|6|250|300|false|false|false|true|400"
"R520|5|مسکونی کم‌تراکم قدوسی|RES|2000|500|4|300|300|false|false|false|true|250"
"S510|5|تجاری محور معالی‌آباد|COM,OFF,MIX,TUR|3600|700|7|300|400|false|false|false|true|500"
# --- منطقه ۶ ---
"R610|6|مسکونی شهرک گلستان|RES,MIX|2600|600|5|200|300|false|false|false|true|350"
"S610|6|تجاری محور صدرا|COM,OFF,MIX|3200|700|6|250|400|false|false|false|true|400"
"EDU610|6|آموزشی و دانشگاهی|EDU,SPT|1500|400|4|1000|500|false|false|false|false|0"
# --- منطقه ۷: صنعتی ---
"I710|7|شهرک صنعتی جنوب غرب|IND|1500|550|3|1000|500|false|false|false|false|0"
"R710|7|مسکونی کارگری|RES|2000|600|4|150|200|false|false|false|true|250"
# --- منطقه ۸: حریم ---
"AGR810|8|اراضی کشاورزی حریم|AGR,GRD|0|30|1|2000|0|false|false|true|false|0"
"R810|8|توسعه مسکونی جدید|RES,MIX|2200|550|5|250|400|false|false|false|true|500"
# --- منطقه ۹ ---
"R910|9|مسکونی جنوب|RES|2200|600|4|150|200|false|false|false|true|300"
"S910|9|تجاری دروازه کازرون|COM,OFF,MIX|2800|700|5|200|300|false|false|false|true|350"
# --- منطقه ۱۰ ---
"R1010|10|مسکونی طرح ساماندهی شمال غرب|RES,MIX|2600|600|5|200|300|false|false|false|true|450"
"S1010|10|تجاری خدماتی شمال غرب|COM,OFF,MIX|3000|700|5|250|400|false|false|false|true|400"
# --- منطقه ۱۱ ---
"R1110|11|مسکونی محور فرودگاه|RES|2200|600|4|180|200|false|false|false|true|300"
"I1110|11|خدمات فرودگاهی و انبار|IND,OFF|1400|550|3|800|500|false|false|false|false|0"
)

seed_zones() {
  log "── پهنه‌های طرح تفصیلی ──"
  for row in "${ZONES_DATA[@]}"; do
    [[ "$row" =~ ^# ]] && continue
    IFS='|' read -r code region title uses far cov floors minlot setback heritage fault send recv tdrmax <<< "$row"
    invoke Regulation \
      "{\"function\":\"SetZone\",\"Args\":[\"$code\",\"$region\",\"$title\",\"$uses\",\"$far\",\"$cov\",\"$floors\",\"$minlot\",\"$setback\",\"$heritage\",\"$fault\",\"$send\",\"$recv\",\"$tdrmax\"]}" \
      "پهنه $code"
  done
}

# ═══════════════════════════════════════════════════════════════════════════
# ۳) دفترچه تعرفه سال
#
# همه ضرایب «در هزار» اند: ۱۸۰ یعنی ۰٫۱۸ × P.
# بازه ماده ۱۰۰ عمداً ۵۰۰ تا ۳۰۰۰ است — یعنی یک‌دوم تا سه برابر ارزش معاملاتی.
# این کف و سقف قانونی تبصره ۲ ماده ۱۰۰ برای بنای مسکونی است و قرارداد اجازه
# نمی‌دهد رأی کمیسیون بیرون از آن ثبت شود.
# ═══════════════════════════════════════════════════════════════════════════
seed_tariff() {
  log "── دفترچه تعرفه سال $YEAR ──"
  local T
  T=$(cat <<'JSON'
{
 "basePerMille":      {"RES":180,"COM":600,"OFF":380,"IND":220,"MIX":330,"EDU":90,"HLT":120,"TUR":420,"SPT":90,"REL":40},
 "excessPerMille":    {"RES":520,"COM":1500,"OFF":950,"IND":600,"MIX":900,"EDU":200,"HLT":300,"TUR":1100,"SPT":200,"REL":80},
 "balconyPerMille":   {"RES":90,"COM":300,"OFF":190,"IND":110,"MIX":165,"EDU":45,"HLT":60,"TUR":210,"SPT":45,"REL":20},
 "frontagePerMille":  {"COM":1800,"OFF":900,"MIX":1200,"TUR":1100,"IND":400},
 "noParkingPerMille": {"RES":1400,"COM":2600,"OFF":2000,"IND":1200,"MIX":1900,"TUR":2200,"EDU":800,"HLT":900,"SPT":700,"REL":400},
 "subdivPerMille":    {"RES":100,"COM":200,"OFF":200,"IND":200,"MIX":200,"TUR":200,"EDU":100,"HLT":100,"SPT":100,"REL":100},
 "supervisionPerMille": 35,
 "standardSpanCm": 300,
 "standardHeightCm": 400,
 "cashDiscountPerMille": 120,
 "wornTextureDiscountPerMille": 350,
 "greenBuildingDiscountPerMille": 100,
 "latePenaltyPerMilleMonthly": 20,
 "delayFeePerMilleYearly": 50,
 "tdrFeePerMille": 80,
 "article100MinPerMille": 500,
 "article100MaxPerMille": 3000,
 "fastTrackMaxArea": 200,
 "inquirySlaDays": 10
}
JSON
)
  local esc
  esc=$(echo "$T" | tr -d '\n' | tr -s ' ' | sed 's/"/\\"/g')
  invoke Regulation \
    "{\"function\":\"SetTariff\",\"Args\":[\"$YEAR\",\"$esc\"]}" \
    "تعرفه $YEAR"
}

# ═══════════════════════════════════════════════════════════════════════════
# ۴) سقف انتشار توکن (ظرفیت تراکم هر منطقه)
#
# این مهم‌ترین اهرم سیاستی شبکه است و اعدادش باید از ظرفیت زیرساخت بیاید،
# نه از کسری بودجه. سقف پایین‌تر در مناطق ۱ و ۳ عمدی است: مرکز تاریخی و
# باغات قصردشت جایی نیستند که ظرفیت جدید بار بگیرند. مناطق ۸ و ۱۰ که اراضی
# توسعه‌اند سقف بالاتری دارند.
#
# واحد: متر مربع.
# ═══════════════════════════════════════════════════════════════════════════
QUOTA_DATA=(
"1|RES|180000"   "1|COM|60000"    "1|MIX|40000"    "1|TUR|50000"
"2|RES|420000"   "2|COM|90000"    "2|MIX|70000"
"3|RES|260000"   "3|COM|80000"    "3|MIX|60000"    "3|TUR|70000"
"4|RES|520000"   "4|COM|110000"   "4|MIX|90000"    "4|IND|140000"
"5|RES|610000"   "5|COM|160000"   "5|OFF|90000"    "5|MIX|120000"
"6|RES|580000"   "6|COM|130000"   "6|MIX|100000"   "6|EDU|60000"
"7|RES|300000"   "7|IND|260000"   "7|COM|60000"
"8|RES|740000"   "8|COM|120000"   "8|MIX|110000"
"9|RES|380000"   "9|COM|70000"    "9|MIX|55000"
"10|RES|690000"  "10|COM|140000"  "10|MIX|105000"
"11|RES|340000"  "11|IND|150000"  "11|COM|60000"
)

seed_quotas() {
  log "── سقف انتشار توکن متراژ ──"
  for row in "${QUOTA_DATA[@]}"; do
    IFS='|' read -r region use ceiling <<< "$row"
    invoke Regulation \
      "{\"function\":\"SetQuota\",\"Args\":[\"$region\",\"$use\",\"$ceiling\"]}" \
      "سقف منطقه $region / $use = $ceiling م²"
  done
}

# ═══════════════════════════════════════════════════════════════════════════
report() {
  echo
  log "── وضعیت پس از بذرکاری ──"
  for q in ListRegions ListZones ListQuotas; do
    local n
    n=$(docker exec \
      -e CORE_PEER_LOCALMSPID=org1MSP \
      -e CORE_PEER_MSPCONFIGPATH=/etc/hyperledger/fabric/admin-msp \
      -e CORE_PEER_ADDRESS=peer0.org1.example.com:7051 \
      $TLS_ENV \
      peer0.org1.example.com peer chaincode query \
        -C "$CHANNEL" -n Regulation \
        -c "{\"function\":\"$q\",\"Args\":[]}" 2>/dev/null \
      | grep -o '"docType"' | wc -l)
    printf "  %-14s %s رکورد\n" "$q" "${n:-0}"
  done
}

# ═══════════════════════════════════════════════════════════════════════════
case "$WHAT" in
  regions) seed_regions ;;
  zones)   seed_zones ;;
  tariff)  seed_tariff ;;
  quotas)  seed_quotas ;;
  all)
    # ترتیب اجباری است: پهنه به منطقه ارجاع می‌دهد و سقف هم به منطقه.
    # SetZone اگر منطقه نباشد با «منطقه یافت نشد» رد می‌شود.
    seed_regions
    seed_tariff
    seed_zones
    seed_quotas
    ;;
  permitchannel|auditchannel|"")
    # bootstrap-secure.sh نام کانال را پاس می‌دهد، نه نام مرحله را.
    # کانال هسته یعنی همه‌چیز؛ کانال حسابرسی داده پایه‌ای ندارد.
    if [ "$WHAT" = "auditchannel" ]; then
      log "کانال حسابرسی داده پایه ندارد — رد شد"
      exit 0
    fi
    seed_regions; seed_tariff; seed_zones; seed_quotas
    ;;
  *) error "دستور ناشناخته: $WHAT  (regions|zones|tariff|quotas|all)" ;;
esac

[ "$VERIFY_ONLY" = "1" ] || report

echo
if [ "$FAILURES" -gt 0 ]; then
  warn "$FAILURES فراخوانی ناموفق بود — پیام‌های بالا را ببینید"
  exit 1
fi
success "بذرکاری تمام شد"
